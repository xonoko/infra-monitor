package incident

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"time"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/redact"
)

// prepare runs the executor's Preparer (dry-run, diff, rollback, precondition) if it has one.
func (s *Service) prepare(ctx context.Context, exec actions.Executor, params map[string]any, t actions.Target) (*actions.Prepared, error) {
	p, ok := exec.(actions.Preparer)
	if !ok {
		return nil, nil
	}
	pctx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	prep, err := p.Prepare(pctx, params, t)
	if err != nil {
		return nil, fmt.Errorf("%w: cannot prepare %s: %v", ErrInvalid, exec.Type(), err)
	}
	return prep, nil
}

func requiresApproval(exec actions.Executor) bool {
	if sv, ok := exec.(actions.Supervised); ok && sv.RequiresApproval() {
		return true
	}
	_, ok := exec.(actions.Preparer)
	return ok
}

func preparedMap(p *actions.Prepared) map[string]any {
	if p == nil {
		return nil
	}
	// What is stored and shown never contains secrets (the params used for execution are kept as is).
	r := *p
	r.Payload, r.DryRun, r.Effect, r.Rollback = redact.Text(p.Payload), redact.Text(p.DryRun), redact.Text(p.Effect), redact.Text(p.Rollback)
	p = &r
	b, _ := json.Marshal(p)
	var m map[string]any
	_ = json.Unmarshal(b, &m)
	return m
}

// PreparedOf decodes an action's stored proposal.
func PreparedOf(a *ent.Action) *actions.Prepared {
	if len(a.Prepared) == 0 {
		return nil
	}
	b, _ := json.Marshal(a.Prepared)
	var p actions.Prepared
	if json.Unmarshal(b, &p) != nil {
		return nil
	}
	return &p
}

// checkApprovalHash enforces precise approval: the approver must confirm the payload hash they saw.
func checkApprovalHash(a *ent.Action, hash string) error {
	if a.PayloadHash == "" {
		return nil
	}
	hash = strings.ToLower(strings.TrimSpace(hash))
	if len(hash) < 8 {
		return fmt.Errorf("%w: precise approval required: confirm the payload hash shown on the proposal", ErrInvalid)
	}
	if !strings.HasPrefix(a.PayloadHash, hash) {
		return fmt.Errorf("%w: the proposal changed since you reviewed it (payload hash mismatch); review it again", ErrConflict)
	}
	return nil
}

// preparedAudit is the timeline record of a precise proposal.
func preparedAudit(p *actions.Prepared, hash string) map[string]any {
	if p == nil {
		return nil
	}
	return map[string]any{
		"payload_hash": hash, "target": p.Target, "operation": p.Operation, "dry_run_ok": p.DryRunOK,
		"core_infrastructure": p.CoreInfrastructure, "argo_managed": p.ArgoManaged, "precondition": p.Precondition,
	}
}

// reprepare replaces a proposal whose target changed by a freshly prepared one (asks again).
func (s *Service) reprepare(ctx context.Context, old *ent.Action, reason string) (*ent.Action, error) {
	var out *ent.Action
	err := s.tx(ctx, func(tx *ent.Tx) error {
		a, inc, err := s.lockAction(ctx, tx, old.ID)
		if err != nil {
			return err
		}
		if a.Status == "proposed" {
			if err := s.expireAction(ctx, tx, inc, a, reason); err != nil {
				return err
			}
		}
		actor := domainSystemActor()
		out, err = s.createAction(ctx, tx, inc, actionInput{
			Type: a.Type, Params: a.Params, Rationale: a.Rationale, Risk: string(a.Risk),
			ProposedBy: string(a.ProposedBy), InvestigationID: a.InvestigationID,
		}, actor)
		if err != nil {
			return err
		}
		return tx.Action.UpdateOne(a).SetSupersededBy(out.ID).Exec(ctx)
	})
	return out, err
}

func domainSystemActor() domain.Actor { return domain.System }
