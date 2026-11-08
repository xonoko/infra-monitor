// Package amsilence implements the alertmanager.silence action: it creates an
// Alertmanager silence for the incident's alerts (or explicit matchers), bounded in duration.
package amsilence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/source/alertmanager"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// ClientFunc returns the Alertmanager client of a source.
type ClientFunc func(source string) (*alertmanager.Client, bool)

type Executor struct {
	clients    ClientFunc
	maxSilence time.Duration
	now        func() time.Time
}

// New creates the executor. maxSilence bounds the duration (default 24h).
func New(clients ClientFunc, maxSilence time.Duration) *Executor {
	if maxSilence <= 0 {
		maxSilence = 24 * time.Hour
	}
	return &Executor{clients: clients, maxSilence: maxSilence, now: time.Now}
}

var _ actions.Executor = (*Executor)(nil)

// defaultLabels are used when no matchers are given.
var defaultLabels = []string{"alertname", "cluster", "namespace"}

func (*Executor) Type() string { return "alertmanager.silence" }
func (e *Executor) Description() string {
	return fmt.Sprintf("Create an Alertmanager silence so matching alerts stop notifying for a while (max %s). "+
		"Without matchers it silences the incident's alertname/cluster/namespace.", e.maxSilence)
}
func (*Executor) Risk() string { return "low" }
func (*Executor) Schema() json.RawMessage {
	return json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["duration"],
  "properties": {
    "duration": {"type": "string", "pattern": "^([0-9]+(\\.[0-9]+)?(ms|s|m|h))+$", "description": "Go duration, e.g. 30m or 2h"},
    "comment": {"type": "string", "maxLength": 500, "description": "Why the silence is needed"},
    "matchers": {
      "type": "array", "minItems": 1, "maxItems": 20,
      "description": "Label matchers; defaults to the incident's alertname, cluster and namespace",
      "items": {
        "type": "object", "additionalProperties": false, "required": ["name", "op", "value"],
        "properties": {
          "name": {"type": "string", "minLength": 1},
          "op": {"type": "string", "enum": ["=", "!=", "=~", "!~"]},
          "value": {"type": "string"}
        }
      }
    }
  }
}`)
}

type params struct {
	Duration time.Duration
	Comment  string
	Matchers []domain.Matcher
}

func (e *Executor) parse(p map[string]any, t actions.Target) (params, error) {
	var out params
	ds, _ := p["duration"].(string)
	d, err := time.ParseDuration(strings.TrimSpace(ds))
	if err != nil {
		return out, fmt.Errorf("invalid duration %q", ds)
	}
	if d <= 0 || d > e.maxSilence {
		return out, fmt.Errorf("duration must be between 1s and %s", e.maxSilence)
	}
	out.Duration = d
	out.Comment, _ = p["comment"].(string)
	if raw, ok := p["matchers"]; ok && raw != nil {
		b, _ := json.Marshal(raw)
		if err := json.Unmarshal(b, &out.Matchers); err != nil {
			return out, fmt.Errorf("invalid matchers: %w", err)
		}
		for i := range out.Matchers {
			if err := out.Matchers[i].Validate(); err != nil {
				return out, err
			}
		}
	} else {
		for _, k := range defaultLabels {
			if v := t.Labels[k]; v != "" {
				out.Matchers = append(out.Matchers, domain.Matcher{Name: k, Op: "=", Value: v})
			}
		}
	}
	if len(out.Matchers) == 0 {
		return out, errors.New("no matchers: pass matchers or use an incident with alertname/cluster/namespace labels")
	}
	hasEq := false
	for _, m := range out.Matchers {
		if (m.Op == "=" && m.Value != "") || (m.Op == "=~" && m.Value != "" && m.Value != ".*" && m.Value != ".+") {
			hasEq = true
		}
	}
	if !hasEq {
		return out, errors.New("matchers are too broad: at least one positive matcher with a specific value is required")
	}
	return out, nil
}

func (e *Executor) Validate(_ context.Context, p map[string]any, t actions.Target) error {
	if _, err := e.parse(p, t); err != nil {
		return err
	}
	if _, ok := e.clients(t.Source); !ok {
		return fmt.Errorf("source %q has no alertmanager_url configured for silences", t.Source)
	}
	return nil
}

func (e *Executor) Summary(p map[string]any, t actions.Target) string {
	ps, err := e.parse(p, t)
	if err != nil {
		ds, _ := p["duration"].(string)
		return "silence alerts for " + ds
	}
	ms := make([]string, len(ps.Matchers))
	for i, m := range ps.Matchers {
		ms[i] = m.Name + m.Op + m.Value
	}
	return fmt.Sprintf("silence {%s} for %s", strings.Join(ms, ", "), ps.Duration)
}

func (e *Executor) Execute(ctx context.Context, p map[string]any, t actions.Target) (string, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "alertmanager.silence")
	defer span.End()
	span.SetAttributes(attribute.String("source", t.Source), attribute.Int("incident.id", t.IncidentID))
	ps, err := e.parse(p, t)
	if err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	c, ok := e.clients(t.Source)
	if !ok {
		err := fmt.Errorf("source %q has no alertmanager_url configured for silences", t.Source)
		telemetry.RecordError(span, err)
		return "", err
	}
	comment := strings.TrimSpace(ps.Comment)
	if comment == "" {
		comment = "silenced from infra-monitor"
	}
	if t.IncidentID != 0 {
		comment = fmt.Sprintf("%s (incident #%d)", comment, t.IncidentID)
	}
	start := e.now()
	end := start.Add(ps.Duration)
	id, err := c.CreateSilence(ctx, ps.Matchers, start, end, "infra-monitor", comment)
	if err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	span.SetAttributes(attribute.String("silence.id", id))
	return fmt.Sprintf("silence %s until %s", id, end.UTC().Format(time.RFC3339)), nil
}
