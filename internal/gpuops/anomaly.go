package gpuops

import (
	"encoding/json"
	"hash/fnv"
	"math"
	"math/rand"
	"sort"
	"strconv"
	"strings"
	"time"
)

// AnomalyFeatures are the DCGM gauges each entity model is trained on.
var AnomalyFeatures = []struct {
	Name   string
	Metric string
}{
	{"util", "DCGM_FI_DEV_GPU_UTIL"},
	{"fb_used", "DCGM_FI_DEV_FB_USED"},
	{"power", "DCGM_FI_DEV_POWER_USAGE"},
	{"temp", "DCGM_FI_DEV_GPU_TEMP"},
}

// AnomalyConfig bounds the per-entity detector.
// Recent points are scored against a forest trained on the older ones; only a
// trailing run of at least Persist anomalous points is reported.
type AnomalyConfig struct {
	Threshold float64
	Persist   int
	Recent    int
	MinTrain  int
	Trees     int
}

func (c AnomalyConfig) withDefaults() AnomalyConfig {
	if c.Threshold <= 0 || c.Threshold >= 1 {
		c.Threshold = 0.7
	}
	if c.Persist <= 0 {
		c.Persist = 3
	}
	if c.Recent < c.Persist {
		c.Recent = 15
		if c.Recent < c.Persist {
			c.Recent = c.Persist
		}
	}
	if c.MinTrain <= 0 {
		c.MinTrain = 20
	}
	if c.Trees <= 0 {
		c.Trees = 64
	}
	return c
}

// Observation is one aligned feature vector for an entity.
type Observation struct {
	At     time.Time
	Values []float64
}

// Series is the history of one GPU, pod, or node.
type Series struct {
	Entity    string
	Kind      string // gpu, pod, node
	Node      string
	GPU       string
	Namespace string
	Pod       string
	Features  []string
	Points    []Observation
}

// Anomaly is an entity whose latest points stay isolated for Persist minutes.
type Anomaly struct {
	Entity    string             `json:"entity"`
	Kind      string             `json:"kind"`
	Node      string             `json:"node,omitempty"`
	GPU       string             `json:"gpu,omitempty"`
	Namespace string             `json:"namespace,omitempty"`
	Pod       string             `json:"pod,omitempty"`
	Score     float64            `json:"score"`
	Streak    int                `json:"streak"`
	Since     time.Time          `json:"since"`
	Latest    map[string]float64 `json:"latest"`
	Baseline  map[string]float64 `json:"baseline"`
}

// AnomalyReport is one detection pass over every entity.
type AnomalyReport struct {
	Source      string    `json:"source"`
	At          time.Time `json:"at"`
	Threshold   float64   `json:"threshold"`
	Persist     int       `json:"persist"`
	Entities    int       `json:"entities"`
	Trained     int       `json:"trained"`
	Transient   int       `json:"transient"`
	Persistent  []Anomaly `json:"persistent"`
	Unavailable string    `json:"unavailable,omitempty"`
}

func seedFor(key string) int64 {
	h := fnv.New64a()
	_, _ = h.Write([]byte(key))
	return int64(h.Sum64() & math.MaxInt64)
}

// DetectAnomalies trains one forest per entity and keeps persistent anomalies.
// Transient counts anomalous runs in the recent window that did not last.
func DetectAnomalies(series []Series, cfg AnomalyConfig, now time.Time) AnomalyReport {
	cfg = cfg.withDefaults()
	rep := AnomalyReport{At: now, Threshold: cfg.Threshold, Persist: cfg.Persist, Entities: len(series)}
	for _, s := range series {
		if len(s.Points) < cfg.MinTrain+cfg.Persist {
			continue
		}
		recent := cfg.Recent
		if len(s.Points)-recent < cfg.MinTrain {
			recent = len(s.Points) - cfg.MinTrain
		}
		train := s.Points[:len(s.Points)-recent]
		rows := make([][]float64, len(train)+1)
		for i, p := range train {
			rows[i] = p.Values
		}
		rep.Trained++

		streak, run := 0, 0
		var maxScore float64
		for _, p := range s.Points[len(s.Points)-recent:] {
			// A forest only sees its training range, so each point joins the
			// baseline it is scored against; other recent points stay out so a
			// sustained fault cannot mask itself.
			rows[len(train)] = p.Values
			score := FitForest(rows, cfg.Trees, 256, seedFor(s.Entity)).Score(p.Values)
			if score >= cfg.Threshold {
				run++
				if run == 1 {
					maxScore = 0
				}
				maxScore = math.Max(maxScore, score)
				continue
			}
			if run > 0 && run < cfg.Persist {
				rep.Transient++
			}
			run = 0
		}
		streak = run
		if streak == 0 {
			continue
		}
		if streak < cfg.Persist {
			rep.Transient++
			continue
		}
		last := s.Points[len(s.Points)-1]
		a := Anomaly{
			Entity: s.Entity, Kind: s.Kind, Node: s.Node, GPU: s.GPU, Namespace: s.Namespace, Pod: s.Pod,
			Score: math.Round(maxScore*1000) / 1000, Streak: streak,
			Since:    s.Points[len(s.Points)-streak].At,
			Latest:   map[string]float64{},
			Baseline: map[string]float64{},
		}
		for j, f := range s.Features {
			a.Latest[f] = last.Values[j]
			col := make([]float64, len(train))
			for i, p := range train {
				col[i] = p.Values[j]
			}
			a.Baseline[f] = median(col)
		}
		rep.Persistent = append(rep.Persistent, a)
	}
	sort.Slice(rep.Persistent, func(i, j int) bool {
		if rep.Persistent[i].Streak != rep.Persistent[j].Streak {
			return rep.Persistent[i].Streak > rep.Persistent[j].Streak
		}
		return rep.Persistent[i].Score > rep.Persistent[j].Score
	})
	return rep
}

func median(v []float64) float64 {
	if len(v) == 0 {
		return 0
	}
	c := append([]float64(nil), v...)
	sort.Float64s(c)
	m := len(c) / 2
	if len(c)%2 == 1 {
		return c[m]
	}
	return (c[m-1] + c[m]) / 2
}

// PromSeries is one range-query matrix element.
type PromSeries struct {
	Labels map[string]string
	Points map[int64]float64
}

// ParsePromMatrix reads the `data` object of a Prometheus range query.
func ParsePromMatrix(data json.RawMessage) ([]PromSeries, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var parsed struct {
		Result []struct {
			Metric map[string]string `json:"metric"`
			Values [][]interface{}   `json:"values"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	out := make([]PromSeries, 0, len(parsed.Result))
	for _, row := range parsed.Result {
		ps := PromSeries{Labels: row.Metric, Points: map[int64]float64{}}
		for _, pair := range row.Values {
			if len(pair) < 2 {
				continue
			}
			ts, ok := pair[0].(float64)
			if !ok {
				continue
			}
			raw, _ := pair[1].(string)
			v, err := strconv.ParseFloat(raw, 64)
			if err != nil || math.IsNaN(v) || math.IsInf(v, 0) {
				continue
			}
			ps.Points[int64(ts)] = v
		}
		out = append(out, ps)
	}
	return out, nil
}

type entityAcc struct {
	s      Series
	values map[string]map[int64][]float64
}

// BuildEntitySeries aligns DCGM range data into GPU, pod, and node series.
// Pod and node vectors average every GPU sample that carries the label.
func BuildEntitySeries(byFeature map[string][]PromSeries) []Series {
	acc := map[string]*entityAcc{}
	add := func(key string, base Series, feature string, points map[int64]float64) {
		e := acc[key]
		if e == nil {
			base.Entity = key
			e = &entityAcc{s: base, values: map[string]map[int64][]float64{}}
			acc[key] = e
		}
		if e.values[feature] == nil {
			e.values[feature] = map[int64][]float64{}
		}
		for ts, v := range points {
			e.values[feature][ts] = append(e.values[feature][ts], v)
		}
	}
	for _, f := range AnomalyFeatures {
		for _, ps := range byFeature[f.Name] {
			node := labelNode(ps.Labels)
			if node == "" {
				continue
			}
			gpu := labelGPU(ps.Labels)
			add("gpu:"+node+"/"+gpu, Series{Kind: "gpu", Node: node, GPU: gpu}, f.Name, ps.Points)
			add("node:"+node, Series{Kind: "node", Node: node}, f.Name, ps.Points)
			if ns, pod := labelNS(ps.Labels), labelPod(ps.Labels); ns != "" && pod != "" {
				add("pod:"+ns+"/"+pod, Series{Kind: "pod", Node: node, Namespace: ns, Pod: pod}, f.Name, ps.Points)
			}
		}
	}
	keys := make([]string, 0, len(acc))
	for k := range acc {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	out := make([]Series, 0, len(keys))
	for _, k := range keys {
		e := acc[k]
		var feats []string
		for _, f := range AnomalyFeatures {
			if len(e.values[f.Name]) > 0 {
				feats = append(feats, f.Name)
			}
		}
		if len(feats) == 0 {
			continue
		}
		var stamps []int64
		for ts := range e.values[feats[0]] {
			complete := true
			for _, f := range feats[1:] {
				if _, ok := e.values[f][ts]; !ok {
					complete = false
					break
				}
			}
			if complete {
				stamps = append(stamps, ts)
			}
		}
		sort.Slice(stamps, func(i, j int) bool { return stamps[i] < stamps[j] })
		s := e.s
		s.Features = feats
		for _, ts := range stamps {
			vec := make([]float64, len(feats))
			for j, f := range feats {
				vals := e.values[f][ts]
				var sum float64
				for _, v := range vals {
					sum += v
				}
				vec[j] = sum / float64(len(vals))
			}
			s.Points = append(s.Points, Observation{At: time.Unix(ts, 0), Values: vec})
		}
		out = append(out, s)
	}
	return out
}

// ShowcaseSeries is a deterministic hour of demo telemetry: gpu-a100-2/1 hangs
// for the last six minutes, gpu-a100-1/3 has a one-minute spike that should be
// dropped as transient.
func ShowcaseSeries(now time.Time) []Series {
	rng := rand.New(rand.NewSource(42))
	feats := []string{"util", "fb_used", "power", "temp"}
	const points = 60
	start := now.Truncate(time.Minute).Add(-time.Duration(points-1) * time.Minute)
	type gpuSpec struct {
		node, gpu, ns, pod string
		util, fb, power    float64
	}
	specs := []gpuSpec{
		{"gpu-a100-1", "0", "training", "pretrain-0", 92, 71000, 360},
		{"gpu-a100-1", "3", "", "", 35, 20000, 180},
		{"gpu-a100-2", "0", "llm-inference", "llm-inf-7f-b", 70, 42000, 260},
		{"gpu-a100-2", "1", "llm-inference", "llm-inf-7f-c", 64, 40000, 250},
	}
	var out []Series
	nodeAcc := map[string][][]float64{}
	for _, sp := range specs {
		s := Series{Entity: "gpu:" + sp.node + "/" + sp.gpu, Kind: "gpu", Node: sp.node, GPU: sp.gpu, Features: feats}
		for i := 0; i < points; i++ {
			util := sp.util + rng.NormFloat64()*4
			fb := sp.fb + rng.NormFloat64()*600
			power := sp.power + rng.NormFloat64()*10
			temp := 55 + util*0.2 + rng.NormFloat64()
			if sp.node == "gpu-a100-2" && sp.gpu == "1" && i >= points-6 {
				util, power, temp = 100, 405+rng.NormFloat64()*3, 87+rng.NormFloat64()
			}
			if sp.node == "gpu-a100-1" && sp.gpu == "3" && i == points-8 {
				util, power, temp = 99, 390, 83
			}
			vec := []float64{util, fb, power, temp}
			s.Points = append(s.Points, Observation{At: start.Add(time.Duration(i) * time.Minute), Values: vec})
			if len(nodeAcc[sp.node]) < points {
				nodeAcc[sp.node] = append(nodeAcc[sp.node], make([]float64, len(feats)+1))
			}
			row := nodeAcc[sp.node][i]
			for j := range vec {
				row[j] += vec[j]
			}
			row[len(feats)]++
		}
		out = append(out, s)
		if sp.pod != "" {
			p := s
			p.Entity, p.Kind, p.Namespace, p.Pod = "pod:"+sp.ns+"/"+sp.pod, "pod", sp.ns, sp.pod
			out = append(out, p)
		}
	}
	nodes := make([]string, 0, len(nodeAcc))
	for n := range nodeAcc {
		nodes = append(nodes, n)
	}
	sort.Strings(nodes)
	for _, n := range nodes {
		s := Series{Entity: "node:" + n, Kind: "node", Node: n, Features: feats}
		for i, row := range nodeAcc[n] {
			vec := make([]float64, len(feats))
			for j := range vec {
				vec[j] = row[j] / row[len(feats)]
			}
			s.Points = append(s.Points, Observation{At: start.Add(time.Duration(i) * time.Minute), Values: vec})
		}
		out = append(out, s)
	}
	return out
}

// FormatAnomalies is the investigator view: only persistent anomalies, with
// the latest values next to the trained baseline.
func FormatAnomalies(r AnomalyReport, zh bool) string {
	var b strings.Builder
	if r.Unavailable != "" {
		if zh {
			b.WriteString("异常检测不可用：" + r.Unavailable)
		} else {
			b.WriteString("Anomaly detection unavailable: " + r.Unavailable)
		}
		return b.String()
	}
	if zh {
		b.WriteString(sprintf("Isolation Forest 异常检测：实体 %d，已训练 %d，阈值 %.2f，持续 ≥%d 分钟才上报；丢弃瞬态尖峰 %d 次。\n",
			r.Entities, r.Trained, r.Threshold, r.Persist, r.Transient))
	} else {
		b.WriteString(sprintf("Isolation Forest anomalies: entities=%d trained=%d threshold=%.2f persist>=%dm transient_dropped=%d\n",
			r.Entities, r.Trained, r.Threshold, r.Persist, r.Transient))
	}
	if len(r.Persistent) == 0 {
		if zh {
			b.WriteString("没有持续异常。")
		} else {
			b.WriteString("No persistent anomalies.")
		}
		return b.String()
	}
	for _, a := range r.Persistent {
		var diffs []string
		keys := make([]string, 0, len(a.Latest))
		for k := range a.Latest {
			keys = append(keys, k)
		}
		sort.Strings(keys)
		for _, k := range keys {
			diffs = append(diffs, sprintf("%s %.0f (baseline %.0f)", k, a.Latest[k], a.Baseline[k]))
		}
		if zh {
			b.WriteString(sprintf("- %s 连续 %d 分钟异常，score=%.2f：%s\n", a.Entity, a.Streak, a.Score, strings.Join(diffs, ", ")))
		} else {
			b.WriteString(sprintf("- %s anomalous for %dm, score=%.2f: %s\n", a.Entity, a.Streak, a.Score, strings.Join(diffs, ", ")))
		}
	}
	return strings.TrimSpace(b.String())
}
