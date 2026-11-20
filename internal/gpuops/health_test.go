package gpuops

import (
	"errors"
	"testing"
	"time"
)

func TestFatalXIDDrainsAtL2(t *testing.T) {
	now := time.Now()
	signals := []Signal{{
		Node: "gpu-1", Kind: "xid", Code: "79", Recoverable: false, Message: "GPU has fallen off the bus",
	}}
	pods := []PodFact{{Namespace: "training", Name: "p", Node: "gpu-1", GPUMilli: 1000, OwnerKind: "Job"}}
	report := BuildHealth(signals, pods, Policy{Level: "approve"}, now, map[string]time.Time{}, true)
	if len(report.Incidents) != 1 {
		t.Fatalf("incidents: %+v", report.Incidents)
	}
	inc := report.Incidents[0]
	if inc.Action != "cordon_and_drain" || inc.Tier != "L2" || !inc.NeedsConfirm {
		t.Fatalf("incident: %+v", inc)
	}
	if err := Allows(inc, "drain", false); err == nil {
		t.Fatal("drain without confirm should fail")
	}
	if err := Allows(inc, "drain", true); err != nil {
		t.Fatal(err)
	}
}

func TestRecoverableWarningWaitsForPersistence(t *testing.T) {
	now := time.Now()
	sig := []Signal{{Node: "gpu-2", Kind: "nvlink", Code: "crc", Recoverable: true, Message: "crc"}}
	seen := map[string]time.Time{}
	policy := Policy{Level: "approve", Persistence: 2 * time.Minute}
	first := BuildHealth(sig, nil, policy, now, seen, false)
	if len(first.Incidents) != 0 {
		t.Fatalf("spike should be dropped, got %+v", first.Incidents)
	}
	later := BuildHealth(sig, nil, policy, now.Add(3*time.Minute), seen, false)
	if len(later.Incidents) != 1 || later.Incidents[0].Action != "cordon" {
		t.Fatalf("persisted warning: %+v", later.Incidents)
	}
	BuildHealth(nil, nil, policy, now.Add(4*time.Minute), seen, false)
	back := BuildHealth(sig, nil, policy, now.Add(5*time.Minute), seen, false)
	if len(back.Incidents) != 0 {
		t.Fatalf("a cleared signal must wait again, got %+v", back.Incidents)
	}
}

func TestSuggestLevelNeverExecutes(t *testing.T) {
	inc := BuildHealth(
		[]Signal{{Node: "n", Kind: "xid", Code: "79", Recoverable: false, Message: "off the bus"}},
		nil, Policy{Level: "suggest"}, time.Now(), map[string]time.Time{}, false,
	).Incidents[0]
	if inc.Tier != "L1" || inc.Executable {
		t.Fatalf("L1: %+v", inc)
	}
	if err := Allows(inc, "cordon", true); !errors.Is(err, ErrForbidden) {
		t.Fatalf("L1 must refuse execution, got %v", err)
	}
}

func TestAutoOnlyInsideAllowlist(t *testing.T) {
	sig := []Signal{{Node: "n", Kind: "ecc", Recoverable: false, Message: "ecc"}}
	pods := []PodFact{{Namespace: "llm-inference", Name: "a", Node: "n", OwnerKind: "ReplicaSet"}}
	policy := Policy{Level: "auto", AutoNamespaces: map[string]bool{"llm-inference": true}}
	inc := BuildHealth(sig, pods, policy, time.Now(), nil, true).Incidents[0]
	if inc.Tier != "L3" || inc.NeedsConfirm {
		t.Fatalf("expected L3, got %+v", inc)
	}
	pods[0].Namespace = "other"
	inc = BuildHealth(sig, pods, policy, time.Now(), nil, true).Incidents[0]
	if inc.Tier != "L2" || !inc.NeedsConfirm {
		t.Fatalf("namespace outside allow-list should fall back to L2, got %+v", inc)
	}
}

func TestDaemonSetDoesNotBlockAllowlist(t *testing.T) {
	sig := []Signal{{Node: "n", Kind: "xid", Code: "48", Recoverable: false, Message: "ecc"}}
	pods := []PodFact{
		{Namespace: "llm-inference", Name: "a", Node: "n", OwnerKind: "ReplicaSet"},
		{Namespace: "kube-system", Name: "plugin", Node: "n", OwnerKind: "DaemonSet"},
	}
	policy := Policy{Level: "auto", AutoNamespaces: map[string]bool{"llm-inference": true}}
	inc := BuildHealth(sig, pods, policy, time.Now(), nil, false).Incidents[0]
	if !inc.AutoEligible {
		t.Fatalf("daemonset should be ignored: %+v", inc.AffectedNamespaces)
	}
}
