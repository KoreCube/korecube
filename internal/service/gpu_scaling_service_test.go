package service

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv2 "k8s.io/api/autoscaling/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	fakediscovery "k8s.io/client-go/discovery/fake"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/korecube/korecube/configs"
	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/pkg/k8s"
)

func gpuDeployment(ns, name string, gpus int64) *appsv1.Deployment {
	c := corev1.Container{Name: "main"}
	if gpus > 0 {
		c.Resources.Limits = corev1.ResourceList{"nvidia.com/gpu": *resource.NewQuantity(gpus, resource.DecimalSI)}
	}
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Namespace: ns, Name: name},
		Spec:       appsv1.DeploymentSpec{Template: corev1.PodTemplateSpec{Spec: corev1.PodSpec{Containers: []corev1.Container{c}}}},
	}
}

func liveClient(withKEDA bool, objs ...runtime.Object) *k8s.Client {
	cs := fake.NewSimpleClientset(objs...)
	if withKEDA {
		cs.Discovery().(*fakediscovery.FakeDiscovery).Resources = []*metav1.APIResourceList{
			{GroupVersion: "keda.sh/v1alpha1", APIResources: []metav1.APIResource{{Name: "scaledobjects", Namespaced: true, Kind: "ScaledObject"}}},
			{GroupVersion: "karpenter.sh/v1", APIResources: []metav1.APIResource{{Name: "nodepools", Kind: "NodePool"}}},
		}
	}
	dyn := dynamicfake.NewSimpleDynamicClientWithCustomListKinds(runtime.NewScheme(), map[schema.GroupVersionResource]string{
		scaledObjectGVR: "ScaledObjectList",
		nodePoolGVR:     "NodePoolList",
	})
	return &k8s.Client{Clientset: cs, DynamicClient: dyn, DiscoveryClient: cs.Discovery(), Config: &rest.Config{Host: "https://gpu-live:6443"}}
}

func TestScalingProposalPicksGPUDeploymentAndApplies(t *testing.T) {
	cfg := &configs.Config{}
	cfg.Prometheus.URL = "http://prom:9090"
	svc := NewGPUOpsService(cfg, nil, nil, nil)
	hpa := &autoscalingv2.HorizontalPodAutoscaler{
		ObjectMeta: metav1.ObjectMeta{Namespace: "llm-inference", Name: "old"},
		Spec:       autoscalingv2.HorizontalPodAutoscalerSpec{ScaleTargetRef: autoscalingv2.CrossVersionObjectReference{Kind: "Deployment", Name: "vllm"}},
	}
	client := liveClient(true, gpuDeployment("llm-inference", "vllm", 1), gpuDeployment("llm-inference", "gateway", 0), hpa)

	plan, err := svc.ProposeScaling(context.Background(), client, gpuops.ScalingRequest{Namespace: "llm-inference", MaxReplicas: 6}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Name != "vllm-queue" || plan.Mode != "create" || plan.Status != "pending" {
		t.Fatalf("plan: %+v", plan)
	}
	if !strings.Contains(plan.YAML, "http://prom:9090") || !strings.Contains(plan.YAML, "vllm:num_requests_waiting") {
		t.Fatalf("yaml: %s", plan.YAML)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "HPA old") {
		t.Fatalf("expected HPA conflict warning: %v", plan.Warnings)
	}

	applied, err := svc.DecideScaling(context.Background(), client, "alice", plan.ID, true)
	if err != nil || applied.Status != "applied" || applied.DecidedBy != "alice" {
		t.Fatalf("apply: %+v %v", applied, err)
	}
	got, err := client.DynamicClient.Resource(scaledObjectGVR).Namespace("llm-inference").Get(context.Background(), "vllm-queue", metav1.GetOptions{})
	if err != nil {
		t.Fatal(err)
	}
	if max, _, _ := unstructured.NestedInt64(got.Object, "spec", "maxReplicaCount"); max != 6 {
		t.Fatalf("maxReplicaCount=%d", max)
	}
	if _, err := svc.DecideScaling(context.Background(), client, "alice", plan.ID, true); !errors.Is(err, gpuops.ErrRejected) {
		t.Fatalf("second decision should be rejected, got %v", err)
	}
	if d := svc.Decisions(); len(d) != 1 || d[0].Tier != "L2" || d[0].Outcome != "executed" {
		t.Fatalf("trail: %+v", d)
	}
}

func TestScalingProposalNeedsTargetWhenAmbiguous(t *testing.T) {
	svc := NewGPUOpsService(nil, nil, nil, nil)
	client := liveClient(true, gpuDeployment("ml", "a", 1), gpuDeployment("ml", "b", 1))
	_, err := svc.ProposeScaling(context.Background(), client, gpuops.ScalingRequest{Namespace: "ml"}, false)
	if !errors.Is(err, gpuops.ErrRejected) || !strings.Contains(err.Error(), "a, b") {
		t.Fatalf("expected ambiguity error, got %v", err)
	}
}

func TestScalingApplyRefusedWithoutKEDA(t *testing.T) {
	svc := NewGPUOpsService(nil, nil, nil, nil)
	client := liveClient(false, gpuDeployment("ml", "a", 1))
	plan, err := svc.ProposeScaling(context.Background(), client, gpuops.ScalingRequest{Namespace: "ml"}, false)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(strings.Join(plan.Warnings, "\n"), "KEDA is not installed") {
		t.Fatalf("warnings: %v", plan.Warnings)
	}
	got, err := svc.DecideScaling(context.Background(), client, "bob", plan.ID, true)
	if !errors.Is(err, gpuops.ErrRejected) || got.Status != "pending" {
		t.Fatalf("expected refusal that keeps the plan pending, got %+v %v", got, err)
	}
}

func TestNodePoolUpdateKeepsExistingSpec(t *testing.T) {
	svc := NewGPUOpsService(nil, nil, nil, nil)
	client := liveClient(true)
	existing := map[string]interface{}{
		"apiVersion": "karpenter.sh/v1", "kind": "NodePool",
		"metadata": map[string]interface{}{"name": "gpu"},
		"spec": map[string]interface{}{
			"template": map[string]interface{}{"spec": map[string]interface{}{"nodeClassRef": map[string]interface{}{"name": "custom"}}},
			"limits":   map[string]interface{}{"cpu": "1000", "nvidia.com/gpu": "8"},
		},
	}
	if _, err := client.DynamicClient.Resource(nodePoolGVR).Create(context.Background(), &unstructured.Unstructured{Object: existing}, metav1.CreateOptions{}); err != nil {
		t.Fatal(err)
	}
	plan, err := svc.ProposeScaling(context.Background(), client, gpuops.ScalingRequest{Kind: "karpenter", GPULimit: 32}, false)
	if err != nil {
		t.Fatal(err)
	}
	if plan.Mode != "update" || !strings.Contains(plan.YAML, "custom") || !strings.Contains(plan.YAML, `nvidia.com/gpu: "32"`) || !strings.Contains(plan.YAML, `cpu: "1000"`) {
		t.Fatalf("plan: %+v\n%s", plan, plan.YAML)
	}
	if _, err := svc.DecideScaling(context.Background(), client, "alice", plan.ID, true); err != nil {
		t.Fatal(err)
	}
}

func TestScalingShowcaseProposesButNeverApplies(t *testing.T) {
	svc := NewGPUOpsService(nil, nil, nil, nil)
	client := showcaseClient()
	plan, err := svc.ProposeScaling(context.Background(), client, gpuops.ScalingRequest{Namespace: "llm-inference"}, true)
	if err != nil {
		t.Fatal(err)
	}
	if !strings.Contains(plan.YAML, "name: llm-inf") {
		t.Fatalf("showcase target: %s", plan.YAML)
	}
	if _, err := svc.DecideScaling(context.Background(), client, "demo", plan.ID, true); !errors.Is(err, gpuops.ErrForbidden) {
		t.Fatalf("expected forbidden, got %v", err)
	}
	rejected, err := svc.DecideScaling(context.Background(), client, "demo", plan.ID, false)
	if err != nil || rejected.Status != "rejected" {
		t.Fatalf("reject: %+v %v", rejected, err)
	}
	if other := svc.ScalingPlans(liveClient(false)); len(other) != 0 {
		t.Fatalf("plans leaked to another cluster: %+v", other)
	}
}

func TestShowcaseAnomaliesOnRequest(t *testing.T) {
	svc := NewGPUOpsService(nil, nil, nil, nil)
	rep, err := svc.Anomalies(context.Background(), showcaseClient())
	if err != nil {
		t.Fatal(err)
	}
	if rep.Source != "showcase" || len(rep.Persistent) == 0 || rep.At.After(time.Now()) {
		t.Fatalf("report: %+v", rep)
	}
	live, _ := svc.Anomalies(context.Background(), liveClient(false))
	if live.Unavailable == "" {
		t.Fatalf("live without Prometheus should be unavailable: %+v", live)
	}
}
