package gpuops

import (
	"strings"
	"testing"
)

func TestWasteFlagsExclusiveLowUtil(t *testing.T) {
	pods := []PodFact{
		{Namespace: "llm-inference", Name: "a", GPUMilli: 1000},
		{Namespace: "llm-inference", Name: "b", GPUMilli: 1000, MIG: true},
		{Namespace: "training", Name: "c", GPUMilli: 8000},
	}
	report := BuildWaste(pods, map[string]float64{"llm-inference": 18, "training": 91}, Policy{WasteThreshold: 40})
	if len(report.Namespaces) != 2 {
		t.Fatalf("rows: %+v", report.Namespaces)
	}
	llm := report.Namespaces[0]
	if !llm.Waste || llm.Suggestion != "mig_or_timeslice" || llm.ExclusivePods != 1 || llm.AllocatedGPU != 1 {
		t.Fatalf("llm row: %+v", llm)
	}
	if report.Namespaces[1].Waste {
		t.Fatalf("training should not be waste: %+v", report.Namespaces[1])
	}
}

func TestWasteWithoutDCGMDoesNotPretend(t *testing.T) {
	report := BuildWaste([]PodFact{{Namespace: "llm", Name: "a", GPUMilli: 1000}}, nil, Policy{})
	if report.Namespaces[0].Waste || report.Namespaces[0].Suggestion != "need_dcgm" {
		t.Fatalf("row: %+v", report.Namespaces[0])
	}
}

func TestStormFoldsPodSymptomsIntoGPUNode(t *testing.T) {
	owners := map[string]OwnerLink{
		"pod-0": {UID: "pod-0", Kind: "Pod", Name: "pretrain-0", Namespace: "training", Parents: []string{"job-1"}},
		"pod-1": {UID: "pod-1", Kind: "Pod", Name: "pretrain-1", Namespace: "training", Parents: []string{"job-1"}},
		"job-1": {UID: "job-1", Kind: "Job", Name: "pretrain", Namespace: "training"},
		"pod-u": {UID: "pod-u", Kind: "Pod", Name: "unrelated", Namespace: "default"},
	}
	events := []StormEvent{
		{Reason: "Xid", Message: "Xid 79", Kind: "Node", Name: "gpu-a100-1", UID: "ev-1", Node: "gpu-a100-1"},
		{Reason: "NodeNotReady", Message: "not ready", Kind: "Node", Name: "gpu-a100-1", UID: "ev-2", Node: "gpu-a100-1"},
		{Reason: "Evicted", Message: "evicted", Kind: "Pod", Name: "pretrain-0", UID: "pod-0", Node: "gpu-a100-1"},
		{Reason: "FailedScheduling", Message: "pending", Kind: "Pod", Name: "pretrain-1", UID: "pod-1", Node: "gpu-a100-1"},
		{Reason: "Killing", Message: "killing", Kind: "Pod", Name: "pretrain-0", UID: "pod-0", Node: "gpu-a100-1"},
		{Reason: "BackOff", Message: "backoff", Kind: "Pod", Name: "unrelated", UID: "pod-u", Node: "cpu-1"},
	}
	report := GroupStorm(events, owners)
	if report.Groups != 2 {
		t.Fatalf("groups=%d suppressed=%.2f items=%+v", report.Groups, report.Suppressed, report.Items)
	}
	if report.Suppressed < 0.6 {
		t.Fatalf("expected most of the storm to collapse, got %.2f", report.Suppressed)
	}
	foundNode := false
	for _, g := range report.Items {
		if g.Key == "node:gpu-a100-1" && g.Count == 5 {
			foundNode = true
		}
	}
	if !foundNode {
		t.Fatalf("node group missing: %+v", report.Items)
	}
}

func TestPlacementCallsOutCrossGPUPod(t *testing.T) {
	snap := ShowcaseSnapshot()
	_, byPod := ShowcaseUtil()
	text := ExplainPlacement(snap.Pods, "llm-inference", "llm-inf-7f-c", snap.GPUOnNode, snap.Product, byPod, true)
	if !strings.Contains(text, "8 倍") || !strings.Contains(text, "利用率") {
		t.Fatalf("placement text: %s", text)
	}
}

func TestParsePromVector(t *testing.T) {
	raw := []byte(`{"resultType":"vector","result":[{"metric":{"Hostname":"gpu-1","gpu":"0","namespace":"llm","pod":"a"},"value":[1,"79"]}]}`)
	samples, err := ParsePromVector(raw)
	if err != nil || len(samples) != 1 || samples[0].Value != 79 {
		t.Fatalf("samples: %+v %v", samples, err)
	}
	sigs := SignalsFromDCGM("xid", samples)
	if len(sigs) != 1 || sigs[0].Recoverable || sigs[0].Node != "gpu-1" {
		t.Fatalf("signal: %+v", sigs)
	}
	if got := UtilByNamespace(samples)["llm"]; got != 79 {
		t.Fatalf("util: %v", got)
	}
}
