package gitops

import (
	"context"
	"crypto/sha1"
	"encoding/base64"
	"encoding/hex"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/config"
)

const values = `demo-api:
  image: ghcr.io/xonoko/demo-api:1.2.3
  resources:
    limits:
      memory: 64Mi
      cpu: 200m
    requests:
      memory: 32Mi
`

// fakeGitHub implements the parts of the contents API the executor uses.
type fakeGitHub struct {
	mu        sync.Mutex
	files     map[string]string // path -> content (branch main of lucasgalton/infra-argocd)
	commits   []map[string]any
	readOnly  bool
	conflict  bool
	lastToken string
}

func blobSHA(s string) string {
	h := sha1.Sum([]byte(s))
	return hex.EncodeToString(h[:])
}

func (f *fakeGitHub) handler(t *testing.T) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		f.mu.Lock()
		defer f.mu.Unlock()
		f.lastToken = r.Header.Get("Authorization")
		if r.Header.Get("Authorization") != "Bearer good-token" {
			w.WriteHeader(http.StatusUnauthorized)
			_, _ = w.Write([]byte(`{"message":"Bad credentials"}`))
			return
		}
		require.Equal(t, "application/vnd.github+json", r.Header.Get("Accept"))
		require.Equal(t, "2022-11-28", r.Header.Get("X-GitHub-Api-Version"))
		const repoPrefix = "/repos/lucasgalton/infra-argocd/"
		if !strings.HasPrefix(r.URL.Path, repoPrefix) {
			w.WriteHeader(http.StatusNotFound)
			_, _ = w.Write([]byte(`{"message":"Not Found"}`))
			return
		}
		rest := strings.TrimPrefix(r.URL.Path, repoPrefix)
		switch {
		case rest == "branches/main":
			_, _ = w.Write([]byte(`{"name":"main"}`))
		case strings.HasPrefix(rest, "contents/") && r.Method == http.MethodGet:
			require.Equal(t, "main", r.URL.Query().Get("ref"))
			p := strings.TrimPrefix(rest, "contents/")
			c, ok := f.files[p]
			if !ok {
				w.WriteHeader(http.StatusNotFound)
				_, _ = w.Write([]byte(`{"message":"Not Found"}`))
				return
			}
			enc := base64.StdEncoding.EncodeToString([]byte(c))
			// GitHub wraps base64 at 60 chars
			var wrapped strings.Builder
			for i := 0; i < len(enc); i += 60 {
				end := min(i+60, len(enc))
				wrapped.WriteString(enc[i:end] + "\n")
			}
			_ = json.NewEncoder(w).Encode(map[string]any{"type": "file", "sha": blobSHA(c), "content": wrapped.String(), "encoding": "base64", "size": len(c)})
		case strings.HasPrefix(rest, "contents/") && r.Method == http.MethodPut:
			if f.readOnly {
				w.WriteHeader(http.StatusForbidden)
				_, _ = w.Write([]byte(`{"message":"Resource not accessible by personal access token"}`))
				return
			}
			p := strings.TrimPrefix(rest, "contents/")
			var body map[string]any
			require.NoError(t, json.NewDecoder(r.Body).Decode(&body))
			cur, exists := f.files[p]
			sha, _ := body["sha"].(string)
			if f.conflict || (exists && sha != blobSHA(cur)) || (!exists && sha != "") {
				w.WriteHeader(http.StatusConflict)
				_, _ = w.Write([]byte(`{"message":"is at ` + blobSHA(cur) + ` but expected ` + sha + `"}`))
				return
			}
			raw, err := base64.StdEncoding.DecodeString(body["content"].(string))
			require.NoError(t, err)
			f.files[p] = string(raw)
			f.commits = append(f.commits, body)
			status := http.StatusOK
			if !exists {
				status = http.StatusCreated
			}
			w.WriteHeader(status)
			_ = json.NewEncoder(w).Encode(map[string]any{"commit": map[string]any{"sha": "c0ffee1234567890", "html_url": "https://github.com/lucasgalton/infra-argocd/commit/c0ffee1234567890"}})
		default:
			w.WriteHeader(http.StatusNotFound)
		}
	})
}

func newTest(t *testing.T, mutate ...func(*config.GitOpsConfig)) (*Executor, *fakeGitHub) {
	t.Helper()
	f := &fakeGitHub{files: map[string]string{"gitops/clusters/manager/values-demo.yaml": values}}
	srv := httptest.NewServer(f.handler(t))
	t.Cleanup(srv.Close)
	cfg := config.GitOpsConfig{Enabled: true, Repo: "lucasgalton/infra-argocd", Branch: "main", Token: "good-token", APIURL: srv.URL,
		AuthorName: "infra-monitor", AuthorEmail: "im@example.com"}
	for _, m := range mutate {
		m(&cfg)
	}
	e, err := New(cfg)
	require.NoError(t, err)
	return e, f
}

const valuesPath = "gitops/clusters/manager/values-demo.yaml"

func editParams() map[string]any {
	return map[string]any{
		"path": valuesPath, "message": "fix(demo-api): raise memory limit to 256Mi",
		"edits": []any{map[string]any{"old": "memory: 64Mi", "new": "memory: 256Mi"}},
	}
}

func TestEditsPrepareAndExecute(t *testing.T) {
	e, f := newTest(t)
	ctx := context.Background()
	require.NoError(t, e.Validate(ctx, editParams(), actions.Target{}))
	p, err := e.Prepare(ctx, editParams(), actions.Target{})
	require.NoError(t, err)
	require.True(t, p.DryRunOK, p.DryRun)
	require.Equal(t, "github.com/lucasgalton/infra-argocd@main:"+valuesPath, p.Target)
	require.Equal(t, "diff", p.PayloadFormat)
	require.Contains(t, p.Payload, "--- a/"+valuesPath)
	require.Contains(t, p.Payload, "+++ b/"+valuesPath)
	require.Contains(t, p.Payload, "-      memory: 64Mi")
	require.Contains(t, p.Payload, "+      memory: 256Mi")
	require.Contains(t, p.DryRun, "applies cleanly")
	require.Contains(t, p.Effect, "(+1 −1 lines)")
	require.Equal(t, blobSHA(values), p.Precondition)
	require.Equal(t, "medium", p.Risk)
	require.NotNil(t, p.RollbackAction)
	require.Contains(t, p.Rollback, "+      memory: 64Mi")

	out, err := e.ExecutePrepared(ctx, editParams(), actions.Target{}, p)
	require.NoError(t, err)
	require.Contains(t, out, "c0ffee123456")
	require.Contains(t, f.files[valuesPath], "memory: 256Mi")
	require.Contains(t, f.files[valuesPath], "memory: 32Mi", "only the targeted line changed")
	require.Len(t, f.commits, 1)
	require.Equal(t, "fix(demo-api): raise memory limit to 256Mi", f.commits[0]["message"])
	require.Equal(t, map[string]any{"name": "infra-monitor", "email": "im@example.com"}, f.commits[0]["committer"])
	require.Equal(t, "main", f.commits[0]["branch"])

	// rollback round-trip restores the original file
	rb := p.RollbackAction
	require.Equal(t, Type, rb.Type)
	rp, err := e.Prepare(ctx, rb.Params, actions.Target{})
	require.NoError(t, err)
	require.True(t, rp.DryRunOK, rp.DryRun)
	_, err = e.ExecutePrepared(ctx, rb.Params, actions.Target{}, rp)
	require.NoError(t, err)
	require.Equal(t, values, f.files[valuesPath])
}

func TestContentNewFileAndDiffMode(t *testing.T) {
	e, f := newTest(t)
	ctx := context.Background()
	newPath := "gitops/clusters/manager/values-new.yaml"
	params := map[string]any{"path": newPath, "message": "feat(new): add values", "content": "a: 1\n"}
	p, err := e.Prepare(ctx, params, actions.Target{})
	require.NoError(t, err)
	require.True(t, p.DryRunOK)
	require.Equal(t, "absent", p.Precondition)
	require.Contains(t, p.Operation, "create file")
	require.Contains(t, p.Payload, "--- /dev/null")
	require.Nil(t, p.RollbackAction)
	_, err = e.ExecutePrepared(ctx, params, actions.Target{}, p)
	require.NoError(t, err)
	require.Equal(t, "a: 1\n", f.files[newPath])

	// diff mode: the agent's unified diff is applied strictly
	next := strings.Replace(values, "cpu: 200m", "cpu: 500m", 1)
	diff := unifiedDiff(valuesPath, values, next, true)
	dp := map[string]any{"path": valuesPath, "message": "fix(demo-api): more cpu", "diff": diff}
	p, err = e.Prepare(ctx, dp, actions.Target{})
	require.NoError(t, err)
	require.True(t, p.DryRunOK, p.DryRun)
	_, err = e.ExecutePrepared(ctx, dp, actions.Target{}, p)
	require.NoError(t, err)
	require.Equal(t, next, f.files[valuesPath])

	// a diff that no longer matches the file does not apply
	p, err = e.Prepare(ctx, dp, actions.Target{})
	require.NoError(t, err)
	require.False(t, p.DryRunOK)
	require.Contains(t, p.DryRun, "does not match")
}

func TestEditNotFoundOrAmbiguous(t *testing.T) {
	e, _ := newTest(t)
	ctx := context.Background()
	p, err := e.Prepare(ctx, map[string]any{"path": valuesPath, "message": "fix(x): y",
		"edits": []any{map[string]any{"old": "memory: 1Gi", "new": "memory: 2Gi"}}}, actions.Target{})
	require.NoError(t, err)
	require.False(t, p.DryRunOK)
	require.Contains(t, p.DryRun, "not found")

	p, err = e.Prepare(ctx, map[string]any{"path": valuesPath, "message": "fix(x): y",
		"edits": []any{map[string]any{"old": "memory:", "new": "mem:"}}}, actions.Target{})
	require.NoError(t, err)
	require.False(t, p.DryRunOK)
	require.Contains(t, p.DryRun, "ambiguous")

	_, err = e.Execute(ctx, map[string]any{"path": valuesPath, "message": "fix(x): y",
		"edits": []any{map[string]any{"old": "memory:", "new": "mem:"}}}, actions.Target{})
	require.Error(t, err, "Execute refuses a change that does not apply")
}

func TestValidation(t *testing.T) {
	e, _ := newTest(t, func(c *config.GitOpsConfig) { c.PathPrefixes = []string{"gitops/clusters/"} })
	ctx := context.Background()
	base := func(path, msg string) map[string]any {
		return map[string]any{"path": path, "message": msg, "content": "x"}
	}
	for _, bad := range []string{"../etc/passwd", "/etc/passwd", "gitops/clusters/../../x", "gitops\\clusters\\x", ".git/config", "docs/readme.md", "gitops/clusters/./x"} {
		require.Error(t, e.Validate(ctx, base(bad, "fix(x): y"), actions.Target{}), bad)
	}
	for _, bad := range []string{
		"fix(x): change suggested by the AI", "chore: Co-authored-by: Claude", "fix(llm): tweak", "two\nlines", "x",
		"fix(demo-api): raise memory limit to 256Mi because the cache needs a lot more room today",
		"fix(x): agent proposal", "feat: GPT tuned",
	} {
		require.Error(t, e.Validate(ctx, base(valuesPath, bad), actions.Target{}), bad)
	}
	for _, ok := range []string{"fix(vmagent): raise scrape interval", "fix(demo-api): raise memory limit to 256Mi", "feat(infra-monitor): enable telegram bot"} {
		require.NoError(t, e.Validate(ctx, base("gitops/clusters/manager/x.yaml", ok), actions.Target{}), ok)
	}
	// exactly one mode
	require.Error(t, e.Validate(ctx, map[string]any{"path": valuesPath, "message": "fix(x): y"}, actions.Target{}))
	require.Error(t, e.Validate(ctx, map[string]any{"path": valuesPath, "message": "fix(x): y", "content": "a", "diff": "b"}, actions.Target{}))
}

func TestPreconditionChanged(t *testing.T) {
	e, f := newTest(t)
	ctx := context.Background()
	p, err := e.Prepare(ctx, editParams(), actions.Target{})
	require.NoError(t, err)
	// someone pushes to the file after the approval was given
	f.mu.Lock()
	f.files[valuesPath] = values + "# touched\n"
	f.mu.Unlock()
	_, err = e.ExecutePrepared(ctx, editParams(), actions.Target{}, p)
	require.ErrorIs(t, err, actions.ErrPreconditionChanged)
	require.Len(t, f.commits, 0)

	// a conflict reported by GitHub at write time is a precondition change too
	p, err = e.Prepare(ctx, editParams(), actions.Target{})
	require.NoError(t, err)
	f.conflict = true
	_, err = e.ExecutePrepared(ctx, editParams(), actions.Target{}, p)
	require.ErrorIs(t, err, actions.ErrPreconditionChanged)
}

func TestRollbackFallsBackToContent(t *testing.T) {
	e, f := newTest(t)
	ctx := context.Background()
	// "memory: 32Mi" will appear twice after the edit, so reversing by edits is ambiguous
	params := map[string]any{"path": valuesPath, "message": "fix(demo-api): same limit as request",
		"edits": []any{map[string]any{"old": "memory: 64Mi", "new": "memory: 32Mi"}}}
	p, err := e.Prepare(ctx, params, actions.Target{})
	require.NoError(t, err)
	require.Equal(t, values, p.RollbackAction.Params["content"])
	_, err = e.ExecutePrepared(ctx, params, actions.Target{}, p)
	require.NoError(t, err)
	rp, err := e.Prepare(ctx, p.RollbackAction.Params, actions.Target{})
	require.NoError(t, err)
	_, err = e.ExecutePrepared(ctx, p.RollbackAction.Params, actions.Target{}, rp)
	require.NoError(t, err)
	require.Equal(t, values, f.files[valuesPath])
}

func TestAuthErrorsAndTokenRedaction(t *testing.T) {
	ctx := context.Background()
	e, _ := newTest(t, func(c *config.GitOpsConfig) { c.Token = "bad-token-secret" })
	_, err := e.Prepare(ctx, editParams(), actions.Target{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "401")
	require.NotContains(t, err.Error(), "bad-token-secret")

	e, f := newTest(t)
	f.readOnly = true
	p, err := e.Prepare(ctx, editParams(), actions.Target{})
	require.NoError(t, err)
	_, err = e.ExecutePrepared(ctx, editParams(), actions.Target{}, p)
	require.Error(t, err)
	require.Contains(t, err.Error(), "403")
	require.Contains(t, err.Error(), "Contents read/write")
	require.False(t, errors.Is(err, actions.ErrPreconditionChanged))

	// unknown repository is reported as such, not as a missing file
	e, _ = newTest(t, func(c *config.GitOpsConfig) { c.Repo = "lucasgalton/nope" })
	_, err = e.Prepare(ctx, editParams(), actions.Target{})
	require.Error(t, err)
	require.Contains(t, err.Error(), "not found, or the token has no access")

	_, err = New(config.GitOpsConfig{Repo: "no-slash", Token: "x"})
	require.Error(t, err)
	_, err = New(config.GitOpsConfig{Repo: "a/b"})
	require.Error(t, err)
}

func TestHighRiskPathsAndRegistry(t *testing.T) {
	e, f := newTest(t)
	f.files["gitops/charts/infrastructure/cert-manager/values.yaml"] = "replicas: 1\n"
	p, err := e.Prepare(context.Background(), map[string]any{"path": "gitops/charts/infrastructure/cert-manager/values.yaml",
		"message": "fix(cert-manager): two replicas", "edits": []any{map[string]any{"old": "replicas: 1", "new": "replicas: 2"}}}, actions.Target{})
	require.NoError(t, err)
	require.Equal(t, "high", p.Risk)

	// the schema compiles in the registry and is never auto-approved
	reg := actions.NewRegistry(nil, []string{Type})
	require.NoError(t, reg.Register(e))
	require.NoError(t, reg.Validate(context.Background(), Type, editParams(), actions.Target{}))
	require.Error(t, reg.Validate(context.Background(), Type, map[string]any{"path": valuesPath, "message": "x", "extra": 1}, actions.Target{}))
	require.True(t, e.RequiresApproval())
}

func TestApplyUnifiedDiffErrors(t *testing.T) {
	_, err := applyUnifiedDiff("a\nb\n", "@@ -1,1 +1,1 @@\n-x\n+y\n")
	require.ErrorContains(t, err, "does not match")
	_, err = applyUnifiedDiff("a\n", "no hunks here")
	require.ErrorContains(t, err, "no hunks")
	_, err = applyUnifiedDiff("a\n", "--- a/x\n+++ b/x\n@@ -1 +1 @@\n-a\n+b\n--- a/y\n+++ b/y\n@@ -1 +1 @@\n-c\n+d\n")
	require.ErrorContains(t, err, "several files")
	_, err = applyUnifiedDiff("a\nb\n", "@@ -1,2 +1,2 @@\n-a\n")
	require.ErrorContains(t, err, "truncated")
	out, err := applyUnifiedDiff("a\nb\nc\n", "@@ -2,1 +2,2 @@\n-b\n+B\n+B2\n")
	require.NoError(t, err)
	require.Equal(t, "a\nB\nB2\nc\n", out)
}
