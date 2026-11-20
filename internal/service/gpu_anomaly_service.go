package service

import (
	"context"
	"log"
	"time"

	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/pkg/k8s"
)

const anomalyInterval = time.Minute

func (s *GPUOpsService) anomalyConfig() (gpuops.AnomalyConfig, time.Duration) {
	cfg := gpuops.AnomalyConfig{}
	window := time.Hour
	if s.cfg != nil {
		cfg.Threshold = s.cfg.GPU.AnomalyThreshold
		cfg.Persist = s.cfg.GPU.AnomalyPersist
		if s.cfg.GPU.AnomalyWindow > 0 {
			window = s.cfg.GPU.AnomalyWindow
		}
	}
	return cfg, window
}

// Anomalies returns the latest per-entity Isolation Forest pass. Live results
// come from the minute loop; a stale or missing cache is recomputed in place.
func (s *GPUOpsService) Anomalies(ctx context.Context, client *k8s.Client) (gpuops.AnomalyReport, error) {
	cfg, _ := s.anomalyConfig()
	now := time.Now()
	if client != nil && client.Config != nil && k8s.IsShowcaseConfig(client.Config) {
		rep := gpuops.DetectAnomalies(gpuops.ShowcaseSeries(now), cfg, now)
		rep.Source = "showcase"
		return rep, nil
	}
	if s.prom == nil || !s.prom.Enabled() {
		return gpuops.AnomalyReport{Source: "live", At: now, Unavailable: "Prometheus with dcgm-exporter metrics is not configured"}, nil
	}
	s.mu.Lock()
	cached := s.anomaly
	s.mu.Unlock()
	if cached != nil && now.Sub(cached.At) < anomalyInterval+30*time.Second {
		return *cached, nil
	}
	return s.detectLive(ctx), nil
}

func (s *GPUOpsService) detectLive(ctx context.Context) gpuops.AnomalyReport {
	cfg, window := s.anomalyConfig()
	now := time.Now()
	byFeature := map[string][]gpuops.PromSeries{}
	for _, f := range gpuops.AnomalyFeatures {
		res, err := s.prom.QueryRange(ctx, f.Metric, now.Add(-window), now, "60s")
		if err != nil || res == nil {
			continue
		}
		series, err := gpuops.ParsePromMatrix(res.Data)
		if err != nil {
			continue
		}
		byFeature[f.Name] = series
	}
	rep := gpuops.DetectAnomalies(gpuops.BuildEntitySeries(byFeature), cfg, now)
	rep.Source = "live"
	if len(byFeature) == 0 {
		rep.Unavailable = "no DCGM gauges returned by Prometheus"
	}
	s.mu.Lock()
	s.anomaly = &rep
	s.mu.Unlock()
	return rep
}

// StartAnomalyLoop scores every entity once a minute. The public showcase
// computes on request instead, from synthetic history.
func (s *GPUOpsService) StartAnomalyLoop() {
	if k8s.IsShowcase() {
		return
	}
	go s.runAnomalyLoop(context.Background())
	log.Println("gpu anomaly detector started (isolation forest, every 1m)")
}

func (s *GPUOpsService) runAnomalyLoop(ctx context.Context) {
	t := time.NewTicker(anomalyInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-t.C:
			if s.prom == nil || !s.prom.Enabled() {
				continue
			}
			pass, cancel := context.WithTimeout(ctx, 50*time.Second)
			rep := s.detectLive(pass)
			cancel()
			if len(rep.Persistent) > 0 {
				log.Printf("gpu anomaly: %d persistent of %d entities", len(rep.Persistent), rep.Entities)
			}
		}
	}
}
