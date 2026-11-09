package incident_test

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	entincident "github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/incident"
)

func TestSeverityGate(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Agent.InvestigateSeverities = []string{"critical", "warning"} })
	sig := alert("TargetDown", "monitoring", "firing", "i")
	sig.Labels["severity"] = "info"
	e.ingest(sig)
	e.drain()
	inc := e.onlyIncident()
	require.Equal(t, 0, e.agent.calls)
	require.Equal(t, entincident.InvestigationStateSkipped, inc.InvestigationState)
	require.Contains(t, inc.InvestigationNote, "severity")
	require.Empty(t, inc.NotifyChannels, "info + unknown verdict stays in the UI")
}

func TestAlertStormInvestigatesOnce(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) {
		c.Agent.Storm = config.StormConfig{Enabled: true, Threshold: 2, Window: 10 * time.Minute}
	})
	ctx := context.Background()
	for _, ns := range []string{"a", "b", "c", "d", "e"} {
		e.ingest(alert("KubePodCrashLooping", ns, "firing", "fp-"+ns))
	}
	e.drain()
	require.Equal(t, 1, e.agent.calls, "one investigation for the whole storm")
	incs, err := e.db.Ent.Incident.Query().Order(ent.Asc("id")).All(ctx)
	require.NoError(t, err)
	require.Len(t, incs, 6)
	var storm *ent.Incident
	for _, i := range incs {
		if incident.IsStorm(i) {
			storm = i
		}
	}
	require.NotNil(t, storm)
	require.Equal(t, "Alert storm: 5 incidents grouped", storm.Title)
	require.Equal(t, entincident.InvestigationStateDone, storm.InvestigationState)
	require.Equal(t, []string{"tg"}, storm.NotifyChannels, "only the storm is notified")
	for _, i := range incs {
		if i.ID == storm.ID {
			continue
		}
		require.NotNil(t, i.StormID)
		require.Equal(t, storm.ID, *i.StormID)
		require.Empty(t, i.NotifyChannels)
		require.Contains(t, i.InvestigationNote, "alert storm")
	}

	// all members resolve -> storm resolves
	for _, ns := range []string{"a", "b", "c", "d", "e"} {
		e.ingest(alert("KubePodCrashLooping", ns, "resolved", "fp-"+ns))
	}
	e.drain()
	require.NoError(t, e.svc.ResolveStorms(ctx))
	storm, _ = e.svc.Get(ctx, storm.ID)
	require.Equal(t, entincident.StatusResolved, storm.Status)
}

func TestHourlyCap(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Agent.MaxPerHour = 1 })
	e.ingest(alert("A", "x", "firing", "1"))
	e.drain()
	e.ingest(alert("B", "y", "firing", "2"))
	e.drain()
	require.Equal(t, 1, e.agent.calls)
	incs, _ := e.db.Ent.Incident.Query().Order(ent.Desc("id")).All(context.Background())
	require.Contains(t, incs[0].InvestigationNote, "hourly AI cap")
	require.Equal(t, []string{"tg"}, incs[0].NotifyChannels, "capped critical alert still fails open")
}

func TestBudgetErrorIsFinalAndPauses(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Agent.BudgetPause = time.Hour })
	ctx := context.Background()
	e.agent.err = errors.New("holmes: HTTP 400: litellm.BudgetExceededError: budget_exceeded")
	e.ingest(alert("A", "x", "firing", "1"))
	e.drain()
	inc := e.onlyIncident()
	require.Equal(t, "AI budget reached", inc.InvestigationNote)
	require.Equal(t, []string{"tg"}, inc.NotifyChannels, "fails open")
	require.Equal(t, 1, e.agent.calls, "no retry loop")

	e.agent.err = nil
	e.ingest(alert("B", "y", "firing", "2"))
	e.drain()
	require.Equal(t, 1, e.agent.calls, "paused after a budget error")
	incs, _ := e.db.Ent.Incident.Query().Order(ent.Desc("id")).All(ctx)
	require.Contains(t, incs[0].InvestigationNote, "AI budget reached")

	// chat is refused while paused
	_, err := e.svc.PostMessage(ctx, operator(), inc.ID, "anything?")
	require.NoError(t, err)
	e.drain()
	msgs, _ := e.svc.Messages(ctx, inc.ID)
	require.Contains(t, msgs[len(msgs)-1].Content, "not available")

	u, err := e.svc.Usage(ctx)
	require.NoError(t, err)
	require.NotNil(t, u.PausedUntil)

	// after the pause, calls resume
	e.clock.Add(61 * time.Minute)
	_, err = e.svc.ReinvestigateWith(ctx, operator(), inc.ID, true)
	require.NoError(t, err)
	e.drain()
	require.Equal(t, 2, e.agent.calls)
}

func TestAutoApproveLoopGuard(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Actions.AutoApprove = []string{"test.echo", "agent.reinvestigate"} })
	e.agent.result.ProposedActions = append(e.agent.result.ProposedActions, agentProposal("agent.reinvestigate"))
	e.ingest(alert("Loop", "shop", "firing", "l"))
	e.drain()
	inc := e.onlyIncident()
	// investigation #1 (auto) -> echo + reinvestigate auto-run -> investigation #2 (action) -> no new reinvestigate
	require.Equal(t, 2, e.agent.calls)
	acts, err := e.svc.Actions(context.Background(), inc.ID)
	require.NoError(t, err)
	auto := 0
	for _, a := range acts {
		if a.DecidedByName == "infra-monitor" {
			auto++
		}
	}
	require.LessOrEqual(t, auto, 2, "at most 2 automatic executions per incident per hour")
}

type fakeAnnotations map[string]map[string]string

func (f fakeAnnotations) Annotations(_ context.Context, cluster, ns string) (map[string]string, error) {
	a, ok := f[cluster+"/"+ns]
	if !ok {
		return nil, errors.New("namespace not found")
	}
	return a, nil
}

// testAnnotations is used by newEnv: prod-shop opts out of automatic actions.
var testAnnotations = fakeAnnotations{
	"manager/prod-shop": {"infra-monitor.xonoko.com/auto-approve": "none"},
	"manager/batch":     {"infra-monitor.xonoko.com/auto-approve": "test.echo", "infra-monitor.xonoko.com/max-auto-per-hour": "1"},
}

func TestAutoApproveNamespaceAnnotations(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) {
		c.Actions.AutoApprove = []string{"test.echo"}
		c.Actions.AnnotationPrefix = "infra-monitor.xonoko.com"
	})
	ctx := context.Background()
	e.ingest(alert("A", "prod-shop", "firing", "p")) // annotation: none
	e.drain()
	e.ingest(alert("B", "dev", "firing", "d")) // namespace unreadable: config applies
	e.drain()
	incs, err := e.db.Ent.Incident.Query().Order(ent.Asc("id")).All(ctx)
	require.NoError(t, err)
	prod, _ := e.svc.Actions(ctx, incs[0].ID)
	dev, _ := e.svc.Actions(ctx, incs[1].ID)
	require.Equal(t, "proposed", string(prod[0].Status), "namespace annotation disables automatic actions")
	require.Equal(t, "succeeded", string(dev[0].Status), "global config applies when the namespace has no annotation")
}

func TestCostBreakdown(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) {
		c.Agent.PriceInputPerMTok, c.Agent.PriceOutputPerMTok = 1, 5
		c.Actions.AutoApprove = []string{"agent.reinvestigate"}
	})
	ctx := context.Background()
	e.agent.result.Usage = agent.Usage{PromptTokens: 1_000_000, CompletionTokens: 100_000}        // $1.5
	e.agent.chat.Usage = agent.Usage{PromptTokens: 200_000, CompletionTokens: 0}                  // $0.2
	e.agent.result.ProposedActions = []agent.ProposedAction{agentProposal("agent.reinvestigate")} // auto -> remediation investigation
	e.ingest(alert("Cost", "shop", "firing", "c"))
	e.drain()
	inc := e.onlyIncident()
	_, err := e.svc.PostMessage(ctx, operator(), inc.ID, "why?")
	require.NoError(t, err)
	e.drain()
	c, err := e.svc.Cost(ctx, inc.ID, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 1, c.Investigation.Calls)
	require.InDelta(t, 1.5, c.Investigation.USD, 1e-9)
	require.Equal(t, 2, c.Remediation.Calls, "the final check and the auto-approved reinvestigation count as remediation")
	require.InDelta(t, 1.5, c.Remediation.USD, 1e-9)
	require.Equal(t, 1, c.Chat.Calls)
	require.InDelta(t, 0.2, c.Chat.USD, 1e-9)
	require.InDelta(t, 3.2, c.Total.USD, 1e-9)
	require.Equal(t, 2_200_000, c.Total.PromptTokens)
}

func TestRecurrenceReusesAnalysis(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Correlation.ReopenWindow = 24 * time.Hour })
	e.agent.result.ProposedActions = nil
	for i := 0; i < 3; i++ {
		e.ingest(alert("HostOomKillDetected", "", "firing", "oom"))
		e.drain()
		e.ingest(alert("HostOomKillDetected", "", "resolved", "oom"))
		e.drain()
		e.clock.Add(3 * time.Hour) // comes back every few hours
	}
	inc := e.onlyIncident()
	require.Equal(t, 2, inc.ReopenCount)
	require.Equal(t, 1, e.agent.calls, "one analysis, reused on every recurrence")
}

func TestFailedCallsDontCountTowardCaps(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Agent.MaxPerHour = 1 })
	e.agent.err = errors.New("holmes: connection error: Name has no usable address")
	e.ingest(alert("A", "x", "firing", "1"))
	e.drain()
	e.agent.err = nil
	e.ingest(alert("B", "y", "firing", "2"))
	e.drain()
	require.Equal(t, 2, e.agent.calls, "the failed call (no tokens) didn't use the hourly cap")
}
