package alertmanager

import (
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/domain"
)

func TestParseWebhook(t *testing.T) {
	body, err := os.ReadFile("testdata/webhook_vmalertmanager.json")
	require.NoError(t, err)
	sigs, err := ParseWebhook(body, "alertmanager")
	require.NoError(t, err)
	require.Len(t, sigs, 2)

	wd := sigs[0]
	require.Equal(t, "alertmanager", wd.Source)
	require.Equal(t, "c4c1fa1a3c5e2d20", wd.Fingerprint)
	require.Equal(t, "firing", wd.Status)
	require.Equal(t, "Watchdog", wd.Label("alertname"))
	require.Equal(t, "none", wd.Label("severity"))
	require.Equal(t, "manager", wd.Label("cluster"))
	require.Nil(t, wd.EndsAt, "firing alerts keep no endsAt")
	require.Equal(t, time.Date(2026, 11, 8, 8, 0, 0, 0, time.UTC), wd.StartsAt.UTC())
	require.Contains(t, string(wd.Raw), "Watchdog")

	cl := sigs[1]
	require.Equal(t, "resolved", cl.Status)
	require.NotNil(t, cl.EndsAt)
	require.Equal(t, Fingerprint(cl.Labels), cl.Fingerprint)
	require.Len(t, cl.Fingerprint, 16)
	require.Equal(t, "shop", cl.Label("namespace"))
}

func TestParseWebhookErrors(t *testing.T) {
	_, err := ParseWebhook([]byte(`{`), "am")
	require.Error(t, err)
	_, err = ParseWebhook([]byte(`{"version":"3","alerts":[]}`), "am")
	require.Error(t, err)
	_, err = ParseWebhook([]byte(`{"alerts":[{"status":"weird","labels":{"a":"b"}}]}`), "am")
	require.Error(t, err)
	sigs, err := ParseWebhook([]byte(`{"status":"firing","alerts":[{"labels":{"a":"b"}}]}`), "am")
	require.NoError(t, err)
	require.Equal(t, "firing", sigs[0].Status)
	require.False(t, sigs[0].StartsAt.IsZero())
}

func TestFingerprintStable(t *testing.T) {
	a := Fingerprint(map[string]string{"a": "1", "b": "2"})
	b := Fingerprint(map[string]string{"b": "2", "a": "1"})
	require.Equal(t, a, b)
	require.NotEqual(t, a, Fingerprint(map[string]string{"a": "12"}))
	// boundaries matter: {"a":"1b","":"2"} must differ from {"a":"1","b":"2"}
	require.NotEqual(t, Fingerprint(map[string]string{"ab": "1"}), Fingerprint(map[string]string{"a": "b1"}))
}

func TestClient(t *testing.T) {
	var gotSilence map[string]any
	var deleted string
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		switch {
		case r.Method == http.MethodGet && r.URL.Path == "/api/v2/alerts":
			require.Equal(t, "true", r.URL.Query().Get("active"))
			require.Equal(t, "true", r.URL.Query().Get("silenced"))
			_, _ = io.WriteString(w, `[{"fingerprint":"abc","labels":{"alertname":"HighCPU","cluster":"manager"},"annotations":{"summary":"cpu"},
				"startsAt":"2026-11-08T10:00:00Z","endsAt":"2026-11-08T12:00:00Z","generatorURL":"http://x","status":{"state":"active","silencedBy":[],"inhibitedBy":[]},
				"receivers":[{"name":"infra-monitor"}],"updatedAt":"2026-11-08T10:00:00Z"}]`)
		case r.Method == http.MethodPost && r.URL.Path == "/api/v2/silences":
			require.NoError(t, json.NewDecoder(r.Body).Decode(&gotSilence))
			_, _ = io.WriteString(w, `{"silenceID":"sil-1"}`)
		case r.Method == http.MethodDelete && r.URL.Path == "/api/v2/silence/sil-1":
			deleted = "sil-1"
		case r.URL.Path == "/api/v2/silence/missing":
			w.WriteHeader(http.StatusNotFound)
			_, _ = io.WriteString(w, "silence not found")
		default:
			http.NotFound(w, r)
		}
	}))
	defer srv.Close()
	c, err := NewClient(srv.URL+"/", nil)
	require.NoError(t, err)
	ctx := context.Background()

	sigs, err := c.ListAlerts(ctx, "am")
	require.NoError(t, err)
	require.Len(t, sigs, 1)
	require.Equal(t, "abc", sigs[0].Fingerprint)
	require.Equal(t, "firing", sigs[0].Status)
	require.Nil(t, sigs[0].EndsAt)

	ms, err := domain.ParseMatchers([]string{"alertname=HighCPU", "namespace!=kube-system", "pod=~api-.*", "job!~test.*"})
	require.NoError(t, err)
	now := time.Now()
	id, err := c.CreateSilence(ctx, ms, now, now.Add(time.Hour), "lucas", "flapping")
	require.NoError(t, err)
	require.Equal(t, "sil-1", id)
	matchers := gotSilence["matchers"].([]any)
	require.Len(t, matchers, 4)
	require.Equal(t, map[string]any{"name": "alertname", "value": "HighCPU", "isRegex": false, "isEqual": true}, matchers[0])
	require.Equal(t, map[string]any{"name": "namespace", "value": "kube-system", "isRegex": false, "isEqual": false}, matchers[1])
	require.Equal(t, map[string]any{"name": "pod", "value": "api-.*", "isRegex": true, "isEqual": true}, matchers[2])
	require.Equal(t, map[string]any{"name": "job", "value": "test.*", "isRegex": true, "isEqual": false}, matchers[3])
	require.Equal(t, "lucas", gotSilence["createdBy"])

	_, err = c.CreateSilence(ctx, nil, now, now.Add(time.Hour), "", "")
	require.Error(t, err)

	require.NoError(t, c.ExpireSilence(ctx, "sil-1"))
	require.Equal(t, "sil-1", deleted)
	err = c.ExpireSilence(ctx, "missing")
	require.ErrorContains(t, err, "HTTP 404")

	_, err = NewClient("not a url", nil)
	require.Error(t, err)
}
