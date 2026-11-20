package gpuops

import (
	"context"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

const gpuResource = corev1.ResourceName("nvidia.com/gpu")

// Snapshot is the cluster slice GPU reports are built from.
type Snapshot struct {
	Pods      []PodFact
	Events    []StormEvent
	Owners    map[string]OwnerLink
	Signals   []Signal
	GPUOnNode map[string]int64
	Product   map[string]string
}

// Collect reads nodes, pods, owner chains, and GPU-related events.
func Collect(ctx context.Context, cs kubernetes.Interface) (*Snapshot, error) {
	snap := &Snapshot{
		Owners:    map[string]OwnerLink{},
		GPUOnNode: map[string]int64{},
		Product:   map[string]string{},
	}
	nodes, err := cs.CoreV1().Nodes().List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, n := range nodes.Items {
		if q, ok := n.Status.Allocatable[gpuResource]; ok {
			snap.GPUOnNode[n.Name] = q.Value()
		}
		if p := n.Labels["nvidia.com/gpu.product"]; p != "" {
			snap.Product[n.Name] = p
		}
		snap.Signals = append(snap.Signals, conditionSignals(n)...)
	}
	if err := collectOwners(ctx, cs, snap); err != nil {
		return nil, err
	}
	pods, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	podNode := map[string]string{}
	for _, p := range pods.Items {
		fact := podFact(p, snap.Owners)
		snap.Pods = append(snap.Pods, fact)
		podNode[p.Namespace+"/"+p.Name] = p.Spec.NodeName
	}
	events, err := cs.CoreV1().Events("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}
	for _, ev := range events.Items {
		if ev.Type != corev1.EventTypeWarning && !isGPUText(ev.Reason+" "+ev.Message) {
			continue
		}
		node := ""
		if ev.InvolvedObject.Kind == "Node" {
			node = ev.InvolvedObject.Name
		} else if ev.InvolvedObject.Kind == "Pod" {
			node = podNode[ev.InvolvedObject.Namespace+"/"+ev.InvolvedObject.Name]
		}
		snap.Events = append(snap.Events, StormEvent{
			Type: ev.Type, Reason: ev.Reason, Message: ev.Message,
			Kind: ev.InvolvedObject.Kind, Name: ev.InvolvedObject.Name,
			Namespace: ev.InvolvedObject.Namespace, UID: string(ev.InvolvedObject.UID),
			Node: node,
		})
		if sig, ok := signalFromEvent(ev, node); ok {
			snap.Signals = append(snap.Signals, sig)
		}
	}
	return snap, nil
}

func collectOwners(ctx context.Context, cs kubernetes.Interface, snap *Snapshot) error {
	rs, err := cs.AppsV1().ReplicaSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, item := range rs.Items {
		addOwner(snap.Owners, string(item.UID), "ReplicaSet", item.Namespace, item.Name, item.OwnerReferences)
	}
	deps, err := cs.AppsV1().Deployments("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, item := range deps.Items {
		addOwner(snap.Owners, string(item.UID), "Deployment", item.Namespace, item.Name, item.OwnerReferences)
	}
	jobs, err := cs.BatchV1().Jobs("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, item := range jobs.Items {
		addOwner(snap.Owners, string(item.UID), "Job", item.Namespace, item.Name, item.OwnerReferences)
	}
	sts, err := cs.AppsV1().StatefulSets("").List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}
	for _, item := range sts.Items {
		addOwner(snap.Owners, string(item.UID), "StatefulSet", item.Namespace, item.Name, item.OwnerReferences)
	}
	return nil
}

func addOwner(m map[string]OwnerLink, uid, kind, ns, name string, refs []metav1.OwnerReference) {
	link := m[uid]
	link.UID, link.Kind, link.Name, link.Namespace = uid, kind, name, ns
	for _, ref := range refs {
		if ref.Controller != nil && !*ref.Controller {
			continue
		}
		parent := string(ref.UID)
		link.Parents = []string{parent}
		if _, ok := m[parent]; !ok {
			m[parent] = OwnerLink{UID: parent, Kind: ref.Kind, Name: ref.Name, Namespace: ns}
		}
		break
	}
	m[uid] = link
}

func podFact(p corev1.Pod, owners map[string]OwnerLink) PodFact {
	fact := PodFact{
		Namespace: p.Namespace, Name: p.Name, Node: p.Spec.NodeName, Phase: string(p.Status.Phase),
	}
	for _, c := range p.Spec.Containers {
		for res, q := range c.Resources.Requests {
			name := string(res)
			if strings.Contains(name, "nvidia.com/mig-") {
				fact.MIG = true
				continue
			}
			if res == gpuResource {
				fact.GPUMilli += q.MilliValue()
			}
		}
	}
	if ref := metav1.GetControllerOf(&p); ref != nil {
		fact.OwnerKind = ref.Kind
		root := rootOf(string(ref.UID), owners)
		if root.UID == "" {
			fact.RootUID = string(ref.UID)
			fact.RootKind = ref.Kind
			fact.RootName = ref.Name
			fact.RootSpace = p.Namespace
		} else {
			fact.RootUID = root.UID
			fact.RootKind = root.Kind
			fact.RootName = root.Name
			fact.RootSpace = root.Namespace
		}
	}
	return fact
}

func conditionSignals(n corev1.Node) []Signal {
	var out []Signal
	for _, c := range n.Status.Conditions {
		kind := strings.ToLower(string(c.Type))
		if !strings.Contains(kind, "gpu") && !strings.Contains(kind, "xid") && !strings.Contains(kind, "ecc") && !strings.Contains(kind, "nvlink") {
			continue
		}
		unhealthy := c.Status == corev1.ConditionTrue
		if strings.HasSuffix(kind, "ready") {
			unhealthy = c.Status != corev1.ConditionTrue
		}
		if !unhealthy {
			continue
		}
		out = append(out, Signal{
			Node: n.Name, Kind: "condition", Code: string(c.Type),
			Recoverable: true, Source: "condition",
			Message: string(c.Type) + ": " + c.Message,
		})
	}
	return out
}

func signalFromEvent(ev corev1.Event, node string) (Signal, bool) {
	if node == "" {
		return Signal{}, false
	}
	text := ev.Reason + " " + ev.Message
	if code := ParseXID(text); code > 0 {
		fatal, reason := ClassifyXID(code)
		return Signal{
			Node: node, Kind: "xid", Code: sprintf("%d", code), Recoverable: !fatal,
			Source: "event", Message: reason,
		}, true
	}
	low := strings.ToLower(text)
	switch {
	case strings.Contains(low, "ecc"):
		return Signal{Node: node, Kind: "ecc", Recoverable: false, Source: "event", Message: "ECC error: " + ev.Message}, true
	case strings.Contains(low, "nvlink"):
		return Signal{Node: node, Kind: "nvlink", Recoverable: true, Source: "event", Message: "NVLink: " + ev.Message}, true
	default:
		return Signal{}, false
	}
}
