package gpuops

import (
	"encoding/json"
	"strconv"
	"strings"
)

// ParsePromVector reads the `data` object of a Prometheus instant query.
func ParsePromVector(data json.RawMessage) ([]PromSample, error) {
	if len(data) == 0 {
		return nil, nil
	}
	var parsed struct {
		ResultType string `json:"resultType"`
		Result     []struct {
			Metric map[string]string `json:"metric"`
			Value  []interface{}     `json:"value"`
		} `json:"result"`
	}
	if err := json.Unmarshal(data, &parsed); err != nil {
		return nil, err
	}
	out := make([]PromSample, 0, len(parsed.Result))
	for _, row := range parsed.Result {
		if len(row.Value) < 2 {
			continue
		}
		raw, _ := row.Value[1].(string)
		v, err := strconv.ParseFloat(raw, 64)
		if err != nil {
			continue
		}
		out = append(out, PromSample{Labels: row.Metric, Value: v})
	}
	return out, nil
}

func labelNode(labels map[string]string) string {
	for _, key := range []string{"Hostname", "hostname", "kubernetes_node", "node", "exported_hostname"} {
		if v := strings.TrimSpace(labels[key]); v != "" {
			return v
		}
	}
	return ""
}

func labelGPU(labels map[string]string) string {
	for _, key := range []string{"gpu", "GPU_I_ID", "device"} {
		if v := labels[key]; v != "" {
			return v
		}
	}
	return ""
}

func labelNS(labels map[string]string) string {
	for _, key := range []string{"namespace", "exported_namespace"} {
		if v := labels[key]; v != "" {
			return v
		}
	}
	return ""
}

func labelPod(labels map[string]string) string {
	for _, key := range []string{"pod", "exported_pod"} {
		if v := labels[key]; v != "" {
			return v
		}
	}
	return ""
}

// SignalsFromDCGM turns DCGM instant samples into health signals.
func SignalsFromDCGM(kind string, samples []PromSample) []Signal {
	var out []Signal
	for _, s := range samples {
		node := labelNode(s.Labels)
		if node == "" || s.Value <= 0 {
			continue
		}
		sig := Signal{Node: node, GPU: labelGPU(s.Labels), Kind: kind, Source: "dcgm"}
		switch kind {
		case "xid":
			code := int(s.Value)
			fatal, reason := ClassifyXID(code)
			sig.Code = sprintf("%d", code)
			sig.Recoverable = !fatal
			sig.Message = "DCGM " + reason
		case "ecc":
			sig.Recoverable = false
			sig.Code = "dbe"
			sig.Message = sprintf("DCGM double-bit ECC %.0f", s.Value)
		case "nvlink":
			sig.Recoverable = s.Value < 100
			sig.Code = "crc"
			sig.Message = sprintf("DCGM NVLink CRC errors %.0f", s.Value)
		default:
			continue
		}
		out = append(out, sig)
	}
	return out
}

// UtilByNamespace averages samples that carry a namespace label.
func UtilByNamespace(samples []PromSample) map[string]float64 {
	sum := map[string]float64{}
	n := map[string]float64{}
	for _, s := range samples {
		ns := labelNS(s.Labels)
		if ns == "" {
			continue
		}
		sum[ns] += s.Value
		n[ns]++
	}
	out := map[string]float64{}
	for ns, total := range sum {
		out[ns] = total / n[ns]
	}
	return out
}

// UtilByPod keys are namespace/pod.
func UtilByPod(samples []PromSample) map[string]float64 {
	out := map[string]float64{}
	for _, s := range samples {
		ns, pod := labelNS(s.Labels), labelPod(s.Labels)
		if ns == "" || pod == "" {
			continue
		}
		out[ns+"/"+pod] = s.Value
	}
	return out
}
