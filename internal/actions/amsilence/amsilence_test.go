package amsilence

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/source/alertmanager"
)

func TestSilence(t *testing.T) {
	var got map[string]any
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		require.Equal(t, "/api/v2/silences", r.URL.Path)
		require.NoError(t, json.NewDecoder(r.Body).Decode(&got))
		_, _ = io.WriteString(w, `{"silenceID":"s-42"}`)
	}))
	defer srv.Close()
	am, err := alertmanager.NewClient(srv.URL, nil)
	require.NoError(t, err)
	e := New(func(src string) (*alertmanager.Client, bool) { return am, src == "alertmanager" }, 4*time.Hour)
	fixed := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return fixed }

	r := actions.NewRegistry(nil, nil)
	require.NoError(t, r.Register(e))
	ctx := context.Background()
	tgt := actions.Target{IncidentID: 7, Source: "alertmanager", Labels: map[string]string{"alertname": "HighCPU", "cluster": "manager"}}

	require.NoError(t, r.Validate(ctx, "alertmanager.silence", map[string]any{"duration": "2h"}, tgt))
	require.Error(t, r.Validate(ctx, "alertmanager.silence", map[string]any{"duration": "5h"}, tgt), "over max")
	require.Error(t, r.Validate(ctx, "alertmanager.silence", map[string]any{"duration": "soon"}, tgt))
	require.Error(t, r.Validate(ctx, "alertmanager.silence", map[string]any{}, tgt))
	require.Error(t, r.Validate(ctx, "alertmanager.silence", map[string]any{"duration": "1h"}, actions.Target{Source: "alertmanager"}), "no labels")
	require.Error(t, r.Validate(ctx, "alertmanager.silence", map[string]any{"duration": "1h"}, actions.Target{Source: "other", Labels: tgt.Labels}), "no client")
	require.Error(t, r.Validate(ctx, "alertmanager.silence", map[string]any{"duration": "1h", "matchers": []any{map[string]any{"name": "x", "op": "=~", "value": ".*"}}}, tgt), "too broad")
	require.Error(t, r.Validate(ctx, "alertmanager.silence", map[string]any{"duration": "1h", "matchers": []any{map[string]any{"name": "x", "op": "=~", "value": "("}}}, tgt), "bad regex")

	require.Equal(t, "silence {alertname=HighCPU, cluster=manager} for 2h0m0s", e.Summary(map[string]any{"duration": "2h"}, tgt))
	out, err := e.Execute(ctx, map[string]any{"duration": "2h", "comment": "deploy in progress"}, tgt)
	require.NoError(t, err)
	require.Equal(t, "silence s-42 until 2026-11-08T14:00:00Z", out)
	require.Equal(t, "deploy in progress (incident #7)", got["comment"])
	require.Len(t, got["matchers"], 2)
	require.Equal(t, "2026-11-08T14:00:00Z", got["endsAt"])

	_, err = e.Execute(ctx, map[string]any{"duration": "30m", "matchers": []any{
		map[string]any{"name": "alertname", "op": "=", "value": "Disk"},
		map[string]any{"name": "instance", "op": "=~", "value": "node-[12]"},
	}}, tgt)
	require.NoError(t, err)
	ms := got["matchers"].([]any)
	require.Equal(t, map[string]any{"name": "instance", "value": "node-[12]", "isRegex": true, "isEqual": true}, ms[1])
}
