package gpuops

import (
	"fmt"
	"strings"
)

func sprintf(format string, args ...interface{}) string {
	return fmt.Sprintf(format, args...)
}

func joinLines(lines []string) string {
	return strings.Join(lines, "\n")
}

func joinComma(parts []string) string {
	return strings.Join(parts, ", ")
}

// FormatHealth renders incidents for the investigator.
func FormatHealth(r HealthReport, zh bool) string {
	if len(r.Incidents) == 0 {
		if zh {
			return "没有持续存在的 GPU 硬件故障信号。可恢复告警会先经过持续性过滤，瞬时尖峰不会升级。"
		}
		return "No persistent GPU hardware faults. Recoverable warnings are held until they persist; spikes are dropped."
	}
	lines := make([]string, 0, len(r.Incidents))
	for _, inc := range r.Incidents {
		if zh {
			lines = append(lines, fmt.Sprintf("%s %s 动作=%s 层级=%s 置信度=%.0f%%",
				inc.Node, inc.Severity, inc.Action, inc.Tier, inc.Confidence*100))
			continue
		}
		lines = append(lines, fmt.Sprintf("%s %s action=%s tier=%s confidence=%.0f%%",
			inc.Node, inc.Severity, inc.Action, inc.Tier, inc.Confidence*100))
	}
	return joinLines(lines)
}

// ExplainPlacement answers why one GPU pod may be slower than its siblings.
// utilByPod keys are "namespace/name" percentages; missing metrics still yield a topology checklist.
func ExplainPlacement(pods []PodFact, ns, name string, gpuOnNode map[string]int64, product map[string]string, utilByPod map[string]float64, zh bool) string {
	var self *PodFact
	for i := range pods {
		if pods[i].Namespace == ns && pods[i].Name == name {
			self = &pods[i]
			break
		}
	}
	if self == nil {
		if zh {
			return fmt.Sprintf("没有找到 Pod %s/%s。", ns, name)
		}
		return fmt.Sprintf("Pod %s/%s was not found.", ns, name)
	}
	var siblings []PodFact
	var sameNode []PodFact
	for _, p := range pods {
		if p.GPUMilli <= 0 || (p.Namespace == self.Namespace && p.Name == self.Name) {
			continue
		}
		if p.Node == self.Node {
			sameNode = append(sameNode, p)
		}
		if self.RootUID != "" && p.RootUID == self.RootUID {
			siblings = append(siblings, p)
		}
	}
	util := utilByPod[ns+"/"+name]
	selfKnown := false
	if _, ok := utilByPod[ns+"/"+name]; ok {
		selfKnown = true
	}
	var peerUtils []float64
	for _, p := range siblings {
		if v, ok := utilByPod[p.Namespace+"/"+p.Name]; ok {
			peerUtils = append(peerUtils, v)
		}
	}
	slow := false
	if selfKnown && len(peerUtils) > 0 {
		sum := 0.0
		for _, v := range peerUtils {
			sum += v
		}
		avg := sum / float64(len(peerUtils))
		if avg > 0 && util < avg*0.6 {
			slow = true
		}
	}
	cards := gpuOnNode[self.Node]
	prod := product[self.Node]
	if zh {
		lines := []string{
			fmt.Sprintf("Pod %s/%s 在节点 %s，GPU 申请 %.2f 卡，节点可分配 %d 卡，型号 %s。",
				ns, name, emptyDash(self.Node), float64(self.GPUMilli)/1000, cards, emptyDash(prod)),
		}
		if len(sameNode) > 0 {
			lines = append(lines, fmt.Sprintf("同节点还有 %d 个 GPU Pod，可能争用 PCIe / NVLink。", len(sameNode)))
		}
		if self.GPUMilli > 1000 {
			lines = append(lines, "该 Pod 申请了多张卡。如果调度跨了 NUMA 或 PCIe Switch，集合通信可能差到约 8 倍。节点标签里没有 NVLink clique 提示时，应在节点上跑 nvidia-smi topo -m 确认。")
		}
		if slow {
			lines = append(lines, fmt.Sprintf("同负载其他 Pod 的 GPU 利用率均值明显高于本 Pod（本 Pod %.0f%%）。优先核对拓扑，而不是先怀疑模型代码。", util))
		} else if !selfKnown {
			lines = append(lines, "没有该 Pod 的 DCGM 利用率，无法量化「比同伴慢多少」。拓扑核对仍然有效。")
		}
		return joinLines(lines)
	}
	lines := []string{
		fmt.Sprintf("Pod %s/%s on node %s requests %.2f GPU, node allocatable %d, product %s.",
			ns, name, emptyDash(self.Node), float64(self.GPUMilli)/1000, cards, emptyDash(prod)),
	}
	if len(sameNode) > 0 {
		lines = append(lines, fmt.Sprintf("%d other GPU pods share the node and may contend for PCIe / NVLink.", len(sameNode)))
	}
	if self.GPUMilli > 1000 {
		lines = append(lines, "This pod requests multiple GPUs. Crossing a NUMA node or PCIe switch can cost up to ~8x on collectives. Without an NVLink clique label, check nvidia-smi topo -m on the node.")
	}
	if slow {
		lines = append(lines, fmt.Sprintf("Sibling pods of the same workload show higher GPU util than this pod (%.0f%%). Check topology before blaming the model.", util))
	} else if !selfKnown {
		lines = append(lines, "No DCGM utilization for this pod, so a 40% slowdown cannot be quantified. The topology checklist still applies.")
	}
	return joinLines(lines)
}

func emptyDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}
