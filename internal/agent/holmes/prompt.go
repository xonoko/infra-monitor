package holmes

import (
	"encoding/json"
	"fmt"
	"sort"
	"strings"
	"time"

	"github.com/xonoko/infra-monitor/internal/agent"
)

const basePrompt = `You are working for infra-monitor, the incident hub of a Kubernetes platform team.
infra-monitor receives alerts, groups them into incidents and asks you to investigate.
You are strictly read-only: never modify anything in the cluster or elsewhere, only read with your tools.
infra-monitor can run a fixed set of remediation actions, but only after a human approves them.
You may PROPOSE actions only from the list of allowed actions below. Never invent other action types,
never propose an action whose params do not match its JSON schema, and prefer proposing no action at all
when the evidence is weak or a human should decide. Each proposal needs a short rationale and a risk level.`

func actionList(types []agent.ActionType) string {
	if len(types) == 0 {
		return "\n\nAllowed actions: none. Do not propose any action (return an empty proposed_actions list)."
	}
	sorted := append([]agent.ActionType(nil), types...)
	sort.Slice(sorted, func(i, j int) bool { return sorted[i].Type < sorted[j].Type })
	var b strings.Builder
	b.WriteString("\n\nAllowed actions:")
	for _, t := range sorted {
		fmt.Fprintf(&b, "\n- %s (default risk %s): %s\n  params JSON schema: %s", t.Type, t.Risk, t.Description, compactJSON(t.ParamsSchema))
	}
	return b.String()
}

func compactJSON(raw json.RawMessage) string {
	if len(raw) == 0 {
		return "{}"
	}
	var v any
	if json.Unmarshal(raw, &v) != nil {
		return string(raw)
	}
	b, _ := json.Marshal(v)
	return string(b)
}

const investigationFields = `verdict ("actionable" when the problem is real and a human should act or watch it,
"noise" when it is expected, transient, already recovered or not worth attention, "unknown" when you cannot tell),
summary (at most 3 short plain-text sentences for a chat notification, no markdown),
root_cause (one short paragraph), confidence (number between 0 and 1),
analysis (markdown: what you checked, evidence from your tools, root cause, recommended next steps),
proposed_actions (possibly empty list)`

func investigationSystemPrompt(types []agent.ActionType, structured bool, extra string) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString(actionList(types))
	b.WriteString("\n\nAnswer with these fields: ")
	b.WriteString(investigationFields)
	if structured {
		b.WriteString(".\nEach proposed action has: type, params_json (the params object encoded as a JSON string matching the schema), rationale, risk (low|medium|high).")
		b.WriteString("\nIf you cannot produce structured output, end your answer with one fenced ```json block containing the same fields.")
	} else {
		b.WriteString(fencedInstructions(`{"verdict": "...", "summary": "...", "root_cause": "...", "confidence": 0.0, "analysis": "...", "proposed_actions": [{"type": "...", "params": {}, "rationale": "...", "risk": "low|medium|high"}]}`))
	}
	if strings.TrimSpace(extra) != "" {
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(extra))
	}
	return b.String()
}

func chatSystemPrompt(types []agent.ActionType, structured bool, extra string) string {
	var b strings.Builder
	b.WriteString(basePrompt)
	b.WriteString("\n\nYou are now chatting with the on-call engineers about this incident. Messages are prefixed with the author's name.")
	b.WriteString(" Answer concisely in markdown; use your tools when the question needs fresh data.")
	b.WriteString(actionList(types))
	if structured {
		b.WriteString("\n\nAnswer with: reply (markdown) and proposed_actions (possibly empty; each with type, params_json = params object encoded as a JSON string, rationale, risk).")
		b.WriteString("\nIf you cannot produce structured output, end your answer with one fenced ```json block containing the same fields.")
	} else {
		b.WriteString("\n\nWrite your reply in markdown." + fencedInstructions(`{"proposed_actions": [{"type": "...", "params": {}, "rationale": "...", "risk": "low|medium|high"}]}`) +
			"\nOmit the block when you propose nothing.")
	}
	if strings.TrimSpace(extra) != "" {
		b.WriteString("\n\n")
		b.WriteString(strings.TrimSpace(extra))
	}
	return b.String()
}

func fencedInstructions(example string) string {
	return "\n\nEnd your answer with exactly one fenced ```json code block containing a JSON object like:\n```json\n" + example + "\n```"
}

func incidentContext(ic agent.IncidentContext) string {
	var b strings.Builder
	fmt.Fprintf(&b, "Incident #%d: %s\n", ic.IncidentID, ic.Title)
	fmt.Fprintf(&b, "Status: %s, severity: %s", orDash(ic.Status), orDash(ic.Severity))
	if ic.Cluster != "" {
		fmt.Fprintf(&b, ", cluster: %s", ic.Cluster)
	}
	if ic.Namespace != "" {
		fmt.Fprintf(&b, ", namespace: %s", ic.Namespace)
	}
	b.WriteString("\n")
	if !ic.OpenedAt.IsZero() {
		fmt.Fprintf(&b, "Opened at: %s\n", ic.OpenedAt.UTC().Format(time.RFC3339))
	}
	if len(ic.Labels) > 0 {
		fmt.Fprintf(&b, "Labels: %s\n", labelString(ic.Labels))
	}
	if ic.URL != "" {
		fmt.Fprintf(&b, "UI: %s\n", ic.URL)
	}
	if len(ic.Signals) > 0 {
		fmt.Fprintf(&b, "\nAlerts (%d):\n", len(ic.Signals))
		for i, s := range ic.Signals {
			if i >= 20 {
				fmt.Fprintf(&b, "... and %d more\n", len(ic.Signals)-20)
				break
			}
			fmt.Fprintf(&b, "- %s [%s]", orDash(s.Name), s.Status)
			if s.Severity != "" {
				fmt.Fprintf(&b, " severity=%s", s.Severity)
			}
			if !s.StartsAt.IsZero() {
				fmt.Fprintf(&b, " since %s", s.StartsAt.UTC().Format(time.RFC3339))
			}
			b.WriteString("\n")
			if len(s.Labels) > 0 {
				fmt.Fprintf(&b, "  labels: %s\n", labelString(s.Labels))
			}
			for _, k := range sortedKeys(s.Annotations) {
				fmt.Fprintf(&b, "  %s: %s\n", k, truncate(s.Annotations[k], 500))
			}
			if s.GeneratorURL != "" {
				fmt.Fprintf(&b, "  source: %s\n", s.GeneratorURL)
			}
		}
	}
	if ic.PreviousAnalysis != "" {
		fmt.Fprintf(&b, "\nPrevious analysis:\n%s\n", truncate(ic.PreviousAnalysis, 4000))
	}
	return b.String()
}

func investigationAsk(ic agent.IncidentContext) string {
	return "Investigate this alert and find the root cause using your tools (pods, events, logs, metrics...).\n" +
		"Decide whether it needs human attention and which allowed action, if any, would fix it.\n\n" + incidentContext(ic)
}

func chatAsk(req agent.ChatRequest, hasHistory bool) string {
	author := strings.TrimSpace(req.Author)
	if author == "" {
		author = "user"
	}
	msg := author + ": " + req.Message
	if !hasHistory {
		msg = "Context: an engineer asks about this incident (no investigation has run yet).\n\n" + incidentContext(req.Incident) + "\n" + msg
	}
	if len(req.Incident.ActionTypes) > 0 {
		names := make([]string, 0, len(req.Incident.ActionTypes))
		for _, t := range req.Incident.ActionTypes {
			names = append(names, t.Type)
		}
		sort.Strings(names)
		msg += "\n\n(Reminder: you may only propose these actions: " + strings.Join(names, ", ") + ".)"
	}
	return msg
}

func labelString(m map[string]string) string {
	parts := make([]string, 0, len(m))
	for _, k := range sortedKeys(m) {
		parts = append(parts, k+"="+m[k])
	}
	return strings.Join(parts, ", ")
}

func sortedKeys(m map[string]string) []string {
	keys := make([]string, 0, len(m))
	for k := range m {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	return keys
}

func orDash(s string) string {
	if s == "" {
		return "-"
	}
	return s
}

// --- structured output schemas ---

func responseFormat(name string, schema map[string]any) map[string]any {
	return map[string]any{
		"type": "json_schema",
		"json_schema": map[string]any{
			"name":   name,
			"strict": true,
			"schema": schema,
		},
	}
}

func actionItemSchema(types []agent.ActionType) map[string]any {
	typ := map[string]any{"type": "string", "description": "one of the allowed action types"}
	if len(types) > 0 {
		names := make([]string, 0, len(types))
		for _, t := range types {
			names = append(names, t.Type)
		}
		sort.Strings(names)
		typ["enum"] = names
	}
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"type", "params_json", "rationale", "risk"},
		"properties": map[string]any{
			"type":        typ,
			"params_json": map[string]any{"type": "string", "description": "the params object encoded as a JSON string, matching the action's params schema"},
			"rationale":   map[string]any{"type": "string"},
			"risk":        map[string]any{"type": "string", "enum": []string{"low", "medium", "high"}},
		},
	}
}

func investigationSchema(types []agent.ActionType) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"verdict", "summary", "root_cause", "confidence", "analysis", "proposed_actions"},
		"properties": map[string]any{
			"verdict":          map[string]any{"type": "string", "enum": []string{"actionable", "noise", "unknown"}},
			"summary":          map[string]any{"type": "string"},
			"root_cause":       map[string]any{"type": "string"},
			"confidence":       map[string]any{"type": "number"},
			"analysis":         map[string]any{"type": "string"},
			"proposed_actions": map[string]any{"type": "array", "items": actionItemSchema(types)},
		},
	}
}

func chatSchema(types []agent.ActionType) map[string]any {
	return map[string]any{
		"type":                 "object",
		"additionalProperties": false,
		"required":             []string{"reply", "proposed_actions"},
		"properties": map[string]any{
			"reply":            map[string]any{"type": "string"},
			"proposed_actions": map[string]any{"type": "array", "items": actionItemSchema(types)},
		},
	}
}
