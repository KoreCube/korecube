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
