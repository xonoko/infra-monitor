package incident_test

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/agent"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/heartbeat"
	entincident "github.com/xonoko/infra-monitor/internal/db/ent/incident"
	"github.com/xonoko/infra-monitor/internal/db/ent/timelineevent"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/incident"
	"github.com/xonoko/infra-monitor/internal/queue"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

type clock struct {
	mu sync.Mutex
	t  time.Time
}

func (c *clock) Now() time.Time      { c.mu.Lock(); defer c.mu.Unlock(); return c.t }
func (c *clock) Add(d time.Duration) { c.mu.Lock(); c.t = c.t.Add(d); c.mu.Unlock() }
func newClock() *clock               { return &clock{t: time.Now().UTC().Truncate(time.Second)} }
func operator() domain.Actor {
	return domain.Actor{Type: domain.ActorUser, Name: "lucas", Role: domain.RoleOperator, Channel: "ui"}
}
func viewer() domain.Actor {
	return domain.Actor{Type: domain.ActorUser, Name: "bob", Role: domain.RoleViewer, Channel: "ui"}
}

type fakeAgent struct {
	mu          sync.Mutex
	result      *agent.InvestigationResult
	err         error
	chat        *agent.ChatResult
	calls       int
	lastReq     agent.ChatRequest
	checks      []string
	stillNeeded bool
}

func (f *fakeAgent) Name() string  { return "fake" }
func (f *fakeAgent) Model() string { return "fake-model" }
func (f *fakeAgent) Investigate(_ context.Context, ic agent.IncidentContext, p func(agent.Progress)) (*agent.InvestigationResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.calls++
	p(agent.Progress{Kind: "tool_call", Tool: "kubectl_get"})
	if f.err != nil {
		return nil, f.err
	}
	r := *f.result
	return &r, nil
}
func (f *fakeAgent) Chat(_ context.Context, req agent.ChatRequest, _ func(agent.Progress)) (*agent.ChatResult, error) {
	f.mu.Lock()
	defer f.mu.Unlock()
	f.lastReq = req
	if f.err != nil {
		return nil, f.err
	}
	return f.chat, nil
}

type echoExec struct{ runs int }

func (*echoExec) Type() string        { return "test.echo" }
func (*echoExec) Description() string { return "echo" }
func (*echoExec) Risk() string        { return "low" }
func (*echoExec) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["msg"],"properties":{"msg":{"type":"string"}},"additionalProperties":false}`)
}
func (*echoExec) Validate(_ context.Context, _ map[string]any, t actions.Target) error {
	if t.Namespace == "kube-system" {
		return errors.New("namespace denied")
	}
	return nil
}
func (*echoExec) Summary(p map[string]any, _ actions.Target) string {
	return "echo " + p["msg"].(string)
}
func (e *echoExec) Execute(_ context.Context, p map[string]any, _ actions.Target) (string, error) {
	e.runs++
	return "said " + p["msg"].(string), nil
}

type env struct {
	t     *testing.T
	db    *db.DB
	svc   *incident.Service
	q     *queue.Queue
	clock *clock
	agent *fakeAgent
	exec  *echoExec
}

func testConfig() *config.Config {
	return &config.Config{
		Server:      config.ServerConfig{PublicURL: "https://im.example.com"},
		Sources:     []config.SourceConfig{{Name: "am", Type: "alertmanager", ClusterLabel: "cluster", NamespaceLabel: "namespace"}},
		Heartbeat:   config.HeartbeatConfig{Enabled: true, Matchers: []string{"alertname=Watchdog"}, ExpectedInterval: time.Minute, SilentAfter: 10 * time.Minute, PingInterval: time.Minute},
		Correlation: config.CorrelationConfig{GroupBy: []string{"alertname", "cluster", "namespace"}, ReopenWindow: 30 * time.Minute, Debounce: 30 * time.Second, ResolveGrace: time.Minute},
		Agent:       config.AgentConfig{Enabled: true, Timeout: time.Minute, NotifyTimeout: 2 * time.Minute, MaxConcurrent: 2, MaxPerHour: 10, Cooldown: 15 * time.Minute},
		Actions:     config.ActionsConfig{ProposalTTL: time.Hour, Timeout: time.Minute, MaxAutoPerHour: 2},
		Channels:    []config.ChannelConfig{{Name: "tg", Type: "telegram"}},
	}
}

func newEnv(t *testing.T, withAgent bool, mutate ...func(*config.Config)) *env {
	t.Helper()
	d := pgtest.New(t)
	cfg := testConfig()
	for _, m := range mutate {
		m(cfg)
	}
	c := newClock()
	reg := actions.NewRegistry(nil, cfg.Actions.AutoApprove)
	ex := &echoExec{}
	require.NoError(t, reg.Register(ex))
	fa := &fakeAgent{result: &agent.InvestigationResult{
		Analysis: "## Findings\nPod is crashlooping", Summary: "api pod crashloops after OOM", RootCause: "memory limit too low",
		Verdict: "actionable", Confidence: 0.8,
		ProposedActions: []agent.ProposedAction{
			{Type: "test.echo", Params: map[string]any{"msg": "restart"}, Rationale: "restart it", Risk: "low"},
			{Type: "rm.rf", Params: map[string]any{}, Rationale: "nope"},
		},
		Conversation: json.RawMessage(`[{"role":"system","content":"x"}]`),
	}, chat: &agent.ChatResult{Reply: "It is the memory limit.", Conversation: json.RawMessage(`[{"role":"system","content":"y"}]`)}}
	fa.stillNeeded = true
	deps := incident.Deps{Config: cfg, DB: d, Log: pgtest.Logger(), Registry: reg, Now: c.Now, NamespaceAnnotations: testAnnotations}
	if withAgent {
		deps.Agent = fa
	}
	svc, err := incident.New(deps)
	require.NoError(t, err)
	require.NoError(t, reg.Register(incident.ReinvestigateExecutor{Svc: svc}))
	q := queue.New(d.Pool, nil, pgtest.Logger(), time.Second)
	svc.RegisterJobs(q)
	q.Register(incident.JobChannelSync, func(context.Context, *queue.Job) error { return nil }, queue.HandlerOpts{})
	return &env{t: t, db: d, svc: svc, q: q, clock: c, agent: fa, exec: ex}
}

func (e *env) drain() {
	e.t.Helper()
	_, err := e.q.Drain(context.Background(), true)
	require.NoError(e.t, err)
}

func (e *env) ingest(sigs ...domain.Signal) {
	e.t.Helper()
	_, err := e.svc.IngestSignals(context.Background(), "am", sigs)
	require.NoError(e.t, err)
}

func alert(name, ns, status, fp string) domain.Signal {
	return domain.Signal{
		Fingerprint: fp, Status: status,
		Labels:      map[string]string{"alertname": name, "cluster": "manager", "namespace": ns, "severity": "critical", "pod": fp},
		Annotations: map[string]string{"summary": name + " in " + ns},
		StartsAt:    time.Now().Add(-time.Minute),
	}
}

func (e *env) events(incID int) []string {
	evs, err := e.db.Ent.TimelineEvent.Query().Where(timelineevent.IncidentID(incID)).Order(ent.Asc(timelineevent.FieldID)).All(context.Background())
	require.NoError(e.t, err)
	var out []string
	for _, ev := range evs {
		out = append(out, ev.Type)
	}
	return out
}

func (e *env) onlyIncident() *ent.Incident {
	e.t.Helper()
	incs, err := e.db.Ent.Incident.Query().All(context.Background())
	require.NoError(e.t, err)
	require.Len(e.t, incs, 1)
	return incs[0]
}

func TestFiringAlertCreatesIncidentInvestigatesAndRoutes(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	e.ingest(alert("KubePodCrashLooping", "shop", "firing", "fp1"))
	e.ingest(alert("KubePodCrashLooping", "shop", "firing", "fp1")) // repeat: ignored
	e.ingest(alert("KubePodCrashLooping", "shop", "firing", "fp2")) // same group: attached
	e.drain()

	inc := e.onlyIncident()
	require.Equal(t, entincident.StatusOpen, inc.Status)
	require.Equal(t, "manager", inc.Cluster)
	require.Equal(t, "shop", inc.Namespace)
	require.Equal(t, entincident.VerdictActionable, inc.Verdict)
	require.Equal(t, entincident.InvestigationStateDone, inc.InvestigationState)
	require.Equal(t, []string{"tg"}, inc.NotifyChannels)
	require.NotNil(t, inc.NotifiedAt)
	require.Equal(t, 1, e.agent.calls)
	require.JSONEq(t, `[{"role":"system","content":"x"}]`, string(inc.AgentConversation))

	evs := e.events(inc.ID)
	require.Equal(t, "incident_opened", evs[0])
	require.Contains(t, evs, "investigation_completed")
	require.Contains(t, evs, "action_proposed")
	require.Contains(t, evs, "notification_routed")
	n := 0
	for _, ev := range evs {
		if ev == "signal_firing" {
			n++
		}
	}
	require.Equal(t, 2, n, "repeat notification must not add events")

	acts, err := e.svc.Actions(ctx, inc.ID)
	require.NoError(t, err)
	require.Len(t, acts, 1, "unregistered type must be dropped")
	require.Equal(t, "echo restart", acts[0].Summary)

	// viewer can't approve, operator can; execution runs once
	_, err = e.svc.Approve(ctx, viewer(), acts[0].ID)
	require.ErrorIs(t, err, incident.ErrForbidden)
	a, err := e.svc.Approve(ctx, operator(), acts[0].ID)
	require.NoError(t, err)
	require.Equal(t, "lucas", a.DecidedByName)
	_, err = e.svc.Approve(ctx, operator(), acts[0].ID)
	require.ErrorIs(t, err, incident.ErrConflict)
	e.drain()
	a, err = e.svc.GetAction(ctx, a.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", string(a.Status))
	require.Equal(t, "said restart", a.Output)
	require.Equal(t, 1, e.exec.runs)
	require.Contains(t, e.events(inc.ID), "action_succeeded")
}

func TestResolveGraceAndReopenWindow(t *testing.T) {
	e := newEnv(t, false)
	e.ingest(alert("HighLatency", "web", "firing", "a"))
	e.drain()
	inc := e.onlyIncident()
	require.Equal(t, []string{"tg"}, inc.NotifyChannels, "no agent: fail open routes critical alerts")

	e.ingest(alert("HighLatency", "web", "resolved", "a"))
	e.drain()
	inc = e.onlyIncident()
	require.Equal(t, entincident.StatusResolved, inc.Status)
	require.NotNil(t, inc.ResolvedAt)

	// re-fire within the window: same incident reopened
	e.clock.Add(5 * time.Minute)
	e.ingest(alert("HighLatency", "web", "firing", "a"))
	e.drain()
	inc = e.onlyIncident()
	require.Equal(t, entincident.StatusOpen, inc.Status)
	require.Equal(t, 1, inc.ReopenCount)
	require.Contains(t, e.events(inc.ID), "incident_reopened")

	// resolve, then re-fire after the window: a new incident
	e.ingest(alert("HighLatency", "web", "resolved", "a"))
	e.drain()
	e.clock.Add(2 * time.Hour)
	e.ingest(alert("HighLatency", "web", "firing", "a"))
	e.drain()
	n, err := e.db.Ent.Incident.Query().Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, n)
}

func TestAgentFailureFailsOpen(t *testing.T) {
	e := newEnv(t, true)
	e.agent.err = errors.New("holmes unreachable")
	e.ingest(alert("DiskFull", "db", "firing", "d"))
	e.drain()
	inc := e.onlyIncident()
	require.Equal(t, entincident.InvestigationStateFailed, inc.InvestigationState)
	require.Equal(t, []string{"tg"}, inc.NotifyChannels)
	require.Contains(t, e.events(inc.ID), "investigation_failed")
}

func TestNoiseIsNotRouted(t *testing.T) {
	e := newEnv(t, true)
	e.agent.result.Verdict = "noise"
	e.agent.result.ProposedActions = nil
	sig := alert("CPUThrottling", "batch", "firing", "n")
	sig.Labels["severity"] = "warning"
	e.ingest(sig)
	e.drain()
	inc := e.onlyIncident()
	require.Equal(t, entincident.VerdictNoise, inc.Verdict)
	require.Empty(t, inc.NotifyChannels)
	require.Nil(t, inc.NotifiedAt)
}

func TestHeartbeatSilentAndBack(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	require.NoError(t, e.svc.EnsureHeartbeats(ctx))
	wd := domain.Signal{Fingerprint: "wd", Status: "firing", Labels: map[string]string{"alertname": "Watchdog", "severity": "none"}}
	e.ingest(wd)
	e.drain()
	n, _ := e.db.Ent.Incident.Query().Count(ctx)
	require.Zero(t, n, "heartbeats never create incidents")
	hb, err := e.db.Ent.Heartbeat.Query().Where(heartbeat.Key("am")).Only(ctx)
	require.NoError(t, err)
	require.Equal(t, heartbeat.StateOk, hb.State)

	e.clock.Add(11 * time.Minute)
	require.NoError(t, e.svc.CheckHeartbeats(ctx))
	e.drain()
	inc := e.onlyIncident()
	require.True(t, inc.Internal)
	require.Equal(t, "critical", inc.Severity)
	require.Equal(t, []string{"tg"}, inc.NotifyChannels)

	e.ingest(wd)
	e.drain()
	inc = e.onlyIncident()
	require.Equal(t, entincident.StatusResolved, inc.Status)
}

func TestMuteSuppressesNotification(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	e.ingest(alert("NodeDown", "infra", "firing", "m1"))
	e.drain()
	inc := e.onlyIncident()
	m, err := e.svc.MuteIncident(ctx, operator(), inc.ID, time.Hour, "maintenance")
	require.NoError(t, err)
	inc, _ = e.svc.Get(ctx, inc.ID)
	require.True(t, inc.Muted)

	// resolve and come back later as a new incident: still muted, not routed
	e.ingest(alert("NodeDown", "infra", "resolved", "m1"))
	e.drain()
	e.clock.Add(40 * time.Minute)
	e.ingest(alert("NodeDown", "infra", "firing", "m1"))
	e.drain()
	incs, err := e.db.Ent.Incident.Query().Order(ent.Desc("id")).All(ctx)
	require.NoError(t, err)
	require.Len(t, incs, 2)
	require.True(t, incs[0].Muted)
	require.Empty(t, incs[0].NotifyChannels)

	_, err = e.svc.ExpireMute(ctx, operator(), m.ID)
	require.NoError(t, err)
	got, _ := e.svc.Get(ctx, incs[0].ID)
	require.False(t, got.Muted)
	require.Contains(t, e.events(got.ID), "mute_ended")
}

func TestChatRoundTrip(t *testing.T) {
	e := newEnv(t, true)
	ctx := context.Background()
	e.ingest(alert("OOMKilled", "api", "firing", "c"))
	e.drain()
	inc := e.onlyIncident()
	_, err := e.svc.PostMessage(ctx, viewer(), inc.ID, "why?")
	require.ErrorIs(t, err, incident.ErrForbidden)
	tg := operator()
	tg.Channel, tg.ChannelName = "telegram", "tg"
	_, err = e.svc.PostMessage(ctx, tg, inc.ID, "why did it crash?")
	require.NoError(t, err)
	e.drain()
	msgs, err := e.svc.Messages(ctx, inc.ID)
	require.NoError(t, err)
	require.Len(t, msgs, 2)
	require.Equal(t, "telegram", msgs[0].Channel)
	require.Equal(t, "agent", string(msgs[1].Role))
	require.Equal(t, "It is the memory limit.", msgs[1].Content)
	require.JSONEq(t, `[{"role":"system","content":"x"}]`, string(e.agent.lastReq.Conversation), "history from the investigation is passed")
	inc, _ = e.svc.Get(ctx, inc.ID)
	require.JSONEq(t, `[{"role":"system","content":"y"}]`, string(inc.AgentConversation))
}

func TestProposalExpiryAndPolicy(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	e.ingest(alert("X", "kube-system", "firing", "k"))
	e.drain()
	inc := e.onlyIncident()
	_, err := e.svc.ProposeAction(ctx, operator(), inc.ID, "test.echo", map[string]any{"msg": "x"}, "")
	require.ErrorIs(t, err, incident.ErrInvalid, "namespace policy applies to user proposals too")

	e.ingest(alert("Y", "shop", "firing", "y"))
	e.drain()
	incs, _ := e.db.Ent.Incident.Query().Order(ent.Desc("id")).All(ctx)
	a, err := e.svc.ProposeAction(ctx, operator(), incs[0].ID, "test.echo", map[string]any{"msg": "x"}, "")
	require.NoError(t, err)
	dup, err := e.svc.ProposeAction(ctx, operator(), incs[0].ID, "test.echo", map[string]any{"msg": "x"}, "")
	require.NoError(t, err)
	require.Equal(t, a.ID, dup.ID, "identical pending proposal is reused")
	e.clock.Add(2 * time.Hour)
	require.NoError(t, e.svc.ExpireProposals(ctx))
	a, _ = e.svc.GetAction(ctx, a.ID)
	require.Equal(t, "expired", string(a.Status))
	_, err = e.svc.Approve(ctx, operator(), a.ID)
	require.ErrorIs(t, err, incident.ErrConflict)
}

func TestAutoApprove(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Actions.AutoApprove = []string{"test.echo"} })
	e.ingest(alert("Z", "shop", "firing", "z"))
	e.drain()
	inc := e.onlyIncident()
	acts, err := e.svc.Actions(context.Background(), inc.ID)
	require.NoError(t, err)
	require.Equal(t, "succeeded", string(acts[0].Status))
	require.Equal(t, "infra-monitor", acts[0].DecidedByName)
}

func TestListAndHistogram(t *testing.T) {
	e := newEnv(t, false)
	ctx := context.Background()
	e.ingest(alert("A", "shop", "firing", "1"), alert("B", "web", "firing", "2"))
	e.drain()
	items, total, _, err := e.svc.List(ctx, incident.ListFilter{Query: "shop"})
	require.NoError(t, err)
	require.Equal(t, 1, total)
	require.Equal(t, "shop", items[0].Namespace)
	items, total, next, err := e.svc.List(ctx, incident.ListFilter{Limit: 1})
	require.NoError(t, err)
	require.Equal(t, 2, total)
	require.Len(t, items, 1)
	require.NotZero(t, next)
	counts, err := e.svc.Counts(ctx, []int{items[0].ID})
	require.NoError(t, err)
	require.Equal(t, 1, counts[items[0].ID].Firing)
	bs, err := e.svc.Histogram(ctx, incident.ListFilter{}, e.clock.Now().Add(-time.Hour), e.clock.Now().Add(time.Minute), 10)
	require.NoError(t, err)
	total = 0
	for _, b := range bs {
		total += b.Critical
	}
	require.Equal(t, 2, total)
}

func agentProposal(typ string) agent.ProposedAction {
	return agent.ProposedAction{Type: typ, Params: map[string]any{}, Rationale: "check again"}
}

func TestHeartbeatPingCadence(t *testing.T) {
	hits := 0
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { hits++ }))
	defer srv.Close()
	e := newEnv(t, false, func(c *config.Config) { c.Heartbeat.PingURL = srv.URL })
	ctx := context.Background()
	require.NoError(t, e.svc.EnsureHeartbeats(ctx))
	require.NoError(t, e.svc.PingHeartbeats(ctx))
	require.Zero(t, hits, "no ping before the first heartbeat")

	e.ingest(domain.Signal{Fingerprint: "wd", Status: "firing", Labels: map[string]string{"alertname": "Watchdog"}})
	require.NoError(t, e.svc.PingHeartbeats(ctx))
	require.Equal(t, 1, hits)
	e.clock.Add(20 * time.Second)
	require.NoError(t, e.svc.PingHeartbeats(ctx))
	require.Equal(t, 1, hits, "not before the interval")
	e.clock.Add(45 * time.Second) // 65s after the last ping, Watchdog still fresh (< silent_after)
	require.NoError(t, e.svc.PingHeartbeats(ctx))
	require.Equal(t, 2, hits, "fixed cadence, independent of Watchdog deliveries")

	e.clock.Add(11 * time.Minute) // Watchdog silent: stop pinging
	require.NoError(t, e.svc.PingHeartbeats(ctx))
	require.Equal(t, 2, hits)
}
