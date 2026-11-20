package gpuops

import (
	"fmt"
	"sort"
)

// BuildWaste aggregates whole-GPU requests per namespace and compares them
// with utilization samples keyed by namespace. Missing samples are not treated
// as waste; the row asks for DCGM instead.
func BuildWaste(pods []PodFact, utilByNS map[string]float64, policy Policy) WasteReport {
	type acc struct {
		milli     int64
		exclusive int
	}
	by := map[string]*acc{}
	for _, p := range pods {
		if p.GPUMilli <= 0 || p.MIG {
			continue
		}
		a := by[p.Namespace]
		if a == nil {
			a = &acc{}
			by[p.Namespace] = a
		}
		a.milli += p.GPUMilli
		if p.GPUMilli >= 1000 {
			a.exclusive++
		}
	}
	names := make([]string, 0, len(by))
	for ns := range by {
		names = append(names, ns)
	}
	sort.Strings(names)
	threshold := policy.wasteThreshold()
	rows := make([]NamespaceUse, 0, len(names))
	for _, ns := range names {
		a := by[ns]
		row := NamespaceUse{
			Namespace:     ns,
			AllocatedGPU:  float64(a.milli) / 1000,
			ExclusivePods: a.exclusive,
			Suggestion:    "none",
		}
		if util, ok := utilByNS[ns]; ok {
			row.UtilKnown = true
			row.UtilPercent = util
			if a.exclusive > 0 && util < threshold {
				row.Waste = true
				row.Suggestion = "mig_or_timeslice"
			}
		} else if a.exclusive > 0 {
			row.Suggestion = "need_dcgm"
		}
		rows = append(rows, row)
	}
	return WasteReport{Threshold: threshold, Namespaces: rows}
}

// FormatWaste renders the report for the investigator.
func FormatWaste(r WasteReport, zh bool) string {
	if len(r.Namespaces) == 0 {
		if zh {
			return "没有发现申请了 nvidia.com/gpu 的 Pod。"
		}
		return "No pods request nvidia.com/gpu."
	}
	var b []string
	for _, row := range r.Namespaces {
		util := "n/a"
		if row.UtilKnown {
			util = fmt.Sprintf("%.0f%%", row.UtilPercent)
		}
		if zh {
			b = append(b, fmt.Sprintf("%s 分配=%.2f 卡 独占Pod=%d 利用率=%s 建议=%s",
				row.Namespace, row.AllocatedGPU, row.ExclusivePods, util, suggestionZh(row.Suggestion)))
			continue
		}
		b = append(b, fmt.Sprintf("%s allocated=%.2f exclusivePods=%d util=%s suggestion=%s",
			row.Namespace, row.AllocatedGPU, row.ExclusivePods, util, row.Suggestion))
	}
	return joinLines(b)
}

func suggestionZh(s string) string {
	switch s {
	case "mig_or_timeslice":
		return "低利用率且独占整卡，建议 MIG 或 time-slicing"
	case "need_dcgm":
		return "独占整卡，但没有 DCGM 利用率，先接采集再判断"
	default:
		return "无需调整"
	}
}
