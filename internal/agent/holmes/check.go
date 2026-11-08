package holmes

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

var _ agent.ActionChecker = (*Client)(nil)

const checkSystemPrompt = `You are infra-monitor's final reviewer. A remediation was proposed from a first analysis (by a smaller model) and approved
(by a human, or automatically by policy). It will run NOW, exactly as described.
Do NOT investigate again and do NOT call any tools: review only what you are given (the first analysis, the exact action
with its payload and dry-run, and a fresh snapshot of the current state gathered by infra-monitor).
Answer proceed=true ONLY if all of these hold:
1. The first analysis is sound: the evidence supports the root cause.
2. The action fixes that root cause: right cluster, namespace, object and container; sensible values; no obvious side effects.
3. It is still needed per the fresh snapshot (problem still present, the dry-run still shows a change).
4. There is no clearly better option (e.g. the object is managed by Argo CD and a live change would be reverted: a GitOps change is needed).
Otherwise answer proceed=false and say exactly what is wrong.
Answer ONLY with JSON: {"proceed": true|false, "reason": "<one or two sentences>"}`

// CheckAction asks Holmes whether an approved action is still needed, from a fresh context.
func (c *Client) CheckAction(ctx context.Context, ic agent.IncidentContext, action string) (*agent.ActionCheck, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "holmes.check_action", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.Int("incident.id", ic.IncidentID), attribute.String("agent.model", c.model(ic.Model))))
	defer span.End()
	req := chatRequest{
		Ask:                    "Review this approved action before it runs (no tool calls).\n\n" + action + "\n\nIncident:\n" + incidentContext(ic),
		Model:                  c.model(ic.Model),
		AdditionalSystemPrompt: checkSystemPrompt,
	}
	if c.cfg.StructuredOutput {
		req.ResponseFormat = responseFormat("infra_monitor_action_check", map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"proceed", "reason"},
			"properties": map[string]any{
				"proceed": map[string]any{"type": "boolean"},
				"reason":  map[string]any{"type": "string"},
			},
		})
	}
	resp, _, err := c.call(ctx, req, nil)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	out, err := parseCheck(resp.Analysis)
	if err != nil {
		telemetry.RecordError(span, err)
		return nil, err
	}
	out.Model = c.model(ic.Model)
	if resp.Metadata != nil {
		out.Usage = agent.Usage{PromptTokens: resp.Metadata.Usage.PromptTokens, CompletionTokens: resp.Metadata.Usage.CompletionTokens}
	}
	span.SetAttributes(attribute.Bool("agent.still_needed", out.StillNeeded))
	return out, nil
}

// parseCheck reads {"still_needed": bool, "reason": string} from the whole text or the last fenced block.
func parseCheck(text string) (*agent.ActionCheck, error) {
	cands := []string{strings.TrimSpace(text)}
	for _, m := range fenceRe.FindAllStringSubmatch(text, -1) {
		cands = append([]string{strings.TrimSpace(m[2])}, cands...)
	}
	for _, c := range cands {
		if i := strings.IndexByte(c, '{'); i >= 0 {
			var v struct {
				Proceed     *bool  `json:"proceed"`
				StillNeeded *bool  `json:"still_needed"`
				Reason      string `json:"reason"`
			}
			dec := json.NewDecoder(strings.NewReader(c[i:]))
			if dec.Decode(&v) == nil {
				if v.Proceed == nil {
					v.Proceed = v.StillNeeded
				}
				if v.Proceed != nil {
					return &agent.ActionCheck{StillNeeded: *v.Proceed, Reason: strings.TrimSpace(v.Reason)}, nil
				}
			}
		}
	}
	return nil, fmt.Errorf("holmes: unreadable action check answer")
}
