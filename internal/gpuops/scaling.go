package gpuops

import (
	"fmt"
	"regexp"
	"strconv"
	"strings"
	"time"
)

// ScalingRequest is the intent the investigator extracts from chat, e.g.
// "scale llm-inference by queue length" or "raise the GPU node pool limit".
type ScalingRequest struct {
	Kind      string `json:"kind"` // keda, karpenter
	Namespace string `json:"namespace,omitempty"`
	Target    string `json:"target,omitempty"`

	Trigger       string `json:"trigger,omitempty"` // prometheus, kafka, rabbitmq, redis
	Query         string `json:"query,omitempty"`
	Threshold     string `json:"threshold,omitempty"`
	MinReplicas   int    `json:"minReplicas,omitempty"`
	MaxReplicas   int    `json:"maxReplicas,omitempty"`
	ServerAddress string `json:"serverAddress,omitempty"`
	Topic         string `json:"topic,omitempty"`
	ConsumerGroup string `json:"consumerGroup,omitempty"`
	Bootstrap     string `json:"bootstrapServers,omitempty"`
	Queue         string `json:"queue,omitempty"`
	Address       string `json:"address,omitempty"`

	Pool          string   `json:"pool,omitempty"`
	GPULimit      int      `json:"gpuLimit,omitempty"`
	InstanceTypes []string `json:"instanceTypes,omitempty"`
	CapacityType  string   `json:"capacityType,omitempty"`
	NodeClass     string   `json:"nodeClass,omitempty"`
	NodeClassKind string   `json:"nodeClassKind,omitempty"`
	NodeClassGrp  string   `json:"nodeClassGroup,omitempty"`
}

// ScalingPlan is a proposal waiting for a human decision in the console.
type ScalingPlan struct {
	ID         string                 `json:"id"`
	Kind       string                 `json:"kind"`
	APIVersion string                 `json:"apiVersion"`
	Resource   string                 `json:"resource"`
	Namespace  string                 `json:"namespace,omitempty"`
	Name       string                 `json:"name"`
	Mode       string                 `json:"mode"` // create, update
	Summary    string                 `json:"summary"`
	YAML       string                 `json:"yaml"`
	Warnings   []string               `json:"warnings,omitempty"`
	Status     string                 `json:"status"` // pending, applied, rejected, failed
	CreatedAt  time.Time              `json:"createdAt"`
	DecidedAt  *time.Time             `json:"decidedAt,omitempty"`
	DecidedBy  string                 `json:"decidedBy,omitempty"`
	Detail     string                 `json:"detail,omitempty"`
	Object     map[string]interface{} `json:"-"`
	Cluster    string                 `json:"-"`
}

var dnsLabel = regexp.MustCompile(`^[a-z0-9]([-a-z0-9]*[a-z0-9])?$`)

func validName(s string) bool {
	return len(s) <= 63 && dnsLabel.MatchString(s)
}

const managedBy = "korecube"

// DefaultQueueQuery is the vLLM waiting-request gauge used when no query is given.
func DefaultQueueQuery(namespace string) string {
	return fmt.Sprintf(`sum(vllm:num_requests_waiting{namespace=%q})`, namespace)
}

// BuildScaledObject renders a KEDA ScaledObject for a Deployment.
func BuildScaledObject(req ScalingRequest) (map[string]interface{}, []string, error) {
	var warns []string
	if !validName(req.Namespace) || !validName(req.Target) {
		return nil, nil, fmt.Errorf("%w: namespace and target must be DNS labels", ErrRejected)
	}
	min, max := req.MinReplicas, req.MaxReplicas
	if min < 0 {
		min = 0
	}
	if max <= 0 {
		max = 10
	}
	if max < min {
		return nil, nil, fmt.Errorf("%w: maxReplicas %d is below minReplicas %d", ErrRejected, max, min)
	}
	if max > 100 {
		return nil, nil, fmt.Errorf("%w: maxReplicas %d exceeds 100", ErrRejected, max)
	}
	if min == 0 {
		warns = append(warns, "minReplicaCount=0 scales the deployment to zero when the queue is empty; the first request pays the model load time")
	}
	threshold := strings.TrimSpace(req.Threshold)
	if threshold == "" {
		threshold = "10"
	}
	if _, err := strconv.ParseFloat(threshold, 64); err != nil {
		return nil, nil, fmt.Errorf("%w: threshold %q is not a number", ErrRejected, threshold)
	}
	trigger := strings.ToLower(strings.TrimSpace(req.Trigger))
	if trigger == "" {
		trigger = "prometheus"
	}
	meta := map[string]interface{}{}
	switch trigger {
	case "prometheus":
		query := strings.TrimSpace(req.Query)
		if query == "" {
			query = DefaultQueueQuery(req.Namespace)
			warns = append(warns, "query defaults to the vLLM waiting-request gauge; change it if the server is not vLLM")
		}
		addr := strings.TrimSpace(req.ServerAddress)
		if addr == "" {
			addr = "http://prometheus-server.monitoring.svc:80"
			warns = append(warns, "Prometheus address is a placeholder; set prometheus.url in KoreCube or edit serverAddress")
		}
		meta["serverAddress"], meta["query"], meta["threshold"] = addr, query, threshold
	case "kafka":
		if req.Topic == "" || req.ConsumerGroup == "" || req.Bootstrap == "" {
			return nil, nil, fmt.Errorf("%w: kafka needs topic, consumerGroup and bootstrapServers", ErrRejected)
		}
		meta["bootstrapServers"], meta["consumerGroup"], meta["topic"], meta["lagThreshold"] = req.Bootstrap, req.ConsumerGroup, req.Topic, threshold
	case "rabbitmq":
		if req.Queue == "" {
			return nil, nil, fmt.Errorf("%w: rabbitmq needs queue", ErrRejected)
		}
		meta["queueName"], meta["mode"], meta["value"], meta["hostFromEnv"] = req.Queue, "QueueLength", threshold, "RABBITMQ_HOST"
		warns = append(warns, "rabbitmq reads the connection string from env RABBITMQ_HOST of the target container")
	case "redis":
		if req.Address == "" || req.Queue == "" {
			return nil, nil, fmt.Errorf("%w: redis needs address and queue (list name)", ErrRejected)
		}
		meta["address"], meta["listName"], meta["listLength"] = req.Address, req.Queue, threshold
	default:
		return nil, nil, fmt.Errorf("%w: unsupported trigger %q", ErrRejected, trigger)
	}
	obj := map[string]interface{}{
		"apiVersion": "keda.sh/v1alpha1",
		"kind":       "ScaledObject",
		"metadata": map[string]interface{}{
			"name":      req.Target + "-queue",
			"namespace": req.Namespace,
			"labels":    map[string]interface{}{"app.kubernetes.io/managed-by": managedBy},
		},
		"spec": map[string]interface{}{
			"scaleTargetRef":  map[string]interface{}{"apiVersion": "apps/v1", "kind": "Deployment", "name": req.Target},
			"minReplicaCount": int64(min),
			"maxReplicaCount": int64(max),
			"pollingInterval": int64(30),
			"cooldownPeriod":  int64(300),
			"triggers":        []interface{}{map[string]interface{}{"type": trigger, "metadata": meta}},
		},
	}
	return obj, warns, nil
}

// BuildNodePool renders a Karpenter v1 NodePool for GPU nodes. When existing
// is set, only limits and disruption change so the rest of the pool is kept.
func BuildNodePool(req ScalingRequest, existing map[string]interface{}) (map[string]interface{}, []string, error) {
	var warns []string
	pool := strings.TrimSpace(req.Pool)
	if pool == "" {
		pool = "gpu"
	}
	if !validName(pool) {
		return nil, nil, fmt.Errorf("%w: pool must be a DNS label", ErrRejected)
	}
	limit := req.GPULimit
	if limit <= 0 {
		limit = 8
	}
	if limit > 512 {
		return nil, nil, fmt.Errorf("%w: gpuLimit %d exceeds 512", ErrRejected, limit)
	}
	disruption := map[string]interface{}{
		"consolidationPolicy": "WhenEmpty",
		"consolidateAfter":    "10m",
	}
	if existing != nil {
		obj := deepCopyMap(existing)
		delete(obj, "status")
		if md, ok := obj["metadata"].(map[string]interface{}); ok {
			for _, k := range []string{"managedFields", "creationTimestamp", "generation", "uid"} {
				delete(md, k)
			}
		}
		spec, _ := obj["spec"].(map[string]interface{})
		if spec == nil {
			spec = map[string]interface{}{}
			obj["spec"] = spec
		}
		limits, _ := spec["limits"].(map[string]interface{})
		if limits == nil {
			limits = map[string]interface{}{}
		}
		limits["nvidia.com/gpu"] = strconv.Itoa(limit)
		spec["limits"] = limits
		if _, ok := spec["disruption"]; !ok {
			spec["disruption"] = disruption
		}
		return obj, warns, nil
	}
	reqs := []interface{}{
		map[string]interface{}{"key": "karpenter.sh/capacity-type", "operator": "In", "values": []interface{}{capacity(req.CapacityType)}},
	}
	if len(req.InstanceTypes) > 0 {
		vals := make([]interface{}, 0, len(req.InstanceTypes))
		for _, it := range req.InstanceTypes {
			if it = strings.TrimSpace(it); it != "" {
				vals = append(vals, it)
			}
		}
		reqs = append(reqs, map[string]interface{}{"key": "node.kubernetes.io/instance-type", "operator": "In", "values": vals})
	} else {
		warns = append(warns, "no instance types given; add a requirement that selects GPU instance types for your provider")
	}
	class, kind, group := req.NodeClass, req.NodeClassKind, req.NodeClassGrp
	if class == "" {
		class = "default"
	}
	if kind == "" {
		kind = "EC2NodeClass"
	}
	if group == "" {
		group = "karpenter.k8s.aws"
	}
	if req.NodeClassKind == "" {
		warns = append(warns, "nodeClassRef defaults to the AWS EC2NodeClass named default; change it on other providers")
	}
	obj := map[string]interface{}{
		"apiVersion": "karpenter.sh/v1",
		"kind":       "NodePool",
		"metadata": map[string]interface{}{
			"name":   pool,
			"labels": map[string]interface{}{"app.kubernetes.io/managed-by": managedBy},
		},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{
				"metadata": map[string]interface{}{"labels": map[string]interface{}{"korecube.io/pool": pool}},
				"spec": map[string]interface{}{
					"nodeClassRef": map[string]interface{}{"group": group, "kind": kind, "name": class},
					"requirements": reqs,
					"taints": []interface{}{
						map[string]interface{}{"key": "nvidia.com/gpu", "value": "true", "effect": "NoSchedule"},
					},
				},
			},
			"limits":     map[string]interface{}{"nvidia.com/gpu": strconv.Itoa(limit)},
			"disruption": disruption,
		},
	}
	return obj, warns, nil
}

func capacity(c string) string {
	if strings.EqualFold(strings.TrimSpace(c), "spot") {
		return "spot"
	}
	return "on-demand"
}

func deepCopyMap(in map[string]interface{}) map[string]interface{} {
	out := make(map[string]interface{}, len(in))
	for k, v := range in {
		out[k] = deepCopyValue(v)
	}
	return out
}

func deepCopyValue(v interface{}) interface{} {
	switch t := v.(type) {
	case map[string]interface{}:
		return deepCopyMap(t)
	case []interface{}:
		c := make([]interface{}, len(t))
		for i := range t {
			c[i] = deepCopyValue(t[i])
		}
		return c
	default:
		return v
	}
}

// FormatScalingPlan is what the investigator replies with: the summary, the
// manifest and where to approve it.
func FormatScalingPlan(p ScalingPlan, zh bool) string {
	var b strings.Builder
	if zh {
		b.WriteString(sprintf("已生成伸缩方案 %s（%s %s，%s）。方案不会自动执行：请到「GPU 运维 → 伸缩方案」审批后由控制台下发。\n", p.ID, p.Kind, p.Name, p.Mode))
	} else {
		b.WriteString(sprintf("Scaling proposal %s (%s %s, %s). Nothing was applied: approve it under GPU Ops -> Scaling proposals.\n", p.ID, p.Kind, p.Name, p.Mode))
	}
	b.WriteString(p.Summary + "\n")
	for _, w := range p.Warnings {
		b.WriteString("! " + w + "\n")
	}
	b.WriteString("```yaml\n" + p.YAML + "```")
	return b.String()
}
