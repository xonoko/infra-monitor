package incident

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/google/uuid"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/metric"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/action"
	"github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/redact"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

type actionInput struct {
	Type            string
	Params          map[string]any
	Rationale       string
	Risk            string
	ProposedBy      string // agent | user | system
	InvestigationID *int
	// DropIfDryRunFails: agent proposals that fail the server dry-run are not created (never shown to humans).
	DropIfDryRunFails bool
}

// Target derives the action scope from an incident.
func Target(inc *ent.Incident) actions.Target {
	return actions.Target{IncidentID: inc.ID, Cluster: inc.Cluster, Namespace: inc.Namespace, Labels: incidentMatchLabels(inc), Source: inc.Source}
}

func paramsHash(typ string, p map[string]any) string {
	b, _ := json.Marshal(p) // map keys are sorted by encoding/json
	h := sha256.Sum256(append([]byte(typ+"\x00"), b...))
	return hex.EncodeToString(h[:8])
}

// createAction validates and stores a proposal; a pending identical proposal is returned instead of duplicated.
func (s *Service) createAction(ctx context.Context, tx *ent.Tx, inc *ent.Incident, in actionInput, actor domain.Actor) (*ent.Action, error) {
	if in.Params == nil {
		in.Params = map[string]any{}
	}
	exec, ok := s.registry.Get(in.Type)
	if !ok {
		return nil, fmt.Errorf("%w: %w: %q", ErrInvalid, actions.ErrUnknownType, in.Type)
	}
	t := Target(inc)
	if err := s.registry.Validate(ctx, in.Type, in.Params, t); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	hash := paramsHash(in.Type, in.Params)
	pending, err := tx.Action.Query().Where(action.IncidentID(inc.ID), action.TypeEQ(in.Type),
		action.StatusIn(action.StatusProposed, action.StatusApproved, action.StatusRunning)).All(ctx)
	if err != nil {
		return nil, err
	}
	for _, a := range pending {
		if paramsHash(a.Type, a.Params) == hash {
			return a, nil
		}
	}
	risk := in.Risk
	switch risk {
	case "low", "medium", "high":
	default:
		risk = exec.Risk()
	}
	// The registry's default risk is a floor: the agent can't downgrade it.
	if riskRank(exec.Risk()) > riskRank(risk) {
		risk = exec.Risk()
	}
	supervised := requiresApproval(exec)
	prep, err := s.prepare(ctx, exec, in.Params, t)
	if err != nil {
		return nil, err
	}
	if prep != nil && !prep.DryRunOK && in.DropIfDryRunFails {
		return nil, fmt.Errorf("%w: server dry-run failed: %s", ErrInvalid, truncate(redact.Text(prep.DryRun), 1500))
	}
	// With GitOps available, a live change to an Argo-managed object would be reverted: send it back to the agent.
	if prep != nil && prep.ArgoManaged != "" && in.DropIfDryRunFails && strings.HasPrefix(in.Type, "k8s.") {
		if _, ok := s.registry.Get("gitops.change"); ok {
			return nil, fmt.Errorf("%w: %s is managed by Argo CD app %q (selfHeal reverts live changes): propose gitops.change on the file in the GitOps repo that sets this value instead (find it from the Argo CD Application source: repo path and helm valueFiles)", ErrInvalid, prep.Target, prep.ArgoManaged)
		}
	}
	payloadHash := ""
	summary := exec.Summary(in.Params, t)
	if prep != nil {
		if riskRank(prep.Risk) > riskRank(risk) {
			risk = prep.Risk
		}
		payloadHash = actions.PayloadHash(in.Type, in.Params, prep.Precondition)
		if prep.CoreInfrastructure {
			summary = "[core infrastructure] " + summary
		}
	}
	proposedBy := action.ProposedBy(in.ProposedBy)
	a, err := tx.Action.Create().
		SetIncidentID(inc.ID).SetType(in.Type).SetParams(in.Params).SetRationale(truncate(in.Rationale, 4000)).
		SetRisk(action.Risk(risk)).SetProposedBy(proposedBy).SetNillableProposerID(actor.UserIDPtr()).
		SetNillableInvestigationID(in.InvestigationID).
		SetExpiresAt(s.now().Add(s.cfg.Actions.ProposalTTL)).
		SetIdempotencyKey(uuid.NewString()).
		SetSummary(truncate(summary, 300)).
		SetPrepared(preparedMap(prep)).SetPayloadHash(payloadHash).SetRequiresApproval(supervised).
		Save(ctx)
	if err != nil {
		return nil, err
	}
	s.m.actions.Add(ctx, 1, metric.WithAttributes(attribute.String("type", a.Type), attribute.String("status", "proposed")))
	if _, err := s.addEvent(ctx, tx, inc, domain.EventActionProposed, actor, actionEventPayload(a, preparedAudit(prep, payloadHash))); err != nil {
		return nil, err
	}
	// A proposal needs a human decision: make sure it reaches the chat channels (unless muted).
	if a.Status == action.StatusProposed && len(inc.NotifyChannels) == 0 && !inc.Muted && len(s.cfg.Channels) > 0 &&
		(inc.Status == incident.StatusOpen || inc.Status == incident.StatusAcknowledged) {
		all := make([]string, 0, len(s.cfg.Channels))
		for _, c := range s.cfg.Channels {
			all = append(all, c.Name)
		}
		upd := tx.Incident.UpdateOne(inc).SetNotifyChannels(all)
		if inc.NotifiedAt == nil {
			upd.SetNotifiedAt(s.now())
		}
		if inc, err = upd.Save(ctx); err != nil {
			return nil, err
		}
		if _, err := s.addEvent(ctx, tx, inc, domain.EventNotificationRouted, domain.System, map[string]any{
			"channels": all, "reason": "action proposed: needs a decision",
		}); err != nil {
			return nil, err
		}
	}
	// Generic and gitops actions are never auto-approved, whatever auto_approve says.
	if pol := s.autoPolicy(ctx, inc); !supervised && pol.allows(a.Type) {
		// Loop guard: at most maxAutoPerHour automatic executions per incident, then a human decides.
		auto, err := tx.Action.Query().Where(action.IncidentID(inc.ID), action.DecidedByNameEQ(domain.System.Name),
			action.DecidedAtGT(s.now().Add(-time.Hour))).Count(ctx)
		if err != nil {
			return nil, err
		}
		if auto < pol.maxAutoPerHour {
			if a, err = s.approveLocked(ctx, tx, inc, a, domain.System, "auto-approved ("+pol.source+")"); err != nil {
				return nil, err
			}
		} else {
			s.log.InfoContext(ctx, "auto-approve limit reached, waiting for a human", "incident_id", inc.ID, "action_id", a.ID)
		}
	}
	return a, nil
}

func riskRank(r string) int {
	switch r {
	case "low":
		return 1
	case "medium":
		return 2
	case "high":
		return 3
	}
	return 0
}

func actionEventPayload(a *ent.Action, extra map[string]any) map[string]any {
	p := map[string]any{"action_id": a.ID, "type": a.Type, "summary": a.Summary, "risk": string(a.Risk), "status": string(a.Status)}
	for k, v := range extra {
		p[k] = v
	}
	return p
}

// ProposeAction lets a user propose an action (it still needs approval).
func (s *Service) ProposeAction(ctx context.Context, actor domain.Actor, incidentID int, typ string, params map[string]any, rationale string) (*ent.Action, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	var out *ent.Action
	err := s.tx(ctx, func(tx *ent.Tx) error {
		inc, err := s.lockIncident(ctx, tx, incidentID)
		if err != nil {
			return err
		}
		out, err = s.createAction(ctx, tx, inc, actionInput{Type: typ, Params: params, Rationale: rationale, ProposedBy: "user"}, actor)
		return err
	})
	return out, err
}

func (s *Service) lockAction(ctx context.Context, tx *ent.Tx, id int) (*ent.Action, *ent.Incident, error) {
	a, err := tx.Action.Query().Where(action.ID(id)).ForUpdate().Only(ctx)
	if err != nil {
		return nil, nil, notFound(err, fmt.Sprintf("action %d", id))
	}
	inc, err := s.lockIncident(ctx, tx, a.IncidentID)
	return a, inc, err
}

// Approve approves a proposal and queues its execution.
func (s *Service) Approve(ctx context.Context, actor domain.Actor, actionID int) (*ent.Action, error) {
	return s.ApproveWithHash(ctx, actor, actionID, "")
}

// ApproveWithHash approves a proposal. Precise proposals (generic / gitops) require the payload hash the
// approver saw; the target is re-checked and, if it changed, the proposal is re-prepared for a new review.
func (s *Service) ApproveWithHash(ctx context.Context, actor domain.Actor, actionID int, hash string) (*ent.Action, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	var out *ent.Action
	var supersede *ent.Action
	err := s.tx(ctx, func(tx *ent.Tx) error {
		a, inc, err := s.lockAction(ctx, tx, actionID)
		if err != nil {
			return err
		}
		if err := checkApprovalHash(a, hash); err != nil {
			return err
		}
		if p := PreparedOf(a); p != nil {
			if !p.DryRunOK {
				return fmt.Errorf("%w: the dry-run failed for this proposal; it can't be approved", ErrConflict)
			}
			if a.Status == action.StatusProposed {
				if exec, ok := s.registry.Get(a.Type); ok {
					fresh, perr := s.prepare(ctx, exec, a.Params, Target(inc))
					if perr != nil {
						return perr
					}
					if fresh != nil && fresh.Precondition != p.Precondition {
						supersede = a
						return nil
					}
				}
			}
		}
		out, err = s.approveLocked(ctx, tx, inc, a, actor, "")
		return err
	})
	if err != nil {
		return nil, err
	}
	if supersede != nil {
		n, err := s.reprepare(ctx, supersede, "target changed before approval")
		if err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: the target changed since this proposal was prepared; review the updated proposal #%d", ErrConflict, n.ID)
	}
	return out, nil
}

func (s *Service) approveLocked(ctx context.Context, tx *ent.Tx, inc *ent.Incident, a *ent.Action, actor domain.Actor, note string) (*ent.Action, error) {
	if a.Status != action.StatusProposed {
		return nil, fmt.Errorf("%w: action #%d is %s", ErrConflict, a.ID, a.Status)
	}
	if !a.ExpiresAt.After(s.now()) {
		if err := s.expireAction(ctx, tx, inc, a, "expired before approval"); err != nil {
			return nil, err
		}
		return nil, fmt.Errorf("%w: action #%d expired", ErrConflict, a.ID)
	}
	// Re-validate: policy or registry may have changed since the proposal.
	if err := s.registry.Validate(ctx, a.Type, a.Params, Target(inc)); err != nil {
		return nil, fmt.Errorf("%w: %v", ErrInvalid, err)
	}
	a, err := tx.Action.UpdateOne(a).SetStatus(action.StatusApproved).SetNillableDecidedByID(actor.UserIDPtr()).
		SetDecidedByName(actor.Name).SetDecidedVia(actor.Channel).SetDecidedAt(s.now()).Save(ctx)
	if err != nil {
		return nil, err
	}
	s.m.actions.Add(ctx, 1, metric.WithAttributes(attribute.String("type", a.Type), attribute.String("status", "approved")))
	extra := map[string]any{"decided_by": actor.Name, "decided_via": actor.Channel}
	if note != "" {
		extra["note"] = note
	}
	if _, err := s.addEvent(ctx, tx, inc, domain.EventActionApproved, actor, actionEventPayload(a, extra)); err != nil {
		return nil, err
	}
	return a, enqueue(ctx, tx, JobExecuteAction, actionPayload{ActionID: a.ID}, queue.Opts{UniqueKey: fmt.Sprintf("exec:%d", a.ID), MaxAttempts: 1})
}

// Reject rejects a proposal.
func (s *Service) Reject(ctx context.Context, actor domain.Actor, actionID int, reason string) (*ent.Action, error) {
	if err := require(actor, domain.RoleOperator); err != nil {
		return nil, err
	}
	var out *ent.Action
	err := s.tx(ctx, func(tx *ent.Tx) error {
		a, inc, err := s.lockAction(ctx, tx, actionID)
		if err != nil {
			return err
		}
		if a.Status != action.StatusProposed {
			return fmt.Errorf("%w: action #%d is %s", ErrConflict, a.ID, a.Status)
		}
		if out, err = tx.Action.UpdateOne(a).SetStatus(action.StatusRejected).SetNillableDecidedByID(actor.UserIDPtr()).
			SetDecidedByName(actor.Name).SetDecidedVia(actor.Channel).SetDecidedAt(s.now()).SetError(reason).Save(ctx); err != nil {
			return err
		}
		s.m.actions.Add(ctx, 1, metric.WithAttributes(attribute.String("type", a.Type), attribute.String("status", "rejected")))
		_, err = s.addEvent(ctx, tx, inc, domain.EventActionRejected, actor, actionEventPayload(out, map[string]any{
			"decided_by": actor.Name, "decided_via": actor.Channel, "reason": reason,
		}))
		return err
	})
	return out, err
}

func (s *Service) expireAction(ctx context.Context, tx *ent.Tx, inc *ent.Incident, a *ent.Action, reason string) error {
	a, err := tx.Action.UpdateOne(a).SetStatus(action.StatusExpired).SetError(reason).Save(ctx)
	if err != nil {
		return err
	}
	_, err = s.addEvent(ctx, tx, inc, domain.EventActionExpired, domain.System, actionEventPayload(a, map[string]any{"reason": reason}))
	return err
}

// ExpireProposals expires proposals past their TTL and fails actions whose run was lost (scheduler).
func (s *Service) ExpireProposals(ctx context.Context) error {
	now := s.now()
	ids, err := s.ent.Action.Query().Where(action.StatusEQ(action.StatusProposed), action.ExpiresAtLT(now)).IDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range ids {
		if err := s.tx(ctx, func(tx *ent.Tx) error {
			a, inc, err := s.lockAction(ctx, tx, id)
			if err != nil || a.Status != action.StatusProposed {
				return err
			}
			return s.expireAction(ctx, tx, inc, a, "proposal expired")
		}); err != nil {
			return err
		}
	}
	lost, err := s.ent.Action.Query().Where(action.StatusEQ(action.StatusRunning),
		action.StartedAtLT(now.Add(-s.cfg.Actions.Timeout-5*time.Minute))).IDs(ctx)
	if err != nil {
		return err
	}
	for _, id := range lost {
		if err := s.finishAction(ctx, id, "", errors.New("execution lost (worker stopped)")); err != nil {
			return err
		}
	}
	return nil
}

// executeAction runs an approved action exactly once (job with a single attempt).
func (s *Service) executeAction(ctx context.Context, id int) error {
	if canceled, err := s.preExecutionCheck(ctx, id); err != nil || canceled {
		return err
	}
	var a *ent.Action
	var inc *ent.Incident
	err := s.tx(ctx, func(tx *ent.Tx) error {
		var err error
		a, inc, err = s.lockAction(ctx, tx, id)
		if err != nil {
			return err
		}
		if a.Status != action.StatusApproved {
			a = nil
			return nil
		}
		if a, err = tx.Action.UpdateOne(a).SetStatus(action.StatusRunning).SetStartedAt(s.now()).Save(ctx); err != nil {
			return err
		}
		_, err = s.addEvent(ctx, tx, inc, domain.EventActionStarted, domain.System, actionEventPayload(a, nil))
		return err
	})
	if err != nil || a == nil {
		return err
	}
	exec, ok := s.registry.Get(a.Type)
	var out string
	var rerr error
	if !ok {
		rerr = fmt.Errorf("action type %q is no longer enabled", a.Type)
	} else {
		ectx, cancel := context.WithTimeout(ctx, s.cfg.Actions.Timeout)
		ectx, span := telemetry.Tracer().Start(ectx, "action.execute")
		span.SetAttributes(attribute.String("action.type", a.Type), attribute.Int("action.id", a.ID), attribute.Int("incident.id", a.IncidentID))
		if pe, ok := exec.(actions.PreconditionExecutor); ok && PreparedOf(a) != nil {
			out, rerr = pe.ExecutePrepared(ectx, a.Params, Target(inc), PreparedOf(a))
		} else if requiresApproval(exec) {
			rerr = errors.New("this action needs a prepared proposal")
		} else {
			out, rerr = exec.Execute(ectx, a.Params, Target(inc))
		}
		telemetry.RecordError(span, rerr)
		span.End()
		cancel()
	}
	if errors.Is(rerr, actions.ErrPreconditionChanged) {
		ctx = context.WithoutCancel(ctx)
		if err := s.finishAction(ctx, a.ID, out, fmt.Errorf("refused: %w; a fresh proposal needs your approval", rerr)); err != nil {
			return err
		}
		_, err := s.reprepare(ctx, a, "target changed after approval")
		return err
	}
	return s.finishAction(context.WithoutCancel(ctx), a.ID, out, rerr)
}

func (s *Service) finishAction(ctx context.Context, id int, out string, rerr error) error {
	return s.tx(ctx, func(tx *ent.Tx) error {
		a, inc, err := s.lockAction(ctx, tx, id)
		if err != nil {
			return err
		}
		out = redact.Text(out)
		u := tx.Action.UpdateOne(a).SetFinishedAt(s.now()).SetOutput(truncate(out, 16000))
		typ := domain.EventActionSucceeded
		status := action.StatusSucceeded
		if rerr != nil {
			typ, status = domain.EventActionFailed, action.StatusFailed
			u.SetError(truncate(redact.Text(rerr.Error()), 4000))
		}
		if a, err = u.SetStatus(status).Save(ctx); err != nil {
			return err
		}
		s.m.actions.Add(ctx, 1, metric.WithAttributes(attribute.String("type", a.Type), attribute.String("status", string(status))))
		extra := map[string]any{"output": truncate(out, 1500)}
		if rerr != nil {
			extra["error"] = truncate(redact.Text(rerr.Error()), 1000)
		}
		_, err = s.addEvent(ctx, tx, inc, typ, domain.System, actionEventPayload(a, extra))
		return err
	})
}

// ReinvestigateExecutor is the "agent.reinvestigate" action.
type ReinvestigateExecutor struct{ Svc *Service }

func (ReinvestigateExecutor) Type() string { return "agent.reinvestigate" }
func (ReinvestigateExecutor) Description() string {
	return "Run a new AI investigation on this incident (e.g. after a remediation, to check the result)."
}
func (ReinvestigateExecutor) Risk() string { return "low" }
func (ReinvestigateExecutor) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","additionalProperties":false,"properties":{"reason":{"type":"string","description":"Why a new investigation is useful"}}}`)
}
func (ReinvestigateExecutor) Validate(context.Context, map[string]any, actions.Target) error {
	return nil
}
func (ReinvestigateExecutor) Summary(map[string]any, actions.Target) string {
	return "re-run the AI investigation"
}
func (e ReinvestigateExecutor) Execute(ctx context.Context, _ map[string]any, t actions.Target) (string, error) {
	err := e.Svc.tx(ctx, func(tx *ent.Tx) error {
		return enqueue(ctx, tx, JobInvestigate, incidentPayload{IncidentID: t.IncidentID, Trigger: "action"},
			queue.Opts{UniqueKey: fmt.Sprintf("investigate:%d", t.IncidentID), MaxAttempts: 3})
	})
	if err != nil {
		return "", err
	}
	return "investigation queued", nil
}
