package ai

import (
	"context"
	"strings"
	"testing"

	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/pkg/k8s"
)

type fakeGPU struct{}

func (fakeGPU) Health(context.Context, *k8s.Client, bool) (gpuops.HealthReport, error) {
	return gpuops.HealthReport{Incidents: []gpuops.Incident{{Node: "gpu-1", Severity: "critical", Action: "cordon_and_drain", Tier: "L2", Confidence: 0.9}}}, nil
}

func (fakeGPU) Waste(context.Context, *k8s.Client) (gpuops.WasteReport, error) {
	return gpuops.WasteReport{}, nil
}

func (fakeGPU) Storm(context.Context, *k8s.Client) (gpuops.StormReport, error) {
	return gpuops.StormReport{}, nil
}

func (fakeGPU) Placement(_ context.Context, _ *k8s.Client, ns, name string, _ bool) (string, error) {
	return ns + "/" + name + " crosses a PCIe switch", nil
}

func (fakeGPU) Anomalies(context.Context, *k8s.Client) (gpuops.AnomalyReport, error) {
	return gpuops.AnomalyReport{Entities: 3, Trained: 3, Persist: 3, Threshold: 0.7, Persistent: []gpuops.Anomaly{
		{Entity: "pod:llm/a", Kind: "pod", Namespace: "llm", Pod: "a", Streak: 5, Score: 0.83, Latest: map[string]float64{"util": 100}, Baseline: map[string]float64{"util": 60}},
	}}, nil
}

func (fakeGPU) Capacity(_ context.Context, _ *k8s.Client, zh bool) (gpuops.CapacityReport, error) {
	snap := gpuops.ShowcaseSnapshot()
	incidents := []gpuops.Incident{{Node: "gpu-a100-1", Severity: "critical"}}
	return gpuops.BuildCapacity(snap.Pods, snap.GPUOnNode, snap.Product, incidents, zh), nil
}

func (fakeGPU) ProposeScaling(_ context.Context, _ *k8s.Client, req gpuops.ScalingRequest, _ bool) (gpuops.ScalingPlan, error) {
	lastScaling = req
	return gpuops.ScalingPlan{ID: "sp-1", Kind: "ScaledObject", Name: "vllm-queue", Mode: "create", YAML: "kind: ScaledObject\n"}, nil
}

var lastScaling gpuops.ScalingRequest

func TestAnomalyAndScalingTools(t *testing.T) {
	SetGPUReporter(fakeGPU{})
	defer SetGPUReporter(nil)

	res, err := executeTool(context.Background(), nil, "get_gpu_anomalies", nil, "en")
	if err != nil || !strings.Contains(res.Text, "pod:llm/a anomalous for 5m") || len(res.Resources) != 1 {
		t.Fatalf("anomaly tool: %+v %v", res, err)
	}

	res, err = executeTool(context.Background(), nil, "plan_autoscaling", map[string]interface{}{
		"kind": "keda", "namespace": "llm", "max_replicas": float64(8), "instance_types": "p4d.24xlarge, g5.12xlarge",
	}, "zh")
	if err != nil || !strings.Contains(res.Text, "sp-1") || !strings.Contains(res.Text, "伸缩方案") {
		t.Fatalf("plan tool: %+v %v", res, err)
	}
	if res.Resources[0].Href != "/gpu?proposal=sp-1" || lastScaling.MaxReplicas != 8 || len(lastScaling.InstanceTypes) != 2 {
		t.Fatalf("plan args: %+v %+v", res.Resources, lastScaling)
	}
}

func TestCapacityTool(t *testing.T) {
	SetGPUReporter(fakeGPU{})
	defer SetGPUReporter(nil)

	res, err := executeTool(context.Background(), nil, "get_gpu_capacity", nil, "en")
	if err != nil || !strings.Contains(res.Text, "[critical]") || !strings.Contains(res.Text, "Karpenter") {
		t.Fatalf("capacity tool: %+v %v", res, err)
	}
	if len(res.Resources) != 1 || res.Resources[0].Href != "/nodes/gpu-a100-2" {
		t.Fatalf("N-1 node ref: %+v", res.Resources)
	}
}

func TestMockScalingArgs(t *testing.T) {
	raw := "帮我把 llm-inference 命名空间的副本数根据队列长度伸缩一下，最多 12 个"
	args := mockScalingArgs(raw, strings.ToLower(raw), "")
	if args["kind"] != "keda" || args["namespace"] != "llm-inference" || args["max_replicas"] != 12 || args["trigger"] != "prometheus" {
		t.Fatalf("zh keda: %+v", args)
	}
	raw = "Raise the Karpenter nodepool gpu-train limit to 32 GPUs on spot"
	args = mockScalingArgs(raw, strings.ToLower(raw), "")
	if args["kind"] != "karpenter" || args["pool"] != "gpu-train" || args["gpu_limit"] != 32 || args["capacity_type"] != "spot" {
		t.Fatalf("en karpenter: %+v", args)
	}
	raw = "scale deployment vllm on kafka lag"
	args = mockScalingArgs(raw, raw, "serving")
	if args["namespace"] != "serving" || args["target"] != "vllm" || args["trigger"] != "kafka" {
		t.Fatalf("en kafka: %+v", args)
	}
}

func TestGPUToolsUnavailableWithoutReporter(t *testing.T) {
	SetGPUReporter(nil)
	if _, err := executeTool(context.Background(), nil, "get_gpu_health", nil, "en"); err == nil {
		t.Fatal("expected an error without a reporter")
	}
}

func TestGPUToolsUseReporter(t *testing.T) {
	SetGPUReporter(fakeGPU{})
	defer SetGPUReporter(nil)

	res, err := executeTool(context.Background(), nil, "get_gpu_health", nil, "en")
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(res.Text, "gpu-1") || len(res.Resources) != 1 || res.Resources[0].Href != "/nodes/gpu-1" {
		t.Fatalf("health tool: %+v", res)
	}

	res, err = executeTool(context.Background(), nil, "get_gpu_placement", map[string]interface{}{
		"namespace": "llm", "name": "a",
	}, "en")
	if err != nil || !strings.Contains(res.Text, "PCIe") {
		t.Fatalf("placement tool: %+v %v", res, err)
	}
}
