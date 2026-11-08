package incident

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/domain"
)

type route struct {
	name       string
	matchers   []domain.Matcher
	verdicts   []string
	severities []string
	channels   []string
	cont       bool
}

// compileRoutes parses routing rules. Without rules, the default is: notify every channel when
// the verdict is actionable, when the verdict is unknown (AI unavailable: fail open) for
// critical/warning alerts, or when severity is critical. Noise stays in the UI.
func compileRoutes(cfg *config.Config) ([]route, error) {
	var all []string
	for _, ch := range cfg.Channels {
		all = append(all, ch.Name)
	}
	if len(cfg.Routing) == 0 {
		if len(all) == 0 {
			return nil, nil
		}
		return []route{
			{name: "default-actionable", verdicts: []string{"actionable"}, channels: all},
			{name: "default-unknown", verdicts: []string{"unknown"}, severities: []string{"critical", "warning"}, channels: all},
			{name: "default-critical", severities: []string{"critical"}, channels: all},
		}, nil
	}
	out := make([]route, 0, len(cfg.Routing))
	for i, r := range cfg.Routing {
		ms, err := domain.ParseMatchers(r.Match)
		if err != nil {
			return nil, fmt.Errorf("routing[%d]: %w", i, err)
		}
		name := r.Name
		if name == "" {
			name = fmt.Sprintf("route-%d", i)
		}
		chs := r.Channels
		if len(chs) == 0 {
			chs = all
		}
		out = append(out, route{name: name, matchers: ms, verdicts: lower(r.Verdicts), severities: lower(r.Severities), channels: chs, cont: r.Continue})
	}
	return out, nil
}

func lower(ss []string) []string {
	out := make([]string, len(ss))
	for i, s := range ss {
		out[i] = strings.ToLower(s)
	}
	return out
}

// Route returns the channels an incident should be notified on and the matching rule names.
func (s *Service) Route(inc *ent.Incident) (channels, rules []string) {
	labels := incidentMatchLabels(inc)
	verdict := string(inc.Verdict)
	sev := strings.ToLower(inc.Severity)
	for _, r := range s.routes {
		if !domain.MatchAll(r.matchers, labels) {
			continue
		}
		if len(r.verdicts) > 0 && !slices.Contains(r.verdicts, verdict) {
			continue
		}
		if len(r.severities) > 0 && !slices.Contains(r.severities, sev) {
			continue
		}
		rules = append(rules, r.name)
		for _, c := range r.channels {
			if !slices.Contains(channels, c) {
				channels = append(channels, c)
			}
		}
		if !r.cont && len(s.cfg.Routing) > 0 {
			break
		}
	}
	return channels, rules
}

// decideNotification routes the incident to channels (once investigation finished, failed or timed out).
func (s *Service) decideNotification(ctx context.Context, tx *ent.Tx, inc *ent.Incident, reason string) (*ent.Incident, error) {
	if inc.Muted {
		return inc, nil
	}
	if inc.NotifiedAt == nil && inc.Status != incident.StatusOpen && inc.Status != incident.StatusAcknowledged {
		// resolved before we ever notified: flapping noise, keep it in the UI
		return inc, nil
	}
	channels, rules := s.Route(inc)
	var added []string
	for _, c := range channels {
		if !slices.Contains(inc.NotifyChannels, c) {
			added = append(added, c)
		}
	}
	if len(added) == 0 {
		return inc, nil
	}
	all := append(slices.Clone(inc.NotifyChannels), added...)
	upd := tx.Incident.UpdateOne(inc).SetNotifyChannels(all)
	if inc.NotifiedAt == nil {
		upd.SetNotifiedAt(s.now())
	}
	inc, err := upd.Save(ctx)
	if err != nil {
		return nil, err
	}
	for _, c := range added {
		s.m.routed.Add(ctx, 1, metric.WithAttributes(attribute.String("channel", c)))
	}
	_, err = s.addEvent(ctx, tx, inc, domain.EventNotificationRouted, domain.System, map[string]any{"channels": added, "rules": rules, "reason": reason})
	return inc, err
}

// failOpen notifies without waiting for the AI when the investigation is late, failed or disabled.
func (s *Service) failOpen(ctx context.Context, id int) error {
	return s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, id)
		if err != nil {
			return notFoundPermanent(err)
		}
		if inc.InvestigationState == incident.InvestigationStateDone {
			return nil // investigation already routed it
		}
		reason := "no agent configured"
		switch inc.InvestigationState {
		case incident.InvestigationStatePending, incident.InvestigationStateRunning:
			reason = "AI investigation timed out (fail open)"
		case incident.InvestigationStateFailed:
			reason = "AI investigation failed (fail open)"
		case incident.InvestigationStateSkipped:
			reason = "AI investigation skipped"
		case incident.InvestigationStateNone, incident.InvestigationStateDone:
		}
		if inc.Internal {
			reason = "internal incident"
		}
		_, err = s.decideNotification(ctx, tx, inc, reason)
		return err
	})
}
