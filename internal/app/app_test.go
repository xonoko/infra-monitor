package app_test

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net"
	"net/http"
	"net/http/cookiejar"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/app"
	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/telemetry"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

type replica struct {
	url    string
	app    *app.App
	cancel context.CancelFunc
	done   chan error
}

func startReplica(t *testing.T, dsn string, extra ...string) *replica {
	t.Helper()
	dir := t.TempDir()
	cfgPath := filepath.Join(dir, "config.yaml")
	require.NoError(t, os.WriteFile(cfgPath, []byte(`
env: development
log_level: error
database:
  dsn: "`+dsn+`"
server:
  public_url: http://localhost
sources:
  - name: alertmanager
    type: alertmanager
    auth:
      bearer_token: hook-secret
correlation:
  debounce: 0s
  resolve_grace: 0s
jobs:
  poll_interval: 200ms
`+strings.Join(extra, "\n")), 0o600))
	cfg, err := config.Load(cfgPath)
	require.NoError(t, err)
	ctx, cancel := context.WithCancel(context.Background())
	a, err := app.Build(ctx, cfg, pgtest.Logger(), app.Options{})
	require.NoError(t, err)
	a.Leases.RetryInterval = 200 * time.Millisecond
	ln, err := net.Listen("tcp", "127.0.0.1:0")
	require.NoError(t, err)
	scheme := "http://"
	if cfg.Server.TLS.CertFile != "" {
		scheme = "https://"
	}
	r := &replica{url: scheme + ln.Addr().String(), app: a, cancel: cancel, done: make(chan error, 1)}
	go func() { r.done <- a.Serve(ctx, ln) }()
	t.Cleanup(func() {
		cancel()
		select {
		case <-r.done:
		case <-time.After(10 * time.Second):
			t.Error("replica did not stop")
		}
		a.Close(context.Background())
	})
	readyClient := telemetry.HTTPClient(2 * time.Second)
	require.Eventually(t, func() bool {
		resp, err := readyClient.Get(r.url + "/readyz")
		if err != nil {
			return false
		}
		resp.Body.Close()
		return resp.StatusCode == 200
	}, 10*time.Second, 50*time.Millisecond)
	return r
}

func client(t *testing.T) *http.Client {
	jar, _ := cookiejar.New(nil)
	return &http.Client{Jar: jar, Timeout: 10 * time.Second}
}

func do(t *testing.T, c *http.Client, method, url, body string, hdr map[string]string) (int, map[string]any) {
	t.Helper()
	req, err := http.NewRequest(method, url, strings.NewReader(body))
	require.NoError(t, err)
	if body != "" {
		req.Header.Set("Content-Type", "application/json")
	}
	for k, v := range hdr {
		req.Header.Set(k, v)
	}
	resp, err := c.Do(req)
	require.NoError(t, err)
	defer resp.Body.Close()
	b, _ := io.ReadAll(resp.Body)
	var out map[string]any
	_ = json.Unmarshal(b, &out)
	return resp.StatusCode, out
}

const webhook = `{"version":"4","status":"firing","receiver":"infra-monitor","groupKey":"{}:{alertname=\"KubePodCrashLooping\"}",
"alerts":[{"status":"firing","fingerprint":"abc123","labels":{"alertname":"KubePodCrashLooping","cluster":"manager","namespace":"shop","severity":"critical","pod":"api-1"},
"annotations":{"summary":"Pod shop/api-1 is crash looping"},"startsAt":"2026-11-08T10:00:00Z","endsAt":"0001-01-01T00:00:00Z","generatorURL":"http://vmalert/x"},
{"status":"firing","fingerprint":"wd","labels":{"alertname":"Watchdog","severity":"none","cluster":"manager"},"annotations":{},"startsAt":"2026-11-08T10:00:00Z","endsAt":"0001-01-01T00:00:00Z"}]}`

func TestTwoReplicasEndToEnd(t *testing.T) {
	dsn := pgtest.NewURL(t)
	a := startReplica(t, dsn)
	b := startReplica(t, dsn)
	ctx := context.Background()

	_, err := auth.NewService(a.app.Cfg, a.app.DB, pgtest.Logger()).CreateUser(ctx, "admin@example.com", "Admin", domain.RoleAdmin, "admin-password")
	require.NoError(t, err)

	// health + openapi
	resp, err := http.Get(a.url + "/api/v1/openapi.json")
	require.NoError(t, err)
	require.Equal(t, 200, resp.StatusCode)
	resp.Body.Close()
	resp, err = http.Get(a.url + "/metrics")
	require.NoError(t, err)
	mb, _ := io.ReadAll(resp.Body)
	resp.Body.Close()
	require.Contains(t, string(mb), "go_goroutines")

	// unauthenticated
	anon := &http.Client{Timeout: 5 * time.Second}
	st, _ := do(t, anon, "GET", a.url+"/api/v1/incidents", "", nil)
	require.Equal(t, 401, st)

	// login on replica A, session valid on replica B (sessions live in Postgres)
	c := client(t)
	st, me := do(t, c, "POST", a.url+"/api/v1/auth/login", `{"email":"admin@example.com","password":"admin-password"}`, nil)
	require.Equal(t, 200, st, me)
	csrf := me["csrf_token"].(string)
	cookies := c.Jar.Cookies(mustURL(a.url))
	require.NotEmpty(t, cookies)
	cb := client(t)
	cb.Jar.SetCookies(mustURL(b.url), cookies)
	st, _ = do(t, cb, "GET", b.url+"/api/v1/me", "", nil)
	require.Equal(t, 200, st)

	// SSE on replica B
	sseCtx, sseCancel := context.WithCancel(ctx)
	defer sseCancel()
	req, _ := http.NewRequestWithContext(sseCtx, "GET", b.url+"/api/v1/events", nil)
	sseClient := &http.Client{Jar: cb.Jar}
	sresp, err := sseClient.Do(req)
	require.NoError(t, err)
	defer sresp.Body.Close()
	require.Equal(t, "text/event-stream", sresp.Header.Get("Content-Type"))
	events := make(chan string, 100)
	go func() {
		sc := bufio.NewScanner(sresp.Body)
		for sc.Scan() {
			if strings.HasPrefix(sc.Text(), "event: ") {
				events <- strings.TrimPrefix(sc.Text(), "event: ")
			}
		}
	}()
	time.Sleep(300 * time.Millisecond)

	// webhook auth
	st, _ = do(t, anon, "POST", a.url+"/api/v1/sources/alertmanager/webhook", webhook, map[string]string{"Authorization": "Bearer wrong"})
	require.Equal(t, 401, st)
	st, res := do(t, anon, "POST", a.url+"/api/v1/sources/alertmanager/webhook", webhook, map[string]string{"Authorization": "Bearer hook-secret"})
	require.Equal(t, 202, st, res)
	require.EqualValues(t, 1, res["heartbeats"])

	// timeline event reaches the SSE client connected to the other replica
	select {
	case ev := <-events:
		require.Equal(t, "timeline", ev)
	case <-time.After(10 * time.Second):
		t.Fatal("no SSE event on replica B")
	}

	var incID float64
	require.Eventually(t, func() bool {
		st, list := do(t, cb, "GET", b.url+"/api/v1/incidents?status=open", "", nil)
		if st != 200 {
			return false
		}
		items := list["items"].([]any)
		if len(items) != 1 {
			return false
		}
		inc := items[0].(map[string]any)
		incID = inc["id"].(float64)
		return inc["firing_count"].(float64) == 1 && inc["cluster"] == "manager"
	}, 10*time.Second, 100*time.Millisecond)

	// list filters are applied
	st, none := do(t, cb, "GET", b.url+"/api/v1/incidents?namespace=does-not-exist", "", nil)
	require.Equal(t, 200, st)
	require.EqualValues(t, 0, none["total"])
	_, byQ := do(t, cb, "GET", b.url+"/api/v1/incidents?q=crash", "", nil)
	require.EqualValues(t, 1, byQ["total"])
	_, byQ = do(t, cb, "GET", b.url+"/api/v1/incidents?q=nomatch-xyz", "", nil)
	require.EqualValues(t, 0, byQ["total"])

	// CSRF is required for cookie sessions
	id := itoa(int(incID))
	st, _ = do(t, c, "POST", a.url+"/api/v1/incidents/"+id+"/ack", "", nil)
	require.Equal(t, 403, st)
	st, inc := do(t, c, "POST", a.url+"/api/v1/incidents/"+id+"/ack", "", map[string]string{"X-CSRF-Token": csrf})
	require.Equal(t, 200, st, inc)
	require.Equal(t, "acknowledged", inc["status"])

	// API token with viewer role can read but not mutate
	st, tok := do(t, c, "POST", a.url+"/api/v1/tokens", `{"name":"ro","role":"viewer"}`, map[string]string{"X-CSRF-Token": csrf})
	require.Equal(t, 200, st, tok)
	bearer := map[string]string{"Authorization": "Bearer " + tok["token"].(string)}
	st, _ = do(t, anon, "GET", b.url+"/api/v1/incidents/"+id+"/timeline", "", bearer)
	require.Equal(t, 200, st)
	st, _ = do(t, anon, "POST", b.url+"/api/v1/incidents/"+id+"/resolve", "", bearer)
	require.Equal(t, 403, st)

	// status shows the heartbeat and exactly one scheduler lease across replicas
	require.Eventually(t, func() bool {
		_, sa := do(t, c, "GET", a.url+"/api/v1/status", "", nil)
		_, sb := do(t, cb, "GET", b.url+"/api/v1/status", "", nil)
		n := 0
		for _, s := range []map[string]any{sa, sb} {
			for _, l := range s["leases"].([]any) {
				if l == "scheduler" {
					n++
				}
			}
		}
		hb := sa["heartbeats"].([]any)
		return n == 1 && len(hb) == 1 && hb[0].(map[string]any)["state"] == "ok"
	}, 10*time.Second, 200*time.Millisecond)

	// resolved webhook resolves the incident
	resolved := strings.ReplaceAll(webhook, `"status":"firing"`, `"status":"resolved"`)
	st, _ = do(t, anon, "POST", a.url+"/api/v1/sources/alertmanager/webhook", resolved, map[string]string{"Authorization": "Bearer hook-secret"})
	require.Equal(t, 202, st)
	require.Eventually(t, func() bool {
		_, inc := do(t, c, "GET", a.url+"/api/v1/incidents/"+id, "", nil)
		return inc["status"] == "resolved"
	}, 10*time.Second, 100*time.Millisecond)

	// SPA fallback serves something for client routes
	resp, err = http.Get(a.url + "/incidents/1")
	require.NoError(t, err)
	resp.Body.Close()
	require.Equal(t, 200, resp.StatusCode)
}

func mustURL(s string) *urlT { u, _ := parseURL(s); return u }
