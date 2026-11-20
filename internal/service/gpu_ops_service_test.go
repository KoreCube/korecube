package service

import (
	"context"
	"errors"
	"testing"

	"github.com/korecube/korecube/internal/gpuops"
	"github.com/korecube/korecube/pkg/k8s"
)

func showcaseClient() *k8s.Client {
	return k8s.NewShowcaseClientAt(
		k8s.ShowcaseStagingAPIHost,
		k8s.ShowcaseStagingVersion,
		k8s.ShowcaseStagingSeedObjects()...,
	)
}

func TestGPUShowcaseReportsAreReadOnly(t *testing.T) {
	svc := NewGPUOpsService(nil, nil, nil, nil)
	client := showcaseClient()

	health, err := svc.Health(context.Background(), client, false)
	if err != nil {
		t.Fatal(err)
	}
	if !health.ReadOnly || len(health.Incidents) == 0 {
		t.Fatalf("health: %+v", health)
	}
	for _, inc := range health.Incidents {
		if inc.Executable {
			t.Fatalf("showcase incident must not be executable: %+v", inc)
		}
	}

	dec, err := svc.Act(context.Background(), client, "admin", "gpu-a100-1", "drain", true, false)
	if !errors.Is(err, gpuops.ErrForbidden) || dec.Outcome != "refused" {
		t.Fatalf("expected refusal, got %+v %v", dec, err)
	}
	if got := svc.Decisions(); len(got) != 1 || got[0].Outcome != "refused" {
		t.Fatalf("refusal should be on the trail: %+v", got)
	}
}

func TestGPUShowcaseWasteAndStorm(t *testing.T) {
	svc := NewGPUOpsService(nil, nil, nil, nil)
	client := showcaseClient()

	waste, err := svc.Waste(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	flagged := false
	for _, row := range waste.Namespaces {
		if row.Namespace == "llm-inference" && row.Waste {
			flagged = true
		}
	}
	if !flagged {
		t.Fatalf("llm-inference should be flagged: %+v", waste.Namespaces)
	}

	storm, err := svc.Storm(context.Background(), client)
	if err != nil {
		t.Fatal(err)
	}
	if storm.Groups >= storm.Events {
		t.Fatalf("storm did not collapse: %+v", storm)
	}
}
