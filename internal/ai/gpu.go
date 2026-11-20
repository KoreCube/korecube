package ai

import (
	"context"
	"errors"

	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/pkg/k8s"
)

// GPUReporter is the GPU ops service seen from the investigator. Reports read
// the same DCGM data and persistence state as the GPU ops page.
type GPUReporter interface {
	Health(ctx context.Context, client *k8s.Client, zh bool) (gpuops.HealthReport, error)
	Waste(ctx context.Context, client *k8s.Client) (gpuops.WasteReport, error)
	Storm(ctx context.Context, client *k8s.Client) (gpuops.StormReport, error)
	Placement(ctx context.Context, client *k8s.Client, namespace, name string, zh bool) (string, error)
}

var gpuReporter GPUReporter

// SetGPUReporter wires the GPU tools. Without it they report as unavailable.
func SetGPUReporter(r GPUReporter) {
	gpuReporter = r
}

func requireGPU(zh bool) (GPUReporter, error) {
	if gpuReporter == nil {
		if zh {
			return nil, errors.New("GPU 运维模块未启用")
		}
		return nil, errors.New("GPU operations are not enabled")
	}
	return gpuReporter, nil
}

func toolGPUHealth(ctx context.Context, client *k8s.Client, zh bool) (toolResult, error) {
	r, err := requireGPU(zh)
	if err != nil {
		return toolResult{}, err
	}
	report, err := r.Health(ctx, client, zh)
	if err != nil {
		return toolResult{}, err
	}
	var refs []ResourceRef
	for _, inc := range report.Incidents {
		refs = append(refs, resourceRef("nodes", "", inc.Node, false, ""))
	}
	return toolResult{Text: gpuops.FormatHealth(report, zh), Resources: refs}, nil
}

func toolGPUWaste(ctx context.Context, client *k8s.Client, zh bool) (toolResult, error) {
	r, err := requireGPU(zh)
	if err != nil {
		return toolResult{}, err
	}
	report, err := r.Waste(ctx, client)
	if err != nil {
		return toolResult{}, err
	}
	return toolResult{Text: gpuops.FormatWaste(report, zh)}, nil
}

func toolGPUPlacement(ctx context.Context, client *k8s.Client, args map[string]interface{}, zh bool) (toolResult, error) {
	r, err := requireGPU(zh)
	if err != nil {
		return toolResult{}, err
	}
	ns, name := strArg(args, "namespace"), strArg(args, "name")
	text, err := r.Placement(ctx, client, ns, name, zh)
	if err != nil {
		return toolResult{}, err
	}
	return toolResult{Text: text, Resources: []ResourceRef{podRef(ns, name, "")}}, nil
}

func toolEventStorm(ctx context.Context, client *k8s.Client, zh bool) (toolResult, error) {
	r, err := requireGPU(zh)
	if err != nil {
		return toolResult{}, err
	}
	report, err := r.Storm(ctx, client)
	if err != nil {
		return toolResult{}, err
	}
	return toolResult{Text: gpuops.FormatStorm(report, zh)}, nil
}
