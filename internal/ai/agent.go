package ai

import (
	"context"
	"fmt"
	"strings"

	"github.com/korecube/korecube/configs"
	"github.com/korecube/korecube/pkg/k8s"
)

type sendFn func(SSEEvent)

func Ready(cfg *configs.Config) bool {
	if cfg == nil || !cfg.AI.Enabled {
		return false
	}
	p := strings.ToLower(cfg.AI.Provider)
	if p == "" || p == "mock" {
		return true
	}
	return cfg.AI.APIKey != ""
}

func ProcessChat(ctx context.Context, cfg *configs.Config, client *k8s.Client, req *ChatRequest, send sendFn) {
	if cfg == nil || client == nil {
		send(SSEEvent{Event: "error", Data: map[string]string{"message": "AI or cluster unavailable"}})
		return
	}
	if len(req.Messages) == 0 {
		send(SSEEvent{Event: "error", Data: map[string]string{"message": "No messages"}})
		return
	}
	if req != nil {
		req.Language = normalizeLang(req.Language)
	}
	provider := strings.ToLower(cfg.AI.Provider)
	if provider == "" {
		provider = "mock"
	}
	switch provider {
	case "mock":
		processMock(ctx, client, req, send)
	case "openai":
		if err := processOpenAI(ctx, cfg, client, req, send); err != nil {
			send(SSEEvent{Event: "error", Data: map[string]string{"message": err.Error()}})
		}
	default:
		send(SSEEvent{Event: "error", Data: map[string]string{"message": fmt.Sprintf("unsupported provider %q", provider)}})
	}
}

func systemPrompt(namespace, lang string) string {
	nsHint := "all namespaces"
	nsHintZh := "全部命名空间"
	if namespace != "" {
		nsHint = "prefer namespace " + namespace
		nsHintZh = "优先命名空间 " + namespace
	}
	if isZh(lang) {
		return strings.TrimSpace(`
你是 KoreCube「集群调查员」——只读巡检与导航助手。
职责：用工具取证，用中文给出简洁结论，并指出控制台下一步入口。
规则：
- 涉及集群现状时必须先调用工具，禁止编造资源名。
- 全程用简体中文回答（资源名、Pod phase、事件 Reason 等 Kubernetes 专有名词可保留原文）。
- Pod / Deployment / Service / Node 名称必须原样写出，方便前端生成可点击卡片。
- 不能变更集群（禁止 create/update/delete/scale/exec）。若用户要求改动，拒绝并提示进控制台操作。
- 落地页 Skills（智能点检 / 快速调查 / 工作负载快照等）按多步工具计划执行。
范围提示：` + nsHintZh + `
工具：get_cluster_overview、list_resources、get_resource、get_pod_logs、get_gpu_health、get_gpu_waste、get_gpu_placement、get_event_storm。
GPU 故障只给出 cordon/drain 建议；真正执行必须走 GPU 运维页，由自愈层级和人工确认决定。
`)
	}
	return strings.TrimSpace(`
You are KoreCube's cluster investigator — a read-only triage and navigation agent.
Your job: gather evidence with tools, summarize clearly in English, and point the user into the console for details.
Rules:
- Always call tools before concluding about live cluster state. Never invent resource names.
- Reply in concise English (Kubernetes resource names and phases may stay as-is).
- Keep pod/deployment/service/node names exact so the UI can render clickable resource cards.
- You cannot mutate the cluster (no create/update/delete/scale/exec). If asked to change something, refuse and suggest the console path.
- Landing-page skills (Smart inspect / Quick triage / Workload snapshot, etc.) are multi-step tool playbooks.
Scope hint: ` + nsHint + `
Tools: get_cluster_overview, list_resources, get_resource, get_pod_logs, get_gpu_health, get_gpu_waste, get_gpu_placement, get_event_storm.
GPU faults are recommendations only. Cordon and drain happen on the GPU ops page, under the heal tier and a human confirm.
`)
}

func lastUserText(msgs []ChatMessage) string {
	for i := len(msgs) - 1; i >= 0; i-- {
		if msgs[i].Role == "user" {
			return msgs[i].Content
		}
	}
	return ""
}

func mergeRefs(dst *[]ResourceRef, add []ResourceRef) {
	seen := map[string]bool{}
	for _, r := range *dst {
		seen[r.Href] = true
	}
	for _, r := range add {
		if r.Href == "" || seen[r.Href] {
			continue
		}
		seen[r.Href] = true
		*dst = append(*dst, r)
	}
}
