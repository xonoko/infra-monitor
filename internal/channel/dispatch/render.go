package dispatch

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/channel"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	incsvc "github.com/xonoko/infra-monitor/internal/incident"
)

// cardInput is everything the card shows.
type cardInput struct {
	inc          *ent.Incident
	firing       int
	signals      int
	pending      []*ent.Action
	agentEnabled bool
	url          string
	now          time.Time
}

func buildCard(in cardInput, caps channel.Capabilities) channel.Card {
	inc := in.inc
	c := channel.Card{
		IncidentID: inc.ID, Title: fmt.Sprintf("#%d %s", inc.ID, inc.Title),
		Status: string(inc.Status), Severity: inc.Severity, Verdict: string(inc.Verdict), Muted: inc.Muted,
		Summary: inc.Summary, URL: in.url,
	}
	add := func(name, val string) {
		if val != "" {
			c.Fields = append(c.Fields, channel.Field{Name: name, Value: val})
		}
	}
	add("Cluster", inc.Cluster)
	add("Namespace", inc.Namespace)
	add("Signals", fmt.Sprintf("%d firing / %d", in.firing, in.signals))
	add("Opened", inc.OpenedAt.UTC().Format("2006-01-02 15:04 UTC"))
	if inc.ResolvedAt != nil {
		add("Duration", humanDuration(inc.ResolvedAt.Sub(inc.OpenedAt)))
	}
	if a := inc.Edges.Assignee; a != nil {
		add("Assignee", firstNonEmpty(a.DisplayName, a.Email))
	}
	switch inc.InvestigationState {
	case incident.InvestigationStatePending, incident.InvestigationStateRunning:
		c.Notice = "AI investigation in progress…"
	case incident.InvestigationStateFailed:
		c.Notice = firstNonEmpty(inc.InvestigationNote, "AI investigation unavailable")
	case incident.InvestigationStateSkipped:
		c.Notice = "AI investigation skipped: " + firstNonEmpty(inc.InvestigationNote, "limits")
	case incident.InvestigationStateNone:
		if in.agentEnabled && !inc.Internal {
			c.Notice = "AI investigation unavailable"
		}
	case incident.InvestigationStateDone:
	}
	for _, a := range in.pending {
		c.PendingActions = append(c.PendingActions, fmt.Sprintf("#%d %s (%s risk)", a.ID, a.Summary, a.Risk))
	}
	active := inc.Status == incident.StatusOpen || inc.Status == incident.StatusAcknowledged
	if caps.Buttons && active {
		if inc.Status == incident.StatusOpen {
			c.Buttons = append(c.Buttons, channel.Button{Label: "👀 Ack", Action: &channel.ButtonAction{Op: "ack", IncidentID: inc.ID}})
		}
		if !inc.Muted {
			c.Buttons = append(c.Buttons, channel.Button{Label: "🔕 Mute 1h", Action: &channel.ButtonAction{Op: "mute", IncidentID: inc.ID, Arg: "1h"}})
		}
		if in.agentEnabled && inc.InvestigationState != incident.InvestigationStateRunning {
			c.Buttons = append(c.Buttons, channel.Button{Label: "🔁 Reinvestigate", Action: &channel.ButtonAction{Op: "reinvestigate", IncidentID: inc.ID}})
		}
		c.Buttons = append(c.Buttons, channel.Button{Label: "✅ Resolve", Action: &channel.ButtonAction{Op: "resolve", IncidentID: inc.ID}})
	}
	if caps.URLButtons && in.url != "" {
		c.Buttons = append(c.Buttons, channel.Button{Label: "Open in UI", URL: in.url})
	}
	return c
}

func cardHash(c channel.Card) string {
	b, _ := json.Marshal(c)
	h := sha256.Sum256(b)
	return hex.EncodeToString(h[:12])
}

func proposalMessage(a *ent.Action, caps channel.Capabilities, url string) channel.Message {
	var b strings.Builder
	fmt.Fprintf(&b, "**Proposed action #%d** (%s risk)\n`%s`\n", a.ID, a.Risk, a.Summary)
	if a.Rationale != "" {
		fmt.Fprintf(&b, "\n%s\n", a.Rationale)
	}
	if p := incsvc.PreparedOf(a); p != nil {
		writePrepared(&b, a, p)
	} else if params, err := json.Marshal(a.Params); err == nil && len(params) > 2 {
		fmt.Fprintf(&b, "\n```\n%s\n```", params)
	}
	m := channel.Message{Kind: channel.KindProposal, Author: proposerName(a), Body: b.String(), URL: url}
	switch a.Status {
	case "proposed":
		m.Footer = "Expires " + a.ExpiresAt.UTC().Format("15:04 UTC")
		if caps.Buttons {
			m.Buttons = []channel.Button{
				{Label: "✅ Approve", Style: channel.StylePrimary, Action: &channel.ButtonAction{Op: "approve", ActionID: a.ID, Arg: hashPrefix(a.PayloadHash)}},
				{Label: "✖ Reject", Style: channel.StyleDanger, Action: &channel.ButtonAction{Op: "reject", ActionID: a.ID}},
			}
		} else {
			m.Footer += fmt.Sprintf(" · reply /approve %d %s or /reject %d", a.ID, hashPrefix(a.PayloadHash), a.ID)
		}
	default:
		m.Footer = actionStatusLine(a)
	}
	return m
}

func proposerName(a *ent.Action) string {
	switch a.ProposedBy {
	case "agent":
		return "🤖 Agent"
	case "system":
		return "infra-monitor"
	default:
		return "User proposal"
	}
}

func actionStatusLine(a *ent.Action) string {
	who := ""
	if a.DecidedByName != "" {
		who = " by " + a.DecidedByName
		if a.DecidedVia != "" {
			who += " via " + a.DecidedVia
		}
	}
	switch a.Status {
	case "approved":
		return "✅ Approved" + who + " · queued"
	case "running":
		return "⏳ Running (approved" + who + ")"
	case "succeeded":
		return "✅ Done (approved" + who + ")"
	case "failed":
		return "❌ Failed (approved" + who + ")"
	case "rejected":
		return "✖ Rejected" + who
	case "expired":
		return "⌛ Expired"
	}
	return string(a.Status)
}

func humanDuration(d time.Duration) string {
	d = d.Round(time.Minute)
	if d < time.Minute {
		return "<1m"
	}
	h := int(d.Hours())
	m := int(d.Minutes()) % 60
	if h >= 24 {
		return fmt.Sprintf("%dd%dh", h/24, h%24)
	}
	if h > 0 {
		return fmt.Sprintf("%dh%02dm", h, m)
	}
	return fmt.Sprintf("%dm", m)
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}

func str(p map[string]any, k string) string {
	if v, ok := p[k]; ok && v != nil {
		if s, ok := v.(string); ok {
			return s
		}
		return fmt.Sprint(v)
	}
	return ""
}

func intVal(p map[string]any, k string) int {
	switch v := p[k].(type) {
	case float64:
		return int(v)
	case int:
		return v
	case json.Number:
		n, _ := v.Int64()
		return int(n)
	}
	return 0
}

func originLabel(ev *ent.TimelineEvent) string {
	name := firstNonEmpty(ev.ActorName, "someone")
	if ev.Channel != "" && ev.Channel != "system" {
		return name + " via " + ev.Channel
	}
	return name
}

func hashPrefix(h string) string {
	if len(h) > 8 {
		return h[:8]
	}
	return h
}

// payloadInlineMax is how much of a payload goes inline in a chat proposal; the rest is attached.
const payloadInlineMax = 1200

// writePrepared renders the precise proposal: target, operation, dry-run, effect, rollback, hash, payload.
func writePrepared(b *strings.Builder, a *ent.Action, p *actions.Prepared) {
	if p.CoreInfrastructure {
		b.WriteString("\n⚠️ **Core infrastructure**\n")
	}
	fmt.Fprintf(b, "\n**Target**: `%s`\n**Operation**: %s\n", p.Target, p.Operation)
	if p.ArgoManaged != "" {
		fmt.Fprintf(b, "⚠️ Managed by Argo CD app `%s`: a live change is reverted by selfHeal\n", p.ArgoManaged)
	}
	for _, w := range p.Warnings {
		fmt.Fprintf(b, "⚠️ %s\n", w)
	}
	if p.DryRunOK {
		b.WriteString("**Dry-run**: ✅ accepted by the server\n")
	} else {
		fmt.Fprintf(b, "**Dry-run**: ❌ failed, can't be approved\n```\n%s\n```\n", truncateText(p.DryRun, 400))
	}
	if p.Effect != "" {
		fmt.Fprintf(b, "**Effect**: %s\n", p.Effect)
	}
	if p.Rollback != "" {
		fmt.Fprintf(b, "**Rollback**: %s\n", p.Rollback)
	}
	fmt.Fprintf(b, "**Payload hash**: `%s`\n", hashPrefix(a.PayloadHash))
	payload := p.Payload
	if len(payload) > payloadInlineMax {
		payload = truncateText(payload, payloadInlineMax) + "\n… (full payload attached and in the UI)"
	}
	fmt.Fprintf(b, "\n```\n%s\n```", payload)
}

func truncateText(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}

// attachment is the full payload + dry-run as a file, for channels that support files.
func attachment(a *ent.Action, p *actions.Prepared) (string, []byte) {
	ext := map[string]string{"diff": "diff", "yaml": "yaml", "json": "json"}[p.PayloadFormat]
	if ext == "" {
		ext = "txt"
	}
	var b strings.Builder
	fmt.Fprintf(&b, "# infra-monitor action #%d (%s)\n# target: %s\n# operation: %s\n# payload hash: %s\n\n%s\n", a.ID, a.Type, p.Target, p.Operation, a.PayloadHash, p.Payload)
	if p.DryRun != "" && p.DryRun != p.Payload {
		fmt.Fprintf(&b, "\n# ---- dry-run result ----\n%s\n", p.DryRun)
	}
	if p.Rollback != "" {
		fmt.Fprintf(&b, "\n# ---- rollback ----\n# %s\n", p.Rollback)
	}
	return fmt.Sprintf("action-%d.%s", a.ID, ext), []byte(b.String())
}
