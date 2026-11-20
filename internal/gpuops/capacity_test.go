package gpuops

import "testing"

func riskKinds(r CapacityReport) map[string]string {
	out := map[string]string{}
	for _, risk := range r.Risks {
		out[risk.Kind] = risk.Severity
	}
	return out
}

func TestCapacityShowcaseFlagsShortageAndUnhealthyNode(t *testing.T) {
	snap := ShowcaseSnapshot()
	incidents := []Incident{
		{Node: "gpu-a100-1", Severity: "critical"},
		{Node: "gpu-a100-2", Severity: "warning"},
	}
	r := BuildCapacity(snap.Pods, snap.GPUOnNode, snap.Product, incidents, false)
	if r.Allocatable != 16 || r.Usable != 8 || r.Free != 5 || r.PendingPods != 1 || r.PendingGPU != 8 {
		t.Fatalf("totals: %+v", r)
	}
	kinds := riskKinds(r)
	if kinds["unhealthy_capacity"] != "critical" || kinds["shortage"] != "critical" || kinds["no_n1_headroom"] != "warning" {
		t.Fatalf("risks: %+v", r.Risks)
	}
	if r.Risks[0].Severity != "critical" {
		t.Fatalf("critical risks first: %+v", r.Risks)
	}
}

func TestCapacityFragmentation(t *testing.T) {
	pods := []PodFact{
		{Namespace: "a", Name: "x", Node: "n1", Phase: "Running", GPUMilli: 4000},
		{Namespace: "a", Name: "y", Node: "n2", Phase: "Running", GPUMilli: 4000},
		{Namespace: "b", Name: "big", Phase: "Pending", GPUMilli: 6000},
		{Namespace: "b", Name: "done", Node: "n1", Phase: "Succeeded", GPUMilli: 4000},
	}
	r := BuildCapacity(pods, map[string]int64{"n1": 8, "n2": 8}, nil, nil, false)
	if r.Free != 8 {
		t.Fatalf("succeeded pods must not hold GPUs: %+v", r)
	}
	if riskKinds(r)["fragmentation"] != "warning" {
		t.Fatalf("expected fragmentation: %+v", r.Risks)
	}
	if _, ok := riskKinds(r)["shortage"]; ok {
		t.Fatalf("total free covers the request, not a shortage: %+v", r.Risks)
	}
}

func TestCapacityQuietWhenHeadroom(t *testing.T) {
	pods := []PodFact{{Namespace: "a", Name: "x", Node: "n1", Phase: "Running", GPUMilli: 2000}}
	r := BuildCapacity(pods, map[string]int64{"n1": 8, "n2": 8}, nil, nil, true)
	if len(r.Risks) != 0 {
		t.Fatalf("unexpected risks: %+v", r.Risks)
	}
}
