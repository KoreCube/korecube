package gpuops

// ShowcaseSnapshot is a small AI fleet used when the process is in demo mode,
// so the GPU pages exercise the same grouping code as a live cluster.
func ShowcaseSnapshot() *Snapshot {
	owners := map[string]OwnerLink{
		"rs-1":  {UID: "rs-1", Kind: "ReplicaSet", Name: "llm-inf-7f", Namespace: "llm-inference", Parents: []string{"dep-1"}},
		"dep-1": {UID: "dep-1", Kind: "Deployment", Name: "llm-inf", Namespace: "llm-inference"},
		"job-1": {UID: "job-1", Kind: "Job", Name: "pretrain", Namespace: "training"},
	}
	pods := []PodFact{
		{Namespace: "llm-inference", Name: "llm-inf-7f-a", Node: "gpu-a100-1", Phase: "Running", GPUMilli: 1000, OwnerKind: "ReplicaSet", RootUID: "dep-1", RootKind: "Deployment", RootName: "llm-inf"},
		{Namespace: "llm-inference", Name: "llm-inf-7f-b", Node: "gpu-a100-2", Phase: "Running", GPUMilli: 1000, OwnerKind: "ReplicaSet", RootUID: "dep-1", RootKind: "Deployment", RootName: "llm-inf"},
		{Namespace: "llm-inference", Name: "llm-inf-7f-c", Node: "gpu-a100-2", Phase: "Running", GPUMilli: 2000, OwnerKind: "ReplicaSet", RootUID: "dep-1", RootKind: "Deployment", RootName: "llm-inf"},
		{Namespace: "training", Name: "pretrain-0", Node: "gpu-a100-1", Phase: "Failed", GPUMilli: 8000, OwnerKind: "Job", RootUID: "job-1", RootKind: "Job", RootName: "pretrain"},
		{Namespace: "training", Name: "pretrain-1", Phase: "Pending", GPUMilli: 8000, OwnerKind: "Job", RootUID: "job-1", RootKind: "Job", RootName: "pretrain"},
		{Namespace: "kube-system", Name: "nvidia-device-plugin", Node: "gpu-a100-1", Phase: "Running", OwnerKind: "DaemonSet"},
	}
	events := []StormEvent{
		{Type: "Warning", Reason: "Xid", Message: "NVRM: Xid (PCI:0000:3b:00): 79, GPU has fallen off the bus", Kind: "Node", Name: "gpu-a100-1", UID: "ev-1", Node: "gpu-a100-1"},
		{Type: "Warning", Reason: "NodeNotReady", Message: "node not ready", Kind: "Node", Name: "gpu-a100-1", UID: "ev-2", Node: "gpu-a100-1"},
		{Type: "Warning", Reason: "Evicted", Message: "pod evicted by node pressure", Kind: "Pod", Name: "pretrain-0", Namespace: "training", UID: "pod-0", Node: "gpu-a100-1"},
		{Type: "Warning", Reason: "FailedScheduling", Message: "0/3 nodes available", Kind: "Pod", Name: "pretrain-1", Namespace: "training", UID: "pod-1", Node: "gpu-a100-1"},
		{Type: "Warning", Reason: "Killing", Message: "stopping container", Kind: "Pod", Name: "pretrain-0", Namespace: "training", UID: "pod-0", Node: "gpu-a100-1"},
		{Type: "Warning", Reason: "BackOff", Message: "back-off restarting failed container", Kind: "Pod", Name: "unrelated", Namespace: "default", UID: "pod-u", Node: "cpu-1"},
	}
	signals := []Signal{
		{Node: "gpu-a100-1", GPU: "0", Kind: "xid", Code: "79", Recoverable: false, Source: "dcgm", Message: "GPU has fallen off the bus"},
		{Node: "gpu-a100-2", GPU: "1", Kind: "nvlink", Code: "crc", Recoverable: true, Source: "dcgm", Message: "NVLink CRC errors 3"},
	}
	return &Snapshot{
		Pods: pods, Events: events, Owners: owners, Signals: signals,
		GPUOnNode: map[string]int64{"gpu-a100-1": 8, "gpu-a100-2": 8},
		Product:   map[string]string{"gpu-a100-1": "NVIDIA-A100-SXM4-80GB", "gpu-a100-2": "NVIDIA-A100-SXM4-80GB"},
	}
}

// ShowcaseUtil is the demo utilization used with ShowcaseSnapshot.
func ShowcaseUtil() (byNS map[string]float64, byPod map[string]float64) {
	byNS = map[string]float64{"llm-inference": 18, "training": 91}
	byPod = map[string]float64{
		"llm-inference/llm-inf-7f-c": 11,
		"llm-inference/llm-inf-7f-a": 72,
		"llm-inference/llm-inf-7f-b": 70,
	}
	return byNS, byPod
}
