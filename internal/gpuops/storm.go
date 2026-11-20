package gpuops

import "sort"

// symptomReasons are follower alerts that should fold into a GPU node fault.
var symptomReasons = map[string]bool{
	"Killing": true, "Evicted": true, "FailedScheduling": true,
	"Unhealthy": true, "NodeNotReady": true, "BackOff": true,
}

// GroupStorm collapses alerts that share an owner root, and folds pod symptoms
// on a node into that node's GPU fault when one exists.
func GroupStorm(events []StormEvent, owners map[string]OwnerLink) StormReport {
	if owners == nil {
		owners = map[string]OwnerLink{}
	}
	gpuNodes := map[string]bool{}
	for _, ev := range events {
		if ev.Node != "" && isGPUText(ev.Reason+" "+ev.Message) {
			gpuNodes[ev.Node] = true
		}
	}
	type acc struct {
		g StormGroup
	}
	groups := map[string]*acc{}
	order := []string{}
	for _, ev := range events {
		key, kind, name := groupKey(ev, owners, gpuNodes)
		g := groups[key]
		if g == nil {
			g = &acc{g: StormGroup{Key: key, Kind: kind, Name: name, Node: ev.Node, Sample: ev.Message}}
			groups[key] = g
			order = append(order, key)
		}
		g.g.Count++
		if ev.Reason != "" && !contains(g.g.Reasons, ev.Reason) {
			g.g.Reasons = append(g.g.Reasons, ev.Reason)
		}
		if g.g.Node == "" {
			g.g.Node = ev.Node
		}
	}
	items := make([]StormGroup, 0, len(order))
	for _, key := range order {
		items = append(items, groups[key].g)
	}
	suppressed := 0.0
	if len(events) > 0 {
		suppressed = 1 - float64(len(items))/float64(len(events))
	}
	return StormReport{Events: len(events), Groups: len(items), Suppressed: suppressed, Items: items}
}

func groupKey(ev StormEvent, owners map[string]OwnerLink, gpuNodes map[string]bool) (key, kind, name string) {
	if ev.Node != "" && gpuNodes[ev.Node] && (isGPUText(ev.Reason+" "+ev.Message) || symptomReasons[ev.Reason]) {
		return "node:" + ev.Node, "Node", ev.Node
	}
	if ev.UID != "" {
		root := rootOf(ev.UID, owners)
		if root.UID != "" {
			return root.UID, root.Kind, root.Name
		}
	}
	if ev.UID != "" {
		return ev.UID, ev.Kind, ev.Name
	}
	return ev.Kind + "/" + ev.Namespace + "/" + ev.Name, ev.Kind, ev.Name
}

func rootOf(uid string, owners map[string]OwnerLink) OwnerLink {
	seen := map[string]bool{}
	var last OwnerLink
	cur := uid
	for cur != "" && !seen[cur] {
		seen[cur] = true
		link, ok := owners[cur]
		if !ok {
			break
		}
		last = link
		if len(link.Parents) == 0 {
			break
		}
		cur = link.Parents[0]
	}
	return last
}

func contains(ss []string, v string) bool {
	for _, s := range ss {
		if s == v {
			return true
		}
	}
	return false
}

// FormatStorm renders the aggregation for the investigator.
func FormatStorm(r StormReport, zh bool) string {
	if r.Events == 0 {
		if zh {
			return "最近没有可聚合的 Warning 事件。"
		}
		return "No warning events to aggregate."
	}
	lines := []string{}
	if zh {
		lines = append(lines, sprintf("事件 %d 条合并为 %d 组，噪声压制 %.0f%%。", r.Events, r.Groups, r.Suppressed*100))
	} else {
		lines = append(lines, sprintf("%d events -> %d groups, suppressed %.0f%%.", r.Events, r.Groups, r.Suppressed*100))
	}
	items := append([]StormGroup(nil), r.Items...)
	sort.SliceStable(items, func(i, j int) bool { return items[i].Count > items[j].Count })
	for i, g := range items {
		if i >= 8 {
			break
		}
		lines = append(lines, sprintf("%s/%s x%d (%s)", g.Kind, g.Name, g.Count, joinComma(g.Reasons)))
	}
	return joinLines(lines)
}
