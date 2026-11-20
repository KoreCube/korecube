package service

import (
	"context"
	"fmt"
	"strings"
	"sync"
	"time"

	"github.com/korecube/korecube/configs"
	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/pkg/k8s"
)

const (
	dcgmXIDQuery    = "DCGM_FI_DEV_XID_ERRORS > 0"
	dcgmECCQuery    = "DCGM_FI_DEV_ECC_DBE_VOL_TOTAL > 0"
	dcgmNVLinkQuery = "DCGM_FI_DEV_NVLINK_CRC_FLIT_ERROR_COUNT_TOTAL > 0"
	dcgmUtilQuery   = "avg by (namespace, pod) (DCGM_FI_DEV_GPU_UTIL)"
)

// GPUDecision is one self-heal attempt, including refusals, so the trail can be reviewed.
type GPUDecision struct {
	At      time.Time `json:"at"`
	User    string    `json:"user"`
	Node    string    `json:"node"`
	Action  string    `json:"action"`
	Tier    string    `json:"tier"`
	Outcome string    `json:"outcome"`
	Detail  string    `json:"detail"`
}

// GPUOpsService builds GPU health, waste, and alert-storm reports and
// executes cordon/drain only inside the configured heal tier.
type GPUOpsService struct {
	cfg   *configs.Config
	prom  *PrometheusService
	nodes *NodeOpsService
	audit *AuditService

	mu        sync.Mutex
	seen      map[string]time.Time
	decisions []GPUDecision
	anomaly   *gpuops.AnomalyReport
	plans     []gpuops.ScalingPlan
	planSeq   int
}

func NewGPUOpsService(cfg *configs.Config, prom *PrometheusService, nodes *NodeOpsService, audit *AuditService) *GPUOpsService {
	if nodes == nil {
		nodes = NewNodeOpsService()
	}
	return &GPUOpsService{
		cfg: cfg, prom: prom, nodes: nodes, audit: audit,
		seen: map[string]time.Time{},
	}
}

func (s *GPUOpsService) policy() gpuops.Policy {
	p := gpuops.Policy{Level: "approve", WasteThreshold: 40, Persistence: 2 * time.Minute}
	if s.cfg == nil {
		return p
	}
	if s.cfg.GPU.HealLevel != "" {
		p.Level = s.cfg.GPU.HealLevel
	}
	if s.cfg.GPU.WasteUtilPercent > 0 {
		p.WasteThreshold = s.cfg.GPU.WasteUtilPercent
	}
	if s.cfg.GPU.Persistence > 0 {
		p.Persistence = s.cfg.GPU.Persistence
	}
	if len(s.cfg.GPU.AutoNamespaces) > 0 {
		p.AutoNamespaces = map[string]bool{}
		for _, ns := range s.cfg.GPU.AutoNamespaces {
			ns = strings.TrimSpace(ns)
			if ns != "" {
				p.AutoNamespaces[ns] = true
			}
		}
	}
	return p
}

type gpuView struct {
	snap    *gpuops.Snapshot
	policy  gpuops.Policy
	source  string
	utilNS  map[string]float64
	utilPod map[string]float64
}

func (s *GPUOpsService) view(ctx context.Context, client *k8s.Client) (*gpuView, error) {
	if client == nil {
		return nil, fmt.Errorf("cluster client is nil")
	}
	policy := s.policy()
	if client.Config != nil && k8s.IsShowcaseConfig(client.Config) {
		snap := gpuops.ShowcaseSnapshot()
		utilNS, utilPod := gpuops.ShowcaseUtil()
		policy.Persistence = 0
		return &gpuView{snap: snap, policy: policy, source: "showcase", utilNS: utilNS, utilPod: utilPod}, nil
	}
	snap, err := gpuops.Collect(ctx, client.Clientset)
	if err != nil {
		return nil, err
	}
	if s.prom != nil && s.prom.Enabled() {
		snap.Signals = append(snap.Signals, gpuops.SignalsFromDCGM("xid", s.query(ctx, dcgmXIDQuery))...)
		snap.Signals = append(snap.Signals, gpuops.SignalsFromDCGM("ecc", s.query(ctx, dcgmECCQuery))...)
		snap.Signals = append(snap.Signals, gpuops.SignalsFromDCGM("nvlink", s.query(ctx, dcgmNVLinkQuery))...)
		samples := s.query(ctx, dcgmUtilQuery)
		return &gpuView{
			snap: snap, policy: policy, source: "live",
			utilNS: gpuops.UtilByNamespace(samples), utilPod: gpuops.UtilByPod(samples),
		}, nil
	}
	return &gpuView{snap: snap, policy: policy, source: "live"}, nil
}

func (s *GPUOpsService) query(ctx context.Context, q string) []gpuops.PromSample {
	res, err := s.prom.Query(ctx, q, nil)
	if err != nil || res == nil {
		return nil
	}
	samples, err := gpuops.ParsePromVector(res.Data)
	if err != nil {
		return nil
	}
	return samples
}

func (s *GPUOpsService) Health(ctx context.Context, client *k8s.Client, zh bool) (gpuops.HealthReport, error) {
	v, err := s.view(ctx, client)
	if err != nil {
		return gpuops.HealthReport{}, err
	}
	s.mu.Lock()
	report := gpuops.BuildHealth(v.snap.Signals, v.snap.Pods, v.policy, time.Now(), s.seen, zh)
	s.mu.Unlock()
	report.Source = v.source
	report.ReadOnly = v.source == "showcase"
	if report.ReadOnly {
		for i := range report.Incidents {
			report.Incidents[i].Executable = false
		}
	}
	return report, nil
}

func (s *GPUOpsService) Waste(ctx context.Context, client *k8s.Client) (gpuops.WasteReport, error) {
	v, err := s.view(ctx, client)
	if err != nil {
		return gpuops.WasteReport{}, err
	}
	report := gpuops.BuildWaste(v.snap.Pods, v.utilNS, v.policy)
	report.Source = v.source
	return report, nil
}

func (s *GPUOpsService) Storm(ctx context.Context, client *k8s.Client) (gpuops.StormReport, error) {
	v, err := s.view(ctx, client)
	if err != nil {
		return gpuops.StormReport{}, err
	}
	report := gpuops.GroupStorm(v.snap.Events, v.snap.Owners)
	report.Source = v.source
	return report, nil
}

// Capacity is the proactive capacity and risk inspection. Health incidents use
// the same persistence state as Health, so a node only stops counting as
// usable once its incident is reported there.
func (s *GPUOpsService) Capacity(ctx context.Context, client *k8s.Client, zh bool) (gpuops.CapacityReport, error) {
	v, err := s.view(ctx, client)
	if err != nil {
		return gpuops.CapacityReport{}, err
	}
	s.mu.Lock()
	health := gpuops.BuildHealth(v.snap.Signals, v.snap.Pods, v.policy, time.Now(), s.seen, zh)
	s.mu.Unlock()
	report := gpuops.BuildCapacity(v.snap.Pods, v.snap.GPUOnNode, v.snap.Product, health.Incidents, zh)
	report.Source = v.source
	return report, nil
}

func (s *GPUOpsService) Placement(ctx context.Context, client *k8s.Client, namespace, name string, zh bool) (string, error) {
	v, err := s.view(ctx, client)
	if err != nil {
		return "", err
	}
	return gpuops.ExplainPlacement(v.snap.Pods, namespace, name, v.snap.GPUOnNode, v.snap.Product, v.utilPod, zh), nil
}

// Act cordons or drains a node when the current incident and heal tier allow it.
func (s *GPUOpsService) Act(ctx context.Context, client *k8s.Client, user, node, action string, confirm, zh bool) (GPUDecision, error) {
	node = strings.TrimSpace(node)
	action = strings.TrimSpace(action)
	dec := GPUDecision{At: time.Now(), User: user, Node: node, Action: action}
	fail := func(err error) (GPUDecision, error) {
		dec.Outcome = "refused"
		dec.Detail = err.Error()
		s.record(dec)
		return dec, err
	}
	if client != nil && client.Config != nil && k8s.IsShowcaseConfig(client.Config) {
		return fail(fmt.Errorf("%w: showcase mode is read-only", gpuops.ErrForbidden))
	}
	report, err := s.Health(ctx, client, zh)
	if err != nil {
		return fail(err)
	}
	var inc *gpuops.Incident
	for i := range report.Incidents {
		if report.Incidents[i].Node == node {
			inc = &report.Incidents[i]
			break
		}
	}
	if inc == nil {
		return fail(fmt.Errorf("%w: no GPU incident recommends action on node %s", gpuops.ErrRejected, node))
	}
	dec.Tier = inc.Tier
	if err := gpuops.Allows(*inc, action, confirm); err != nil {
		return fail(err)
	}
	var runErr error
	switch action {
	case "cordon":
		if _, runErr = s.nodes.SetUnschedulable(ctx, client.Clientset, node, true); runErr == nil {
			dec.Detail = "node cordoned"
		}
	case "drain":
		var result *DrainResult
		result, runErr = s.nodes.Drain(ctx, client.Clientset, node, DrainOptions{
			GracePeriodSeconds: 30,
			IgnoreDaemonSets:   true,
			DeleteEmptyDirData: true,
			TimeoutSeconds:     120,
		})
		if runErr == nil {
			dec.Detail = fmt.Sprintf("cordoned=%t evicted=%d skipped=%d failed=%d", result.Cordoned, result.Evicted, result.Skipped, result.Failed)
		}
	}
	dec.Outcome = "executed"
	if runErr != nil {
		dec.Outcome = "failed"
		dec.Detail = runErr.Error()
	}
	s.record(dec)
	s.auditDecision(dec)
	return dec, runErr
}

func (s *GPUOpsService) auditDecision(dec GPUDecision) {
	if s.audit == nil {
		return
	}
	result, severity := "success", "warning"
	if dec.Outcome != "executed" {
		result, severity = "failure", "error"
	}
	_ = s.audit.LogAPIRequest(nil, dec.User, "", "", "gpu:"+dec.Node, dec.Action, result, severity, map[string]interface{}{
		"tier": dec.Tier, "detail": dec.Detail,
	})
}

func (s *GPUOpsService) Decisions() []GPUDecision {
	s.mu.Lock()
	defer s.mu.Unlock()
	out := make([]GPUDecision, len(s.decisions))
	copy(out, s.decisions)
	return out
}

func (s *GPUOpsService) record(dec GPUDecision) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.decisions = append(s.decisions, dec)
	if len(s.decisions) > 100 {
		s.decisions = s.decisions[len(s.decisions)-100:]
	}
}
