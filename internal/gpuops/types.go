package gpuops

import "time"

// Policy is the tiered self-heal boundary.
// Level: suggest (L1 runbook only), approve (L2 confirm then execute), auto (L3
// may execute without a second confirm, and only inside AutoNamespaces).
type Policy struct {
	Level          string
	AutoNamespaces map[string]bool
	Persistence    time.Duration
	WasteThreshold float64
}

func (p Policy) wasteThreshold() float64 {
	if p.WasteThreshold <= 0 {
		return 40
	}
	return p.WasteThreshold
}

func (p Policy) level() string {
	switch p.Level {
	case "suggest", "approve", "auto":
		return p.Level
	default:
		return "approve"
	}
}

// Signal is one GPU health observation from events, node conditions, or DCGM.
type Signal struct {
	Node        string `json:"node"`
	GPU         string `json:"gpu,omitempty"`
	Kind        string `json:"kind"` // xid, ecc, nvlink, condition
	Code        string `json:"code,omitempty"`
	Recoverable bool   `json:"recoverable"`
	Message     string `json:"message"`
	Source      string `json:"source"`
}

// Incident is one node-level fault after persistence filtering.
type Incident struct {
	Node               string   `json:"node"`
	Severity           string   `json:"severity"` // warning, critical
	Action             string   `json:"action"`   // observe, cordon, cordon_and_drain
	Tier               string   `json:"tier"`     // L1, L2, L3
	AutoEligible       bool     `json:"autoEligible"`
	Executable         bool     `json:"executable"`
	NeedsConfirm       bool     `json:"needsConfirm"`
	Confidence         float64  `json:"confidence"`
	Summary            string   `json:"summary"`
	Runbook            string   `json:"runbook"`
	Signals            []Signal `json:"signals"`
	AffectedNamespaces []string `json:"affectedNamespaces"`
}

// HealthReport is the GPU health payload.
type HealthReport struct {
	Source      string     `json:"source"`
	Policy      string     `json:"policy"`
	ReadOnly    bool       `json:"readOnly"`
	SignalCount int        `json:"signalCount"`
	Incidents   []Incident `json:"incidents"`
}

// PodFact is the slice of a pod the GPU reports need.
type PodFact struct {
	Namespace string
	Name      string
	Node      string
	Phase     string
	GPUMilli  int64
	MIG       bool
	OwnerKind string
	RootUID   string
	RootKind  string
	RootName  string
	RootSpace string
}

// NamespaceUse is allocated GPU vs observed utilization for one namespace.
type NamespaceUse struct {
	Namespace     string  `json:"namespace"`
	AllocatedGPU  float64 `json:"allocatedGpu"`
	ExclusivePods int     `json:"exclusivePods"`
	UtilPercent   float64 `json:"utilPercent"`
	UtilKnown     bool    `json:"utilKnown"`
	Waste         bool    `json:"waste"`
	Suggestion    string  `json:"suggestion"` // none, mig_or_timeslice, need_dcgm
}

// WasteReport compares GPU requests with DCGM utilization.
type WasteReport struct {
	Source     string         `json:"source"`
	Threshold  float64        `json:"threshold"`
	Namespaces []NamespaceUse `json:"namespaces"`
}

// StormEvent is one warning fed into topology aggregation.
type StormEvent struct {
	Type      string
	Reason    string
	Message   string
	Kind      string
	Name      string
	Namespace string
	UID       string
	Node      string
}

// OwnerLink is one object in the owner chain. Parents are controller UIDs.
type OwnerLink struct {
	UID       string
	Kind      string
	Name      string
	Namespace string
	Parents   []string
}

// StormGroup is one actionable incident collapsed from many alerts.
type StormGroup struct {
	Key     string   `json:"key"`
	Kind    string   `json:"kind"`
	Name    string   `json:"name"`
	Node    string   `json:"node,omitempty"`
	Count   int      `json:"count"`
	Reasons []string `json:"reasons"`
	Sample  string   `json:"sample"`
}

// StormReport is the aggregated alert view.
type StormReport struct {
	Source     string       `json:"source"`
	Events     int          `json:"events"`
	Groups     int          `json:"groups"`
	Suppressed float64      `json:"suppressed"`
	Items      []StormGroup `json:"items"`
}

// PromSample is one instant-query vector element.
type PromSample struct {
	Labels map[string]string
	Value  float64
}
