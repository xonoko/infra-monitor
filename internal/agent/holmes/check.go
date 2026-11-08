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

const checkSystemPrompt = `You are infra-monitor's pre-execution safety check. A human approved a remediation earlier; it is about to run NOW.
Use your read-only tools to look at the CURRENT state of the target and the incident (pods, events, logs, the object itself).
Decide whether the action is still needed and still safe right now. Say not needed when the problem is already gone, the
object already has the intended state, the target no longer exists, or the situation changed so the action would be wrong.
When in doubt about safety, say not needed and explain. Be brief and cheap: a few targeted tool calls.
Answer ONLY with JSON: {"still_needed": true|false, "reason": "<one or two sentences citing what you observed>"}`

// CheckAction asks Holmes whether an approved action is still needed, from a fresh context.
func (c *Client) CheckAction(ctx context.Context, ic agent.IncidentContext, action string) (*agent.ActionCheck, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "holmes.check_action", trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(
		attribute.Int("incident.id", ic.IncidentID), attribute.String("agent.model", c.model(ic.Model))))
	defer span.End()
	req := chatRequest{
		Ask:                    "Approved action about to run:\n" + action + "\n\nIncident context:\n" + incidentContext(ic),
		Model:                  c.model(ic.Model),
		AdditionalSystemPrompt: checkSystemPrompt,
	}
	if c.cfg.StructuredOutput {
		req.ResponseFormat = responseFormat("infra_monitor_action_check", map[string]any{
			"type": "object", "additionalProperties": false, "required": []string{"still_needed", "reason"},
			"properties": map[string]any{
				"still_needed": map[string]any{"type": "boolean"},
				"reason":       map[string]any{"type": "string"},
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
				StillNeeded *bool  `json:"still_needed"`
				Reason      string `json:"reason"`
			}
			dec := json.NewDecoder(strings.NewReader(c[i:]))
			if dec.Decode(&v) == nil && v.StillNeeded != nil {
				return &agent.ActionCheck{StillNeeded: *v.StillNeeded, Reason: strings.TrimSpace(v.Reason)}, nil
			}
		}
	}
	return nil, fmt.Errorf("holmes: unreadable action check answer")
}
