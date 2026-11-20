package gpuops

import (
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"
)

// BuildHealth groups signals by node, drops transient recoverable warnings,
// and assigns an L1/L2/L3 action. firstSeen is updated in place.
func BuildHealth(signals []Signal, pods []PodFact, policy Policy, now time.Time, firstSeen map[string]time.Time, zh bool) HealthReport {
	if firstSeen == nil {
		firstSeen = map[string]time.Time{}
	}
	kept := make([]Signal, 0, len(signals))
	current := map[string]bool{}
	for _, sig := range signals {
		if sig.Node == "" {
			continue
		}
		current[signalKey(sig)] = true
		if keepSignal(sig, now, firstSeen, policy.Persistence) {
			kept = append(kept, sig)
		}
	}
	// A signal that clears and comes back must wait out persistence again.
	for key := range firstSeen {
		if !current[key] {
			delete(firstSeen, key)
		}
	}
	byNode := map[string][]Signal{}
	for _, sig := range kept {
		byNode[sig.Node] = append(byNode[sig.Node], sig)
	}
	names := make([]string, 0, len(byNode))
	for n := range byNode {
		names = append(names, n)
	}
	sort.Strings(names)

	incidents := make([]Incident, 0, len(names))
	for _, node := range names {
		incidents = append(incidents, buildIncident(node, byNode[node], pods, policy, zh))
	}
	return HealthReport{
		Policy:      policy.level(),
		SignalCount: len(kept),
		Incidents:   incidents,
	}
}

func signalKey(sig Signal) string {
	return sig.Node + "|" + sig.Kind + "|" + sig.GPU + "|" + sig.Code
}

func keepSignal(sig Signal, now time.Time, firstSeen map[string]time.Time, min time.Duration) bool {
	key := signalKey(sig)
	seen, ok := firstSeen[key]
	if !ok {
		firstSeen[key] = now
		seen = now
	}
	if !sig.Recoverable || min <= 0 {
		return true
	}
	return now.Sub(seen) >= min
}

func buildIncident(node string, signals []Signal, pods []PodFact, policy Policy, zh bool) Incident {
	critical := false
	for _, sig := range signals {
		if !sig.Recoverable {
			critical = true
			break
		}
	}
	ns := namespacesOnNode(pods, node)
	eligible := autoEligible(ns, policy)
	inc := Incident{
		Node:               node,
		Signals:            signals,
		AffectedNamespaces: ns,
		AutoEligible:       eligible,
	}
	if critical {
		inc.Severity = "critical"
		inc.Action = "cordon_and_drain"
		inc.Confidence = 0.86
	} else {
		inc.Severity = "warning"
		inc.Action = "cordon"
		inc.Confidence = 0.62
	}
	switch policy.level() {
	case "suggest":
		inc.Tier = "L1"
	case "auto":
		if eligible {
			inc.Tier = "L3"
			inc.Executable = true
		} else {
			inc.Tier = "L2"
			inc.Executable = true
			inc.NeedsConfirm = true
		}
	default:
		inc.Tier = "L2"
		inc.Executable = true
		inc.NeedsConfirm = true
	}
	inc.Summary = summarize(signals, zh)
	inc.Runbook = runbook(inc, zh)
	return inc
}

func namespacesOnNode(pods []PodFact, node string) []string {
	seen := map[string]bool{}
	var out []string
	for _, p := range pods {
		if p.Node != node || p.Namespace == "" || p.OwnerKind == "DaemonSet" {
			continue
		}
		if seen[p.Namespace] {
			continue
		}
		seen[p.Namespace] = true
		out = append(out, p.Namespace)
	}
	sort.Strings(out)
	return out
}

func autoEligible(namespaces []string, policy Policy) bool {
	if policy.level() != "auto" || len(policy.AutoNamespaces) == 0 {
		return false
	}
	for _, ns := range namespaces {
		if !policy.AutoNamespaces[ns] {
			return false
		}
	}
	return true
}

func summarize(signals []Signal, zh bool) string {
	parts := make([]string, 0, len(signals))
	for _, sig := range signals {
		if sig.Message != "" {
			parts = append(parts, sig.Message)
		}
	}
	if len(parts) > 3 {
		parts = parts[:3]
	}
	if zh {
		return "检测到 GPU 硬件信号：" + strings.Join(parts, "；")
	}
	return "GPU hardware signals: " + strings.Join(parts, "; ")
}

func runbook(inc Incident, zh bool) string {
	action := "kubectl cordon " + inc.Node
	if inc.Action == "cordon_and_drain" {
		action = "kubectl cordon " + inc.Node + " && kubectl drain " + inc.Node + " --ignore-daemonsets --delete-emptydir-data --grace-period=30"
	}
	if zh {
		return fmt.Sprintf("【%s】%s。建议动作：%s。置信度 %.0f%%。本 Runbook 只描述动作；是否执行由自愈层级（当前 %s）决定。",
			inc.Tier, inc.Summary, action, inc.Confidence*100, inc.Tier)
	}
	return fmt.Sprintf("[%s] %s. Suggested action: %s. Confidence %.0f%%. This runbook does not execute anything; tier %s decides.",
		inc.Tier, inc.Summary, action, inc.Confidence*100, inc.Tier)
}

var (
	// ErrForbidden means the heal tier or mode does not allow execution at all.
	ErrForbidden = errors.New("execution not allowed")
	// ErrRejected means the request does not match what the incident recommends.
	ErrRejected = errors.New("action rejected")
)

// Allows reports whether an action may be executed for this incident.
func Allows(inc Incident, action string, confirm bool) error {
	if inc.Tier == "L1" || !inc.Executable {
		return fmt.Errorf("%w: heal level only generates a runbook", ErrForbidden)
	}
	switch action {
	case "cordon":
		if inc.Action != "cordon" && inc.Action != "cordon_and_drain" {
			return fmt.Errorf("%w: cordon is not recommended for this node", ErrRejected)
		}
	case "drain":
		if inc.Action != "cordon_and_drain" {
			return fmt.Errorf("%w: drain is not recommended for this node", ErrRejected)
		}
	default:
		return fmt.Errorf("%w: unsupported action %q", ErrRejected, action)
	}
	if inc.NeedsConfirm && !confirm {
		return fmt.Errorf("%w: confirmation required", ErrRejected)
	}
	return nil
}
