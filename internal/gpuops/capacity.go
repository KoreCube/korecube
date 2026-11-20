package gpuops

import (
	"fmt"
	"sort"
)

const saturationPercent = 85

// NodeCapacity is allocatable vs requested whole GPUs on one node.
type NodeCapacity struct {
	Node        string  `json:"node"`
	Product     string  `json:"product,omitempty"`
	Allocatable float64 `json:"allocatable"`
	Requested   float64 `json:"requested"`
	Free        float64 `json:"free"`
	Health      string  `json:"health"` // ok, warning, critical
}

// CapacityRisk is one finding of the capacity inspection.
type CapacityRisk struct {
	Severity string `json:"severity"` // warning, critical
	Kind     string `json:"kind"`     // unhealthy_capacity, shortage, fragmentation, saturated, no_n1_headroom
	Target   string `json:"target,omitempty"`
	Message  string `json:"message"`
}

// CapacityReport is the proactive GPU capacity and risk inspection.
// Usable and Free only count nodes without a critical incident.
type CapacityReport struct {
	Source      string         `json:"source"`
	Allocatable float64        `json:"allocatable"`
	Requested   float64        `json:"requested"`
	Usable      float64        `json:"usable"`
	Free        float64        `json:"free"`
	PendingPods int            `json:"pendingPods"`
	PendingGPU  float64        `json:"pendingGpu"`
	Nodes       []NodeCapacity `json:"nodes"`
	Risks       []CapacityRisk `json:"risks"`
}

func holdsGPU(p PodFact) bool {
	return p.Node != "" && p.Phase != "Succeeded" && p.Phase != "Failed"
}

// BuildCapacity sums GPU requests per node, folds in health incidents, and
// checks whether unscheduled GPU pods fit and whether losing the busiest node
// would strand workloads.
func BuildCapacity(pods []PodFact, gpuOnNode map[string]int64, product map[string]string, incidents []Incident, zh bool) CapacityReport {
	health := map[string]string{}
	for _, inc := range incidents {
		if inc.Severity == "critical" {
			health[inc.Node] = "critical"
		} else if health[inc.Node] == "" {
			health[inc.Node] = "warning"
		}
	}
	requested := map[string]int64{}
	var pendingMilli, biggestPending int64
	var biggestPod string
	pending := 0
	for _, p := range pods {
		if p.GPUMilli <= 0 || p.MIG {
			continue
		}
		switch {
		case holdsGPU(p):
			requested[p.Node] += p.GPUMilli
		case p.Phase == "Pending" && p.Node == "":
			pending++
			pendingMilli += p.GPUMilli
			if p.GPUMilli > biggestPending {
				biggestPending, biggestPod = p.GPUMilli, p.Namespace+"/"+p.Name
			}
		}
	}

	r := CapacityReport{PendingPods: pending, PendingGPU: float64(pendingMilli) / 1000}
	var bestFree float64
	for node, alloc := range gpuOnNode {
		if alloc <= 0 {
			continue
		}
		nc := NodeCapacity{
			Node: node, Product: product[node],
			Allocatable: float64(alloc), Requested: float64(requested[node]) / 1000,
			Health: health[node],
		}
		if nc.Health == "" {
			nc.Health = "ok"
		}
		if nc.Free = nc.Allocatable - nc.Requested; nc.Free < 0 {
			nc.Free = 0
		}
		r.Allocatable += nc.Allocatable
		r.Requested += nc.Requested
		if nc.Health != "critical" {
			r.Usable += nc.Allocatable
			r.Free += nc.Free
			if nc.Free > bestFree {
				bestFree = nc.Free
			}
		}
		r.Nodes = append(r.Nodes, nc)
	}
	sort.Slice(r.Nodes, func(i, j int) bool { return r.Nodes[i].Node < r.Nodes[j].Node })

	add := func(sev, kind, target, zhMsg, enMsg string) {
		msg := enMsg
		if zh {
			msg = zhMsg
		}
		r.Risks = append(r.Risks, CapacityRisk{Severity: sev, Kind: kind, Target: target, Message: msg})
	}

	if lost := r.Allocatable - r.Usable; lost > 0 {
		sev := "warning"
		if lost*4 >= r.Allocatable {
			sev = "critical"
		}
		add(sev, "unhealthy_capacity", "",
			fmt.Sprintf("%.0f / %.0f 张卡在严重故障节点上，不应再调度", lost, r.Allocatable),
			fmt.Sprintf("%.0f of %.0f GPUs sit on nodes with a critical incident and should not take new work", lost, r.Allocatable))
	}

	if biggestPending > 0 {
		need := float64(biggestPending) / 1000
		switch {
		case r.Free < r.PendingGPU && bestFree < need:
			add("critical", "shortage", biggestPod,
				fmt.Sprintf("%d 个 Pod 等待 %.0f 张卡，健康节点只剩 %.0f 张空闲；需要扩 GPU 节点（可让调查员起草 Karpenter NodePool）", pending, r.PendingGPU, r.Free),
				fmt.Sprintf("%d pods wait for %.0f GPUs but healthy nodes have %.0f free; add GPU nodes (the investigator can draft a Karpenter NodePool)", pending, r.PendingGPU, r.Free))
		case bestFree < need:
			add("warning", "fragmentation", biggestPod,
				fmt.Sprintf("%s 需要单节点 %.0f 张卡，空闲总数 %.0f 够但最大单节点只有 %.0f，资源碎片化", biggestPod, need, r.Free, bestFree),
				fmt.Sprintf("%s needs %.0f GPUs on one node; %.0f are free in total but at most %.0f on a single node", biggestPod, need, r.Free, bestFree))
		}
	}

	if r.Usable > 0 {
		used := (r.Usable - r.Free) / r.Usable * 100
		if used >= saturationPercent {
			add("warning", "saturated", "",
				fmt.Sprintf("健康节点 GPU 分配率 %.0f%%，接近上限", used),
				fmt.Sprintf("healthy GPU nodes are %.0f%% allocated", used))
		}
	}

	for _, nc := range r.Nodes {
		if nc.Health == "critical" || nc.Requested == 0 {
			continue
		}
		elsewhere := r.Free - nc.Free
		if nc.Requested > elsewhere {
			add("warning", "no_n1_headroom", nc.Node,
				fmt.Sprintf("%s 一旦故障，%.0f 张卡的负载只能挪到其余 %.0f 张空闲卡上", nc.Node, nc.Requested, elsewhere),
				fmt.Sprintf("if %s fails, workloads holding %.0f GPUs have only %.0f free GPUs to move to", nc.Node, nc.Requested, elsewhere))
		}
	}
	sort.SliceStable(r.Risks, func(i, j int) bool {
		return r.Risks[i].Severity == "critical" && r.Risks[j].Severity != "critical"
	})
	return r
}

// FormatCapacity renders the inspection for the investigator.
func FormatCapacity(r CapacityReport, zh bool) string {
	if len(r.Nodes) == 0 {
		if zh {
			return "集群里没有可分配 nvidia.com/gpu 的节点。"
		}
		return "No node advertises allocatable nvidia.com/gpu."
	}
	var lines []string
	if zh {
		lines = append(lines, fmt.Sprintf("GPU 容量：可分配 %.0f，已申请 %.0f，健康节点空闲 %.0f / %.0f，等待调度 %d 个 Pod（%.0f 卡）",
			r.Allocatable, r.Requested, r.Free, r.Usable, r.PendingPods, r.PendingGPU))
	} else {
		lines = append(lines, fmt.Sprintf("GPU capacity: allocatable %.0f, requested %.0f, free on healthy nodes %.0f / %.0f, pending %d pods (%.0f GPUs)",
			r.Allocatable, r.Requested, r.Free, r.Usable, r.PendingPods, r.PendingGPU))
	}
	for _, nc := range r.Nodes {
		lines = append(lines, fmt.Sprintf("- %s %.0f/%.0f free=%.0f health=%s", nc.Node, nc.Requested, nc.Allocatable, nc.Free, nc.Health))
	}
	if len(r.Risks) == 0 {
		if zh {
			lines = append(lines, "未发现容量风险。")
		} else {
			lines = append(lines, "No capacity risk found.")
		}
	}
	for _, risk := range r.Risks {
		lines = append(lines, fmt.Sprintf("[%s] %s", risk.Severity, risk.Message))
	}
	return joinLines(lines)
}
