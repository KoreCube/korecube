package service

import (
	"context"
	"errors"
	"fmt"
	"sort"
	"strings"
	"time"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	"sigs.k8s.io/yaml"

	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/pkg/k8s"
)

const maxScalingPlans = 50

var (
	scaledObjectGVR = schema.GroupVersionResource{Group: "keda.sh", Version: "v1alpha1", Resource: "scaledobjects"}
	nodePoolGVR     = schema.GroupVersionResource{Group: "karpenter.sh", Version: "v1", Resource: "nodepools"}
)

func hasAPIResource(client *k8s.Client, gvr schema.GroupVersionResource) bool {
	if client == nil || client.DiscoveryClient == nil {
		return false
	}
	list, err := client.DiscoveryClient.ServerResourcesForGroupVersion(gvr.GroupVersion().String())
	if err != nil || list == nil {
		return false
	}
	for _, r := range list.APIResources {
		if r.Name == gvr.Resource {
			return true
		}
	}
	return false
}

func clusterKey(client *k8s.Client) string {
	if client == nil || client.Config == nil {
		return ""
	}
	return client.Config.Host
}

func isShowcaseClient(client *k8s.Client) bool {
	return client != nil && client.Config != nil && k8s.IsShowcaseConfig(client.Config)
}

// ProposeScaling turns a chat intent into a KEDA ScaledObject or Karpenter
// NodePool proposal. Nothing is applied until DecideScaling approves it.
func (s *GPUOpsService) ProposeScaling(ctx context.Context, client *k8s.Client, req gpuops.ScalingRequest, zh bool) (gpuops.ScalingPlan, error) {
	if client == nil {
		return gpuops.ScalingPlan{}, fmt.Errorf("cluster client is nil")
	}
	kind := strings.ToLower(strings.TrimSpace(req.Kind))
	var plan gpuops.ScalingPlan
	var err error
	switch kind {
	case "", "keda", "scaledobject":
		plan, err = s.planScaledObject(ctx, client, req)
	case "karpenter", "nodepool":
		plan, err = s.planNodePool(ctx, client, req)
	default:
		return gpuops.ScalingPlan{}, fmt.Errorf("%w: kind must be keda or karpenter", gpuops.ErrRejected)
	}
	if err != nil {
		return gpuops.ScalingPlan{}, err
	}
	if isShowcaseClient(client) {
		plan.Warnings = append(plan.Warnings, "showcase mode is read-only: the proposal can be reviewed but not applied")
	}
	b, err := yaml.Marshal(plan.Object)
	if err != nil {
		return gpuops.ScalingPlan{}, err
	}
	plan.YAML = string(b)
	plan.Status = "pending"
	plan.CreatedAt = time.Now()
	plan.Cluster = clusterKey(client)

	s.mu.Lock()
	s.planSeq++
	plan.ID = fmt.Sprintf("sp-%d", s.planSeq)
	s.plans = append(s.plans, plan)
	if len(s.plans) > maxScalingPlans {
		s.plans = s.plans[len(s.plans)-maxScalingPlans:]
	}
	s.mu.Unlock()
	return plan, nil
}

func (s *GPUOpsService) planScaledObject(ctx context.Context, client *k8s.Client, req gpuops.ScalingRequest) (gpuops.ScalingPlan, error) {
	ns := strings.TrimSpace(req.Namespace)
	if ns == "" {
		return gpuops.ScalingPlan{}, fmt.Errorf("%w: namespace is required", gpuops.ErrRejected)
	}
	target, err := s.resolveTarget(ctx, client, ns, strings.TrimSpace(req.Target))
	if err != nil {
		return gpuops.ScalingPlan{}, err
	}
	req.Namespace, req.Target = ns, target
	if req.ServerAddress == "" && s.cfg != nil && s.cfg.Prometheus.URL != "" {
		req.ServerAddress = s.cfg.Prometheus.URL
	}
	obj, warns, err := gpuops.BuildScaledObject(req)
	if err != nil {
		return gpuops.ScalingPlan{}, err
	}
	name := target + "-queue"
	plan := gpuops.ScalingPlan{
		Kind: "ScaledObject", APIVersion: "keda.sh/v1alpha1", Resource: "scaledobjects",
		Namespace: ns, Name: name, Mode: "create", Object: obj, Warnings: warns,
	}
	spec := obj["spec"].(map[string]interface{})
	trigger := spec["triggers"].([]interface{})[0].(map[string]interface{})
	plan.Summary = fmt.Sprintf("Deployment %s/%s: %d-%d replicas on %s trigger",
		ns, target, spec["minReplicaCount"], spec["maxReplicaCount"], trigger["type"])

	if !isShowcaseClient(client) {
		if !hasAPIResource(client, scaledObjectGVR) {
			plan.Warnings = append(plan.Warnings, "KEDA is not installed (no keda.sh/v1alpha1 scaledobjects); install KEDA before approving")
		} else if _, err := client.DynamicClient.Resource(scaledObjectGVR).Namespace(ns).Get(ctx, name, metav1.GetOptions{}); err == nil {
			plan.Mode = "update"
		}
		hpas, err := client.Clientset.AutoscalingV2().HorizontalPodAutoscalers(ns).List(ctx, metav1.ListOptions{})
		if err == nil {
			for _, h := range hpas.Items {
				if h.Spec.ScaleTargetRef.Kind == "Deployment" && h.Spec.ScaleTargetRef.Name == target && !strings.HasPrefix(h.Name, "keda-hpa-") {
					plan.Warnings = append(plan.Warnings, fmt.Sprintf("HPA %s already scales %s; delete it before approving or KEDA and the HPA will fight", h.Name, target))
				}
			}
		}
	}
	return plan, nil
}

// resolveTarget picks the Deployment to scale. Without a name it takes the
// only Deployment in the namespace, or the only one requesting GPUs.
func (s *GPUOpsService) resolveTarget(ctx context.Context, client *k8s.Client, ns, target string) (string, error) {
	var all, gpu []string
	if isShowcaseClient(client) {
		seen := map[string]bool{}
		for _, p := range gpuops.ShowcaseSnapshot().Pods {
			if p.Namespace == ns && p.RootKind == "Deployment" && !seen[p.RootName] {
				seen[p.RootName] = true
				all = append(all, p.RootName)
				gpu = append(gpu, p.RootName)
			}
		}
	} else {
		list, err := client.Clientset.AppsV1().Deployments(ns).List(ctx, metav1.ListOptions{})
		if err != nil {
			return "", err
		}
		for _, d := range list.Items {
			all = append(all, d.Name)
			for _, c := range d.Spec.Template.Spec.Containers {
				if q, ok := c.Resources.Limits["nvidia.com/gpu"]; ok && !q.IsZero() {
					gpu = append(gpu, d.Name)
					break
				}
			}
		}
	}
	sort.Strings(all)
	if target != "" {
		for _, n := range all {
			if n == target {
				return target, nil
			}
		}
		return "", fmt.Errorf("%w: deployment %s/%s not found", gpuops.ErrRejected, ns, target)
	}
	switch {
	case len(all) == 0:
		return "", fmt.Errorf("%w: no deployments in namespace %s", gpuops.ErrRejected, ns)
	case len(all) == 1:
		return all[0], nil
	case len(gpu) == 1:
		return gpu[0], nil
	}
	return "", fmt.Errorf("%w: several deployments in %s (%s); name the target", gpuops.ErrRejected, ns, strings.Join(all, ", "))
}

func (s *GPUOpsService) planNodePool(ctx context.Context, client *k8s.Client, req gpuops.ScalingRequest) (gpuops.ScalingPlan, error) {
	pool := strings.TrimSpace(req.Pool)
	if pool == "" {
		pool = "gpu"
	}
	req.Pool = pool
	var existing map[string]interface{}
	installed := false
	if !isShowcaseClient(client) {
		installed = hasAPIResource(client, nodePoolGVR)
		if installed {
			if cur, err := client.DynamicClient.Resource(nodePoolGVR).Get(ctx, pool, metav1.GetOptions{}); err == nil {
				existing = cur.Object
			}
		}
	}
	obj, warns, err := gpuops.BuildNodePool(req, existing)
	if err != nil {
		return gpuops.ScalingPlan{}, err
	}
	plan := gpuops.ScalingPlan{
		Kind: "NodePool", APIVersion: "karpenter.sh/v1", Resource: "nodepools",
		Name: pool, Mode: "create", Object: obj, Warnings: warns,
	}
	if existing != nil {
		plan.Mode = "update"
	}
	limits := obj["spec"].(map[string]interface{})["limits"].(map[string]interface{})
	plan.Summary = fmt.Sprintf("NodePool %s: nvidia.com/gpu limit %v", pool, limits["nvidia.com/gpu"])
	if !isShowcaseClient(client) && !installed {
		plan.Warnings = append(plan.Warnings, "Karpenter v1 is not installed (no karpenter.sh/v1 nodepools); install Karpenter >= 1.0 before approving")
	}
	return plan, nil
}

// ScalingPlans lists proposals for the cluster, newest first.
func (s *GPUOpsService) ScalingPlans(client *k8s.Client) []gpuops.ScalingPlan {
	key := clusterKey(client)
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]gpuops.ScalingPlan, 0, len(s.plans))
	for i := len(s.plans) - 1; i >= 0; i-- {
		if s.plans[i].Cluster == key {
			out = append(out, s.plans[i])
		}
	}
	return out
}

// DecideScaling applies or rejects a pending proposal. Approval is always a
// human action in the console, so scaling never runs at L3.
func (s *GPUOpsService) DecideScaling(ctx context.Context, client *k8s.Client, user, id string, approve bool) (gpuops.ScalingPlan, error) {
	s.mu.Lock()
	idx := -1
	for i := range s.plans {
		if s.plans[i].ID == id {
			idx = i
			break
		}
	}
	if idx < 0 {
		s.mu.Unlock()
		return gpuops.ScalingPlan{}, fmt.Errorf("%w: proposal %s not found", gpuops.ErrRejected, id)
	}
	plan := s.plans[idx]
	s.mu.Unlock()

	if plan.Status != "pending" {
		return plan, fmt.Errorf("%w: proposal %s is already %s", gpuops.ErrRejected, id, plan.Status)
	}
	if plan.Cluster != clusterKey(client) {
		return plan, fmt.Errorf("%w: proposal %s belongs to another cluster", gpuops.ErrRejected, id)
	}
	dec := GPUDecision{At: time.Now(), User: user, Node: scalingTarget(plan), Action: "scale:" + strings.ToLower(plan.Kind), Tier: "L2"}

	var applyErr error
	switch {
	case !approve:
		plan.Status = "rejected"
		dec.Outcome = "refused"
		dec.Detail = "rejected by reviewer"
	case isShowcaseClient(client):
		applyErr = fmt.Errorf("%w: showcase mode is read-only", gpuops.ErrForbidden)
	default:
		applyErr = s.applyPlan(ctx, client, plan)
	}
	if approve {
		if applyErr != nil {
			dec.Outcome = "failed"
			dec.Detail = applyErr.Error()
			plan.Detail = applyErr.Error()
			if errors.Is(applyErr, gpuops.ErrForbidden) || errors.Is(applyErr, gpuops.ErrRejected) {
				dec.Outcome = "refused"
			} else {
				plan.Status = "failed"
			}
		} else {
			plan.Status = "applied"
			dec.Outcome = "executed"
			dec.Detail = fmt.Sprintf("%s %s %s", plan.Mode, plan.Kind, plan.Name)
		}
	}
	if plan.Status != "pending" {
		now := time.Now()
		plan.DecidedAt, plan.DecidedBy = &now, user
	}
	s.mu.Lock()
	for i := range s.plans {
		if s.plans[i].ID == id {
			s.plans[i] = plan
		}
	}
	s.mu.Unlock()
	s.record(dec)
	s.auditDecision(dec)
	return plan, applyErr
}

func scalingTarget(p gpuops.ScalingPlan) string {
	if p.Namespace == "" {
		return p.Name
	}
	return p.Namespace + "/" + p.Name
}

func (s *GPUOpsService) applyPlan(ctx context.Context, client *k8s.Client, plan gpuops.ScalingPlan) error {
	gvr := scaledObjectGVR
	if plan.Kind == "NodePool" {
		gvr = nodePoolGVR
	}
	if !hasAPIResource(client, gvr) {
		return fmt.Errorf("%w: %s is not served by this cluster", gpuops.ErrRejected, gvr.GroupVersion().String()+"/"+gvr.Resource)
	}
	var ri dynamic.ResourceInterface = client.DynamicClient.Resource(gvr)
	if plan.Namespace != "" {
		ri = client.DynamicClient.Resource(gvr).Namespace(plan.Namespace)
	}
	obj := &unstructured.Unstructured{Object: plan.Object}
	cur, err := ri.Get(ctx, plan.Name, metav1.GetOptions{})
	switch {
	case apierrors.IsNotFound(err):
		_, err = ri.Create(ctx, obj, metav1.CreateOptions{})
		return err
	case err != nil:
		return err
	}
	obj.SetResourceVersion(cur.GetResourceVersion())
	_, err = ri.Update(ctx, obj, metav1.UpdateOptions{})
	return err
}
