package holmes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

var _ agent.Correlator = (*Client)(nil)

const correlateSystemPrompt = `You are infra-monitor's alert correlator. A new alert fired in a Kubernetes namespace where other incidents are
already open. Decide whether the new alert is a symptom of the SAME underlying problem as one of the open incidents
(e.g. an OOM kill, a crash loop and a replicas mismatch of the same workload), or a different problem.
Do NOT investigate and do NOT call any tools: decide only from what you are given (alert names, labels, descriptions,
the open incidents' analyses).
Say same only when the evidence points to one cause: same workload or a direct dependency, consistent timing, and
symptoms the known root cause explains. Different workloads with unrelated symptoms are different problems.
Answer ONLY with JSON: {"same_as": <incident id, or 0 if different from all>, "reason": "<one short sentence>"}`

// Correlate asks whether a new alert is the same problem as one of the open incidents (no tools).
func (c *Client) Correlate(ctx context.Context, req agent.CorrelationRequest) (*agent.CorrelationResult, error) {
	model := c.model(req.Alert.Model)
	ctx, span := telemetry.Tracer().Start(ctx, "holmes.correlate", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.Int("incident.id", req.Alert.IncidentID), attribute.Int("candidates", len(req.Candidates)), attribute.String("agent.model", model)))
	defer span.End()
	var b strings.Builder
	b.WriteString("Is this new alert the same problem as one of the open incidents below? (no tool calls)\n\n== New alert ==\n")
	b.WriteString(incidentContext(req.Alert))
	b.WriteString("\n== Open incidents in the same namespace ==\n")
	for _, cand := range req.Candidates {
		fmt.Fprintf(&b, "\nIncident #%d: %s (opened %s)\n", cand.IncidentID, cand.Title, cand.OpenedAt.UTC().Format(time.RFC3339))
		if cand.Summary != "" {
			fmt.Fprintf(&b, "Analysis: %s\n", truncate(cand.Summary, 800))
		}
		if cand.RootCause != "" {
			fmt.Fprintf(&b, "Root cause: %s\n", truncate(cand.RootCause, 600))
		}
		for i, s := range cand.Signals {
			if i >= 5 {
				break
			}
			fmt.Fprintf(&b, "- alert %s [%s] labels: %s\n", s.Name, s.Status, labelString(s.Labels))
			if d := firstAnnotation(s.Annotations); d != "" {
				fmt.Fprintf(&b, "  %s\n", truncate(d, 300))
			}
		}
	}
	creq := chatRequest{Ask: b.String(), Model: model, AdditionalSystemPrompt: correlateSystemPrompt}
	if c.cfg.StructuredOutput {
		creq.ResponseFormat = responseFormat("infra_monitor_correlation", map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"same_as", "reason"},
			"properties": map[string]any{
				"same_as": map[string]any{"type": "integer"},
				"reason":  map[string]any{"type": "string"},
			},
		})
	}
	resp, _, err := c.call(ctx, creq, nil)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	out, err := parseCorrelation(resp.Analysis)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	out.Model = model
	if resp.Metadata != nil {
		out.Usage = agent.Usage{PromptTokens: resp.Metadata.Usage.PromptTokens, CompletionTokens: resp.Metadata.Usage.CompletionTokens}
	}
	span.SetAttributes(attribute.Int("correlation.same_as", out.SameAs))
	return out, nil
}

func firstAnnotation(a map[string]string) string {
	for _, k := range []string{"description", "summary", "message"} {
		if v := a[k]; v != "" {
			return v
		}
	}
	return ""
}

// parseCorrelation reads {"same_as": int, "reason": string} from the whole text or the last fenced block.
func parseCorrelation(text string) (*agent.CorrelationResult, error) {
	cands := []string{strings.TrimSpace(text)}
	for _, m := range fenceRe.FindAllStringSubmatch(text, -1) {
		cands = append([]string{strings.TrimSpace(m[2])}, cands...)
	}
	for _, c := range cands {
		if i := strings.IndexByte(c, '{'); i >= 0 {
			var v struct {
				SameAs *int   `json:"same_as"`
				Reason string `json:"reason"`
			}
			if err := json.NewDecoder(strings.NewReader(c[i:])).Decode(&v); err == nil && v.SameAs != nil {
				return &agent.CorrelationResult{SameAs: *v.SameAs, Reason: strings.TrimSpace(v.Reason)}, nil
			}
		}
	}
	return nil, fmt.Errorf("holmes: correlation answer is not the expected JSON: %s", truncate(text, 200))
}
