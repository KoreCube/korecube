package gpuops

import (
	"encoding/json"
	"math/rand"
	"testing"
	"time"
)

func TestForestScoresOutlierHigher(t *testing.T) {
	rng := rand.New(rand.NewSource(1))
	var rows [][]float64
	for i := 0; i < 200; i++ {
		rows = append(rows, []float64{50 + rng.NormFloat64()*3, 200 + rng.NormFloat64()*5})
	}
	f := FitForest(rows, 100, 128, 7)
	normal := f.Score([]float64{50, 200})
	outlier := f.Score([]float64{100, 400})
	if outlier < 0.65 || normal > 0.55 {
		t.Fatalf("normal=%.3f outlier=%.3f", normal, outlier)
	}
}

func TestShowcaseKeepsPersistentAndDropsSpike(t *testing.T) {
	now := time.Date(2026, 11, 20, 12, 0, 0, 0, time.UTC)
	rep := DetectAnomalies(ShowcaseSeries(now), AnomalyConfig{}, now)
	got := map[string]bool{}
	for _, a := range rep.Persistent {
		got[a.Entity] = true
		if a.Streak < rep.Persist {
			t.Fatalf("%s streak %d below persist", a.Entity, a.Streak)
		}
	}
	if !got["gpu:gpu-a100-2/1"] || !got["pod:llm-inference/llm-inf-7f-c"] {
		t.Fatalf("hung GPU not reported: %+v", rep.Persistent)
	}
	if got["gpu:gpu-a100-1/3"] {
		t.Fatal("one-minute spike should be transient")
	}
	if rep.Transient == 0 {
		t.Fatal("expected the spike to be counted as transient")
	}
}

func TestShortSeriesNotTrained(t *testing.T) {
	s := Series{Entity: "gpu:n/0", Features: []string{"util"}}
	for i := 0; i < 10; i++ {
		s.Points = append(s.Points, Observation{Values: []float64{float64(i)}})
	}
	rep := DetectAnomalies([]Series{s}, AnomalyConfig{}, time.Now())
	if rep.Trained != 0 || len(rep.Persistent) != 0 {
		t.Fatalf("unexpected %+v", rep)
	}
}

func TestBuildEntitySeriesAlignsFeatures(t *testing.T) {
	raw := json.RawMessage(`{"resultType":"matrix","result":[
	 {"metric":{"Hostname":"n1","gpu":"0","namespace":"ns","pod":"p"},"values":[[60,"10"],[120,"20"]]},
	 {"metric":{"Hostname":"n1","gpu":"1"},"values":[[60,"30"],[120,"40"]]}]}`)
	util, err := ParsePromMatrix(raw)
	if err != nil {
		t.Fatal(err)
	}
	power, _ := ParsePromMatrix(json.RawMessage(`{"result":[{"metric":{"Hostname":"n1","gpu":"0"},"values":[[120,"300"]]}]}`))
	series := BuildEntitySeries(map[string][]PromSeries{"util": util, "power": power})
	by := map[string]Series{}
	for _, s := range series {
		by[s.Entity] = s
	}
	g0 := by["gpu:n1/0"]
	if len(g0.Features) != 2 || len(g0.Points) != 1 || g0.Points[0].Values[1] != 300 {
		t.Fatalf("gpu0 %+v", g0)
	}
	if n := by["node:n1"]; len(n.Points) != 1 || n.Points[0].Values[0] != 30 {
		t.Fatalf("node %+v", n)
	}
	if _, ok := by["pod:ns/p"]; !ok {
		t.Fatal("pod entity missing")
	}
}
