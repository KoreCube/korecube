package ai

import (
	"regexp"
	"strconv"
	"strings"
)

var (
	reNSBefore   = regexp.MustCompile(`([a-z0-9][a-z0-9-]*)\s*(?:命名空间|名字空间|namespace)`)
	reNSAfter    = regexp.MustCompile(`(?:namespace|-n)\s+([a-z0-9][a-z0-9-]*)`)
	reTarget     = regexp.MustCompile(`deployment[/\s]+([a-z0-9][a-z0-9-]*)`)
	reMax        = regexp.MustCompile(`(?:最多|上限|max(?:imum)?\s*(?:replicas?)?\s*(?:to|=)?)\s*(\d+)`)
	reMin        = regexp.MustCompile(`(?:最少|至少|下限|min(?:imum)?\s*(?:replicas?)?\s*(?:to|=)?)\s*(\d+)`)
	reGPULimit   = regexp.MustCompile(`(\d+)\s*(?:张|块|个)?\s*(?:gpu|卡)`)
	rePool       = regexp.MustCompile(`(?:nodepool|node pool|节点池)\s*([a-z0-9][a-z0-9-]*)`)
	reThreshold  = regexp.MustCompile(`(?:每个副本|per replica|阈值|threshold)\D{0,6}(\d+)`)
	stopNSTokens = map[string]bool{"the": true, "this": true, "a": true, "my": true}
)

// mockScalingArgs reads a scaling intent from free text for the offline agent.
func mockScalingArgs(raw, q, uiNS string) map[string]interface{} {
	args := map[string]interface{}{"kind": "keda"}
	if strings.Contains(q, "karpenter") || strings.Contains(q, "nodepool") || strings.Contains(q, "node pool") || strings.Contains(raw, "节点池") {
		args["kind"] = "karpenter"
		if m := rePool.FindStringSubmatch(q); m != nil && m[1] != "" {
			args["pool"] = m[1]
		}
		if m := reGPULimit.FindStringSubmatch(q); m != nil {
			args["gpu_limit"] = atoi(m[1])
		}
		if strings.Contains(q, "spot") || strings.Contains(raw, "竞价") {
			args["capacity_type"] = "spot"
		}
		return args
	}
	ns := uiNS
	if m := reNSBefore.FindStringSubmatch(q); m != nil && !stopNSTokens[m[1]] {
		ns = m[1]
	} else if m := reNSAfter.FindStringSubmatch(q); m != nil {
		ns = m[1]
	}
	if ns == "" {
		ns = "default"
	}
	args["namespace"] = ns
	if m := reTarget.FindStringSubmatch(q); m != nil {
		args["target"] = m[1]
	}
	switch {
	case strings.Contains(q, "kafka"):
		args["trigger"] = "kafka"
	case strings.Contains(q, "rabbitmq"):
		args["trigger"] = "rabbitmq"
	case strings.Contains(q, "redis"):
		args["trigger"] = "redis"
	default:
		args["trigger"] = "prometheus"
	}
	if m := reMax.FindStringSubmatch(q); m != nil {
		args["max_replicas"] = atoi(m[1])
	}
	if m := reMin.FindStringSubmatch(q); m != nil {
		args["min_replicas"] = atoi(m[1])
	}
	if m := reThreshold.FindStringSubmatch(q); m != nil {
		args["threshold"] = m[1]
	}
	return args
}

func atoi(s string) int {
	n, _ := strconv.Atoi(s)
	return n
}
