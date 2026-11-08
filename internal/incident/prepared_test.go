package incident_test

import (
	"context"
	"encoding/json"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/incident"
)

// patchExec is a fake generic executor: the target's "resourceVersion" is rv.
type patchExec struct {
	rv   string
	runs int
}

func (*patchExec) Type() string        { return "k8s.patch" }
func (*patchExec) Description() string { return "patch" }
func (*patchExec) Risk() string        { return "medium" }
func (*patchExec) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","properties":{"name":{"type":"string"},"patch":{"type":"object"}}}`)
}
func (*patchExec) Validate(context.Context, map[string]any, actions.Target) error { return nil }
func (*patchExec) Summary(p map[string]any, _ actions.Target) string {
	return "patch " + p["name"].(string)
}
func (*patchExec) RequiresApproval() bool { return true }
func (e *patchExec) Execute(context.Context, map[string]any, actions.Target) (string, error) {
	return "", nil
}
func (e *patchExec) Prepare(_ context.Context, p map[string]any, t actions.Target) (*actions.Prepared, error) {
	return &actions.Prepared{Target: t.Namespace + "/" + p["name"].(string), Operation: "strategic-merge patch",
		Payload: "spec: {}", DryRun: "-a\n+b", DryRunOK: true, Risk: "high", Precondition: e.rv,
		CoreInfrastructure: t.Namespace == "kube-system"}, nil
}
func (e *patchExec) ExecutePrepared(_ context.Context, _ map[string]any, _ actions.Target, p *actions.Prepared) (string, error) {
	if p.Precondition != e.rv {
		return "", actions.ErrPreconditionChanged
	}
	e.runs++
	return "patched", nil
}

func preciseEnv(t *testing.T) (*env, *patchExec) {
	pe := &patchExec{rv: "100"}
	e := newEnv(t, false, func(c *config.Config) { c.Actions.AutoApprove = []string{"k8s.patch"} })
	require.NoError(t, e.svc.Registry().Register(pe))
	return e, pe
}

func TestPreciseApproval(t *testing.T) {
	e, pe := preciseEnv(t)
	ctx := context.Background()
	e.ingest(alert("OOM", "demo", "firing", "o"))
	e.drain()
	inc := e.onlyIncident()
	a, err := e.svc.ProposeAction(ctx, operator(), inc.ID, "k8s.patch", map[string]any{"name": "demo-api", "patch": map[string]any{}}, "raise limit")
	require.NoError(t, err)
	require.Equal(t, "proposed", string(a.Status), "generic actions are never auto-approved")
	require.True(t, a.RequiresApproval)
	require.Len(t, a.PayloadHash, 64)
	p := incident.PreparedOf(a)
	require.Equal(t, "demo/demo-api", p.Target)
	require.Equal(t, "high", string(a.Risk), "risk from the dry-run analysis")

	_, err = e.svc.ApproveWithHash(ctx, operator(), a.ID, "")
	require.ErrorIs(t, err, incident.ErrInvalid, "hash required")
	_, err = e.svc.ApproveWithHash(ctx, operator(), a.ID, "deadbeef")
	require.ErrorIs(t, err, incident.ErrConflict, "wrong hash")
	_, err = e.svc.ApproveWithHash(ctx, operator(), a.ID, a.PayloadHash[:12])
	require.NoError(t, err)
	e.drain()
	a, _ = e.svc.GetAction(ctx, a.ID)
	require.Equal(t, "succeeded", string(a.Status))
	require.Equal(t, 1, pe.runs)
}

func TestPreciseApprovalTargetChanged(t *testing.T) {
	e, pe := preciseEnv(t)
	ctx := context.Background()
	e.ingest(alert("OOM", "demo", "firing", "o"))
	e.drain()
	inc := e.onlyIncident()
	a, err := e.svc.ProposeAction(ctx, operator(), inc.ID, "k8s.patch", map[string]any{"name": "x", "patch": map[string]any{}}, "")
	require.NoError(t, err)

	// changed before approval: refused, re-prepared as a new proposal
	pe.rv = "101"
	_, err = e.svc.ApproveWithHash(ctx, operator(), a.ID, a.PayloadHash[:8])
	require.ErrorIs(t, err, incident.ErrConflict)
	old, _ := e.svc.GetAction(ctx, a.ID)
	require.Equal(t, "expired", string(old.Status))
	require.NotNil(t, old.SupersededBy)
	fresh, _ := e.svc.GetAction(ctx, *old.SupersededBy)
	require.Equal(t, "101", incident.PreparedOf(fresh).Precondition)
	require.NotEqual(t, a.PayloadHash, fresh.PayloadHash)

	// changed after approval, before execution: refused at execution, asked again
	_, err = e.svc.ApproveWithHash(ctx, operator(), fresh.ID, fresh.PayloadHash[:8])
	require.NoError(t, err)
	pe.rv = "102"
	e.drain()
	fresh, _ = e.svc.GetAction(ctx, fresh.ID)
	require.Equal(t, "failed", string(fresh.Status))
	require.Contains(t, fresh.Error, "target changed")
	require.NotNil(t, fresh.SupersededBy)
	again, _ := e.svc.GetAction(ctx, *fresh.SupersededBy)
	require.Equal(t, "proposed", string(again.Status))
	require.Zero(t, pe.runs)
}

func (f *fakeAgent) CheckAction(_ context.Context, _ agent.IncidentContext, desc string) (*agent.ActionCheck, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.checks = append(f.checks, desc)
	return &agent.ActionCheck{StillNeeded: f.stillNeeded, Reason: "observed the pod", Model: "fake-model"}, nil
}

func TestPreExecutionCheck(t *testing.T) {
	for _, still := range []bool{true, false} {
		e := newEnv(t, true)
		e.agent.result.ProposedActions = nil
		e.agent.stillNeeded = still
		ctx := context.Background()
		e.ingest(alert("OOM", "demo", "firing", "o"))
		e.drain()
		inc := e.onlyIncident()
		a, err := e.svc.ProposeAction(ctx, operator(), inc.ID, "test.echo", map[string]any{"msg": "fix"}, "")
		require.NoError(t, err)
		e.clock.Add(30 * time.Minute) // approved half an hour later
		_, err = e.svc.Approve(ctx, operator(), a.ID)
		require.NoError(t, err)
		e.drain()
		a, _ = e.svc.GetAction(ctx, a.ID)
		require.Len(t, e.agent.checks, 1, "the agent re-checks before running")
		require.Contains(t, e.agent.checks[0], "test.echo")
		if still {
			require.Equal(t, "succeeded", string(a.Status))
			require.Equal(t, 1, e.exec.runs)
		} else {
			require.Equal(t, "expired", string(a.Status))
			require.Contains(t, a.Error, "cancelled before running")
			require.Zero(t, e.exec.runs)
		}
		c, err := e.svc.Cost(ctx, inc.ID, time.Time{})
		require.NoError(t, err)
		require.Equal(t, 1, c.Remediation.Calls, "the check counts as remediation")
	}
}
