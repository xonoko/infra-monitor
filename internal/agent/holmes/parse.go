package holmes

import (
	"encoding/json"
	"regexp"
	"strconv"
	"strings"

	"github.com/xonoko/infra-monitor/internal/agent"
)

// structured is the union of the investigation and chat answer shapes.
type structured struct {
	Verdict         string      `json:"verdict"`
	Summary         string      `json:"summary"`
	RootCause       string      `json:"root_cause"`
	Confidence      any         `json:"confidence"`
	Analysis        string      `json:"analysis"`
	Reply           string      `json:"reply"`
	ProposedActions []rawAction `json:"proposed_actions"`
}

type rawAction struct {
	Type       string `json:"type"`
	Params     any    `json:"params"`
	ParamsJSON string `json:"params_json"`
	Rationale  string `json:"rationale"`
	Risk       string `json:"risk"`
}

var fenceRe = regexp.MustCompile("(?s)```[ \\t]*([a-zA-Z0-9_-]*)[ \\t]*\\r?\\n(.*?)```")

// extract finds structured JSON in text: the whole text, then the last fenced block,
// then a leading JSON object followed by trailing text. rest is text outside the JSON.
func extract(text string) (s structured, rest string, ok bool) {
	t := strings.TrimSpace(text)
	if t == "" {
		return s, "", false
	}
	if obj, ok := decodeObject(t); ok {
		return obj, "", true
	}
	matches := fenceRe.FindAllStringSubmatchIndex(t, -1)
	for i := len(matches) - 1; i >= 0; i-- {
		m := matches[i]
		lang := strings.ToLower(t[m[2]:m[3]])
		if lang != "" && lang != "json" {
			continue
		}
		body := strings.TrimSpace(t[m[4]:m[5]])
		if obj, ok := decodeObject(body); ok {
			rest = strings.TrimSpace(t[:m[0]] + t[m[1]:])
			return obj, rest, true
		}
	}
	return s, t, false
}

// decodeObject decodes the first JSON object in t (trailing text tolerated).
func decodeObject(t string) (structured, bool) {
	var s structured
	if !strings.HasPrefix(t, "{") {
		return s, false
	}
	dec := json.NewDecoder(strings.NewReader(t))
	var raw map[string]json.RawMessage
	if err := dec.Decode(&raw); err != nil {
		return s, false
	}
	b, _ := json.Marshal(raw)
	if err := json.Unmarshal(b, &s); err != nil {
		// tolerate wrong field types: decode field by field
		s = structured{}
		_ = json.Unmarshal(raw["verdict"], &s.Verdict)
		_ = json.Unmarshal(raw["summary"], &s.Summary)
		_ = json.Unmarshal(raw["root_cause"], &s.RootCause)
		_ = json.Unmarshal(raw["confidence"], &s.Confidence)
		_ = json.Unmarshal(raw["analysis"], &s.Analysis)
		_ = json.Unmarshal(raw["reply"], &s.Reply)
		_ = json.Unmarshal(raw["proposed_actions"], &s.ProposedActions)
	}
	known := false
	for _, k := range []string{"verdict", "summary", "root_cause", "analysis", "reply", "proposed_actions", "confidence"} {
		if _, ok := raw[k]; ok {
			known = true
			break
		}
	}
	return s, known
}

func parseInvestigation(text string) *agent.InvestigationResult {
	s, rest, ok := extract(text)
	if !ok {
		analysis := strings.TrimSpace(text)
		return &agent.InvestigationResult{Analysis: analysis, Summary: firstSentences(analysis, 300), Verdict: "unknown"}
	}
	res := &agent.InvestigationResult{
		Verdict:         normVerdict(s.Verdict),
		Summary:         strings.TrimSpace(s.Summary),
		RootCause:       strings.TrimSpace(s.RootCause),
		Confidence:      normConfidence(s.Confidence),
		Analysis:        strings.TrimSpace(s.Analysis),
		ProposedActions: normActions(s.ProposedActions),
	}
	if res.Analysis == "" {
		res.Analysis = rest
	}
	if res.Analysis == "" {
		res.Analysis = res.RootCause
	}
	if res.Analysis == "" {
		res.Analysis = res.Summary
	}
	if res.Summary == "" {
		src := res.RootCause
		if src == "" {
			src = res.Analysis
		}
		res.Summary = firstSentences(src, 300)
	}
	return res
}

func parseChat(text string) *agent.ChatResult {
	s, rest, ok := extract(text)
	if !ok {
		return &agent.ChatResult{Reply: strings.TrimSpace(text)}
	}
	reply := strings.TrimSpace(s.Reply)
	if reply == "" {
		reply = rest
	}
	if reply == "" {
		reply = strings.TrimSpace(s.Analysis)
	}
	return &agent.ChatResult{Reply: reply, ProposedActions: normActions(s.ProposedActions)}
}

func normVerdict(v string) string {
	switch strings.ToLower(strings.TrimSpace(strings.ReplaceAll(v, "-", "_"))) {
	case "actionable", "action", "action_required", "real", "true_positive", "critical":
		return "actionable"
	case "noise", "noisy", "false_positive", "benign", "ignore", "not_actionable", "transient":
		return "noise"
	default:
		return "unknown"
	}
}

func normConfidence(v any) float64 {
	var f float64
	switch x := v.(type) {
	case float64:
		f = x
	case string:
		s := strings.TrimSpace(x)
		pct := strings.HasSuffix(s, "%")
		s = strings.TrimSuffix(s, "%")
		n, err := strconv.ParseFloat(strings.TrimSpace(s), 64)
		if err != nil {
			switch strings.ToLower(s) {
			case "high":
				return 0.8
			case "medium":
				return 0.5
			case "low":
				return 0.2
			}
			return 0
		}
		f = n
		if pct {
			f /= 100
		}
	default:
		return 0
	}
	if f > 1 {
		f /= 100
	}
	if f < 0 {
		f = 0
	}
	if f > 1 {
		f = 1
	}
	return f
}

func normActions(in []rawAction) []agent.ProposedAction {
	var out []agent.ProposedAction
	for _, a := range in {
		typ := strings.TrimSpace(a.Type)
		if typ == "" {
			continue
		}
		params := map[string]any{}
		switch p := a.Params.(type) {
		case map[string]any:
			params = p
		case string:
			_ = json.Unmarshal([]byte(p), &params)
		}
		if len(params) == 0 && strings.TrimSpace(a.ParamsJSON) != "" {
			_ = json.Unmarshal([]byte(a.ParamsJSON), &params)
		}
		risk := strings.ToLower(strings.TrimSpace(a.Risk))
		if risk != "low" && risk != "medium" && risk != "high" {
			risk = "medium"
		}
		out = append(out, agent.ProposedAction{Type: typ, Params: params, Rationale: strings.TrimSpace(a.Rationale), Risk: risk})
	}
	return out
}

// firstSentences returns the leading sentences of plain text, up to max runes.
func firstSentences(text string, max int) string {
	t := strings.TrimSpace(text)
	// drop markdown headings and blank lines
	var lines []string
	for _, l := range strings.Split(t, "\n") {
		l = strings.TrimSpace(strings.TrimLeft(strings.TrimSpace(l), "#>*- "))
		if l != "" {
			lines = append(lines, l)
		}
	}
	t = strings.Join(lines, " ")
	if len([]rune(t)) <= max {
		return t
	}
	cut := string([]rune(t)[:max])
	if i := strings.LastIndexAny(cut, ".!?"); i > max/3 {
		return cut[:i+1]
	}
	return strings.TrimSpace(cut) + "…"
}
