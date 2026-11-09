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
	"github.com/xonoko/infra-monitor/internal/db/ent/signal"
)

func (e *env) incidents() []*ent.Incident {
	e.t.Helper()
	all, err := e.db.Ent.Incident.Query().Order(ent.Asc(entincident.FieldID)).All(context.Background())
	require.NoError(e.t, err)
	return all
}

func sameAsFirst(req agent.CorrelationRequest) (*agent.CorrelationResult, error) {
	return &agent.CorrelationResult{SameAs: req.Candidates[len(req.Candidates)-1].IncidentID, Reason: "both are the api OOM",
		Model: "fake-model", Usage: agent.Usage{PromptTokens: 900, CompletionTokens: 30}}, nil
}

func TestGroupingMergesSameProblem(t *testing.T) {
	e := newEnv(t, true)
	e.agent.result.ProposedActions = nil
	e.agent.correlate = sameAsFirst
	e.ingest(alert("KubernetesContainerOomKiller", "demo", "firing", "oom"))
	e.drain()
	require.Empty(t, e.agent.correlations, "nothing open in the namespace yet: no grouping call")

	e.ingest(alert("KubernetesPodCrashLooping", "demo", "firing", "crash"), alert("Unrelated", "other", "firing", "x"))
	e.drain()
	incs := e.incidents()
	require.Len(t, incs, 3)
	first, crash, other := incs[0], incs[1], incs[2]
	if crash.Namespace == "other" {
		crash, other = other, crash
	}
	require.Equal(t, entincident.StatusResolved, crash.Status)
	require.NotNil(t, crash.MergedIntoID)
	require.Equal(t, first.ID, *crash.MergedIntoID)
	require.Empty(t, crash.NotifyChannels, "never notified on its own")
	require.Equal(t, entincident.StatusOpen, first.Status)

	n, err := e.db.Ent.Signal.Query().Where(signal.IncidentID(first.ID)).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 2, n, "the crash-loop alert moved to the first incident")
	require.Contains(t, e.events(first.ID), "incident_merged")
	require.Contains(t, e.events(crash.ID), "incident_merged")

	require.Len(t, e.agent.correlations, 1, "only the demo namespace had a candidate")
	req := e.agent.correlations[0]
	require.Equal(t, first.ID, req.Candidates[0].IncidentID)
	require.Equal(t, "api pod crashloops after OOM", req.Candidates[0].Summary)
	require.Equal(t, "memory limit too low", req.Candidates[0].RootCause)
	require.Empty(t, req.Alert.ActionTypes)
	require.Equal(t, 2, e.agent.calls, "investigated: the first incident and the unrelated one")
	require.Equal(t, entincident.StatusOpen, other.Status)

	c, err := e.svc.Cost(context.Background(), first.ID, time.Time{})
	require.NoError(t, err)
	require.Equal(t, 2, c.Investigation.Calls, "the grouping check is costed on the incident it joined")

	inv, err := e.svc.LatestInvestigation(context.Background(), first.ID)
	require.NoError(t, err)
	require.Equal(t, "api pod crashloops after OOM", inv.Summary, "the grouping check is not the latest analysis")
}

func TestGroupingKeepsDifferentProblemsSeparate(t *testing.T) {
	for name, answer := range map[string]func(agent.CorrelationRequest) (*agent.CorrelationResult, error){
		"different": func(agent.CorrelationRequest) (*agent.CorrelationResult, error) {
			return &agent.CorrelationResult{SameAs: 0, Reason: "different workloads"}, nil
		},
		"unknown id": func(agent.CorrelationRequest) (*agent.CorrelationResult, error) {
			return &agent.CorrelationResult{SameAs: 999, Reason: "?"}, nil
		},
		"agent down": func(agent.CorrelationRequest) (*agent.CorrelationResult, error) {
			return nil, errors.New("litellm: connection error")
		},
	} {
		t.Run(name, func(t *testing.T) {
			e := newEnv(t, true)
			e.agent.result.ProposedActions = nil
			e.agent.correlate = answer
			e.ingest(alert("A", "demo", "firing", "a"))
			e.drain()
			e.ingest(alert("B", "demo", "firing", "b"))
			e.drain()
			incs := e.incidents()
			require.Len(t, incs, 2)
			for _, inc := range incs {
				require.Nil(t, inc.MergedIntoID)
				require.Equal(t, entincident.StatusOpen, inc.Status)
			}
			require.Equal(t, 2, e.agent.calls, "each investigated")
		})
	}
}

func TestMergedAlertFiresAgainIntoTarget(t *testing.T) {
	e := newEnv(t, true)
	e.agent.result.ProposedActions = nil
	e.agent.correlate = sameAsFirst
	e.ingest(alert("OOM", "demo", "firing", "oom"))
	e.drain()
	e.ingest(alert("CrashLoop", "demo", "firing", "crash"))
	e.drain()
	e.ingest(alert("CrashLoop", "demo", "resolved", "crash"))
	e.drain()
	first := e.incidents()[0]
	require.Equal(t, entincident.StatusOpen, first.Status, "the OOM alert still fires")

	// Same alert again, and the same alert on another pod: both go to the first incident.
	e.ingest(alert("CrashLoop", "demo", "firing", "crash"), alert("CrashLoop", "demo", "firing", "crash-2"))
	e.drain()
	require.Len(t, e.incidents(), 2, "no new incident")
	n, err := e.db.Ent.Signal.Query().Where(signal.IncidentID(first.ID)).Count(context.Background())
	require.NoError(t, err)
	require.Equal(t, 3, n)

	// Everything resolves, then the crash loop comes back: the first incident reopens.
	e.ingest(alert("OOM", "demo", "resolved", "oom"), alert("CrashLoop", "demo", "resolved", "crash"), alert("CrashLoop", "demo", "resolved", "crash-2"))
	e.drain()
	e.clock.Add(2 * time.Minute)
	e.drain()
	require.Equal(t, entincident.StatusResolved, e.incidents()[0].Status)
	e.ingest(alert("CrashLoop", "demo", "firing", "crash"))
	e.drain()
	incs := e.incidents()
	require.Len(t, incs, 2)
	require.Equal(t, entincident.StatusOpen, incs[0].Status)
	require.Equal(t, 1, incs[0].ReopenCount)
	require.Equal(t, 1, e.agent.calls, "reopened with its analysis reused")
}

func TestReopenedIncidentsAreGrouped(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Correlation.ReopenWindow = 24 * time.Hour })
	e.agent.result.ProposedActions = nil
	// Day 1: two separate incidents (grouping unavailable), both investigated, both resolved.
	e.ingest(alert("OOM", "demo", "firing", "oom"))
	e.drain()
	e.ingest(alert("Mismatch", "demo", "firing", "mm"))
	e.drain()
	e.ingest(alert("OOM", "demo", "resolved", "oom"), alert("Mismatch", "demo", "resolved", "mm"))
	e.drain()
	e.clock.Add(2 * time.Minute)
	e.drain()
	incs := e.incidents()
	require.Len(t, incs, 2)
	require.Equal(t, 2, e.agent.calls)

	// It comes back: OOM reopens first, then Mismatch reopens and is grouped into it (reused analyses, no new call).
	e.agent.correlate = func(req agent.CorrelationRequest) (*agent.CorrelationResult, error) {
		return &agent.CorrelationResult{SameAs: incs[0].ID, Reason: "same OOM"}, nil
	}
	e.ingest(alert("OOM", "demo", "firing", "oom"))
	e.drain()
	e.ingest(alert("Mismatch", "demo", "firing", "mm"))
	e.drain()
	incs = e.incidents()
	require.Equal(t, entincident.StatusOpen, incs[0].Status)
	require.NotNil(t, incs[1].MergedIntoID)
	require.Equal(t, incs[0].ID, *incs[1].MergedIntoID)
	require.Equal(t, 2, e.agent.calls, "analyses reused")
}

func TestReopenAfterAFixInvestigatesAgain(t *testing.T) {
	e := newEnv(t, true, func(c *config.Config) { c.Correlation.ReopenWindow = 24 * time.Hour })
	e.ingest(alert("OOM", "demo", "firing", "oom"))
	e.drain()
	inc := e.onlyIncident()
	acts, err := e.svc.Actions(context.Background(), inc.ID)
	require.NoError(t, err)
	var echo int
	for _, a := range acts {
		if a.Type == "test.echo" {
			echo = a.ID
		}
	}
	require.NotZero(t, echo)
	e.clock.Add(time.Minute)
	_, err = e.svc.Approve(context.Background(), operator(), echo)
	require.NoError(t, err)
	e.drain()
	e.ingest(alert("OOM", "demo", "resolved", "oom"))
	e.drain()
	e.clock.Add(2 * time.Minute)
	e.drain()

	e.ingest(alert("OOM", "demo", "firing", "oom"))
	e.drain()
	require.Equal(t, 2, e.agent.calls, "the fix ran and it came back: investigated again")
}
