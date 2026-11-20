package gpuops

import (
	"errors"
	"testing"
)

func TestBuildScaledObjectValidates(t *testing.T) {
	cases := []ScalingRequest{
		{Namespace: "Bad_NS", Target: "a"},
		{Namespace: "ml", Target: "a", MinReplicas: 5, MaxReplicas: 2},
		{Namespace: "ml", Target: "a", MaxReplicas: 500},
		{Namespace: "ml", Target: "a", Threshold: "lots"},
		{Namespace: "ml", Target: "a", Trigger: "kafka", Topic: "jobs"},
		{Namespace: "ml", Target: "a", Trigger: "sqs"},
	}
	for _, c := range cases {
		if _, _, err := BuildScaledObject(c); !errors.Is(err, ErrRejected) {
			t.Fatalf("%+v: expected rejection, got %v", c, err)
		}
	}
}

func TestBuildScaledObjectKafka(t *testing.T) {
	obj, _, err := BuildScaledObject(ScalingRequest{
		Namespace: "ml", Target: "worker", Trigger: "kafka", Topic: "jobs", ConsumerGroup: "g", Bootstrap: "kafka:9092", Threshold: "50", MinReplicas: 1, MaxReplicas: 4,
	})
	if err != nil {
		t.Fatal(err)
	}
	spec := obj["spec"].(map[string]interface{})
	meta := spec["triggers"].([]interface{})[0].(map[string]interface{})["metadata"].(map[string]interface{})
	if meta["lagThreshold"] != "50" || spec["maxReplicaCount"] != int64(4) {
		t.Fatalf("spec: %+v", spec)
	}
}

func TestBuildNodePoolNew(t *testing.T) {
	obj, warns, err := BuildNodePool(ScalingRequest{Pool: "train", GPULimit: 16, InstanceTypes: []string{"p4d.24xlarge"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	limits := obj["spec"].(map[string]interface{})["limits"].(map[string]interface{})
	if limits["nvidia.com/gpu"] != "16" || len(warns) != 1 {
		t.Fatalf("limits=%v warns=%v", limits, warns)
	}
	if _, _, err := BuildNodePool(ScalingRequest{GPULimit: 9999}, nil); !errors.Is(err, ErrRejected) {
		t.Fatal("expected limit rejection")
	}
}
