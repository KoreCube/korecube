package ai

import (
	"context"
	"errors"
	"strings"

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
	Anomalies(ctx context.Context, client *k8s.Client) (gpuops.AnomalyReport, error)
	Capacity(ctx context.Context, client *k8s.Client, zh bool) (gpuops.CapacityReport, error)
	ProposeScaling(ctx context.Context, client *k8s.Client, req gpuops.ScalingRequest, zh bool) (gpuops.ScalingPlan, error)
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

func toolGPUAnomalies(ctx context.Context, client *k8s.Client, zh bool) (toolResult, error) {
	r, err := requireGPU(zh)
	if err != nil {
		return toolResult{}, err
	}
	report, err := r.Anomalies(ctx, client)
	if err != nil {
		return toolResult{}, err
	}
	var refs []ResourceRef
	for _, a := range report.Persistent {
		switch a.Kind {
		case "pod":
			refs = append(refs, podRef(a.Namespace, a.Pod, ""))
		case "node":
			refs = append(refs, resourceRef("nodes", "", a.Node, false, ""))
		}
	}
	return toolResult{Text: gpuops.FormatAnomalies(report, zh), Resources: refs}, nil
}

func toolGPUCapacity(ctx context.Context, client *k8s.Client, zh bool) (toolResult, error) {
	r, err := requireGPU(zh)
	if err != nil {
		return toolResult{}, err
	}
	report, err := r.Capacity(ctx, client, zh)
	if err != nil {
		return toolResult{}, err
	}
	var refs []ResourceRef
	for _, risk := range report.Risks {
		if risk.Kind == "no_n1_headroom" && risk.Target != "" {
			refs = append(refs, resourceRef("nodes", "", risk.Target, false, ""))
		}
	}
	return toolResult{Text: gpuops.FormatCapacity(report, zh), Resources: refs}, nil
}

// toolPlanAutoscaling only stores a proposal; applying it is a console action.
func toolPlanAutoscaling(ctx context.Context, client *k8s.Client, args map[string]interface{}, zh bool) (toolResult, error) {
	r, err := requireGPU(zh)
	if err != nil {
		return toolResult{}, err
	}
	req := gpuops.ScalingRequest{
		Kind:          strArg(args, "kind"),
		Namespace:     strArg(args, "namespace"),
		Target:        strArg(args, "target"),
		Trigger:       strArg(args, "trigger"),
		Query:         strArg(args, "query"),
		Threshold:     strArg(args, "threshold"),
		MinReplicas:   intArg(args, "min_replicas", 1),
		MaxReplicas:   intArg(args, "max_replicas", 10),
		Topic:         strArg(args, "topic"),
		ConsumerGroup: strArg(args, "consumer_group"),
		Bootstrap:     strArg(args, "bootstrap_servers"),
		Queue:         strArg(args, "queue"),
		Address:       strArg(args, "address"),
		Pool:          strArg(args, "pool"),
		GPULimit:      intArg(args, "gpu_limit", 0),
		CapacityType:  strArg(args, "capacity_type"),
		NodeClass:     strArg(args, "node_class"),
	}
	for _, it := range strings.Split(strArg(args, "instance_types"), ",") {
		if it = strings.TrimSpace(it); it != "" {
			req.InstanceTypes = append(req.InstanceTypes, it)
		}
	}
	plan, err := r.ProposeScaling(ctx, client, req, zh)
	if err != nil {
		return toolResult{}, err
	}
	label := "Scaling proposal " + plan.ID
	if zh {
		label = "伸缩方案 " + plan.ID
	}
	return toolResult{
		Text:      gpuops.FormatScalingPlan(plan, zh),
		Resources: []ResourceRef{{Kind: "gpu", Name: plan.ID, Href: "/gpu?proposal=" + plan.ID, Label: label}},
	}, nil
}
