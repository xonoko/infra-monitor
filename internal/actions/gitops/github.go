package gitops

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"sync"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// errAbsent means the file does not exist on the branch.
var errAbsent = errors.New("file absent")

type githubClient struct {
	// tokenFile is re-read (at most every 30s) so rotated tokens (e.g. External Secrets generators) are picked up.
	tokenFile                       string
	tokenMu                         sync.Mutex
	tokenAt                         time.Time
	api, owner, repo, branch, token string
	authorName, authorEmail         string
	http                            *http.Client
}

type fileState struct {
	content string
	sha     string // blob SHA, "" when absent
}

func (g *githubClient) contentsURL(path string) string {
	segs := strings.Split(path, "/")
	for i, s := range segs {
		segs[i] = url.PathEscape(s)
	}
	return fmt.Sprintf("%s/repos/%s/%s/contents/%s", g.api, url.PathEscape(g.owner), url.PathEscape(g.repo), strings.Join(segs, "/"))
}

func (g *githubClient) do(ctx context.Context, method, u string, body any) (int, []byte, error) {
	var rd io.Reader
	if body != nil {
		b, err := json.Marshal(body)
		if err != nil {
			return 0, nil, err
		}
		rd = bytes.NewReader(b)
	}
	req, err := http.NewRequestWithContext(ctx, method, u, rd)
	if err != nil {
		return 0, nil, err
	}
	req.Header.Set("Authorization", "Bearer "+g.currentToken())
	req.Header.Set("Accept", "application/vnd.github+json")
	req.Header.Set("X-GitHub-Api-Version", "2022-11-28")
	req.Header.Set("User-Agent", "infra-monitor")
	if body != nil {
		req.Header.Set("Content-Type", "application/json")
	}
	resp, err := g.http.Do(req)
	if err != nil {
		return 0, nil, g.redact(err)
	}
	defer func() { _ = resp.Body.Close() }()
	b, err := io.ReadAll(io.LimitReader(resp.Body, 8<<20))
	return resp.StatusCode, b, err
}

// redact removes the token from error messages.
func (g *githubClient) redact(err error) error {
	if err == nil || g.token == "" || !strings.Contains(err.Error(), g.token) {
		return err
	}
	return errors.New(strings.ReplaceAll(err.Error(), g.token, "<redacted>"))
}

func githubMessage(b []byte) string {
	var m struct {
		Message string `json:"message"`
	}
	if json.Unmarshal(b, &m) == nil && m.Message != "" {
		return m.Message
	}
	return strings.TrimSpace(string(b))
}

func (g *githubClient) authError(status int, b []byte) error {
	switch status {
	case http.StatusUnauthorized:
		return fmt.Errorf("github token rejected (401): check actions.gitops token")
	case http.StatusForbidden:
		return fmt.Errorf("github token lacks permission on %s/%s (403: %s): it needs Contents read/write", g.owner, g.repo, githubMessage(b))
	}
	return nil
}

// get reads a file on the branch (errAbsent when it does not exist).
func (g *githubClient) get(ctx context.Context, path string) (fileState, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "github.get_contents", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("github.repo", g.owner+"/"+g.repo), attribute.String("github.path", path)))
	defer span.End()
	u := g.contentsURL(path) + "?ref=" + url.QueryEscape(g.branch)
	status, b, err := g.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		telemetry.RecordError(span, err)
		return fileState{}, err
	}
	if aerr := g.authError(status, b); aerr != nil {
		telemetry.RecordError(span, aerr)
		return fileState{}, aerr
	}
	if status == http.StatusNotFound {
		// Absent file, or repository/branch not visible: tell them apart.
		if err := g.checkRepo(ctx); err != nil {
			telemetry.RecordError(span, err)
			return fileState{}, err
		}
		return fileState{}, errAbsent
	}
	if status != http.StatusOK {
		err := fmt.Errorf("github get %s: HTTP %d: %s", path, status, githubMessage(b))
		telemetry.RecordError(span, err)
		return fileState{}, err
	}
	var f struct {
		Type     string `json:"type"`
		SHA      string `json:"sha"`
		Content  string `json:"content"`
		Encoding string `json:"encoding"`
		Size     int    `json:"size"`
	}
	if err := json.Unmarshal(b, &f); err != nil {
		return fileState{}, fmt.Errorf("github get %s: %w", path, err)
	}
	if f.Type != "" && f.Type != "file" {
		return fileState{}, fmt.Errorf("%s is a %s, not a file", path, f.Type)
	}
	if f.Encoding != "base64" || (f.Content == "" && f.Size > 0) {
		return fileState{}, fmt.Errorf("%s is too large for the contents API (%d bytes)", path, f.Size)
	}
	raw, err := base64.StdEncoding.DecodeString(strings.ReplaceAll(f.Content, "\n", ""))
	if err != nil {
		return fileState{}, fmt.Errorf("decode %s: %w", path, err)
	}
	return fileState{content: string(raw), sha: f.SHA}, nil
}

func (g *githubClient) checkRepo(ctx context.Context) error {
	u := fmt.Sprintf("%s/repos/%s/%s/branches/%s", g.api, url.PathEscape(g.owner), url.PathEscape(g.repo), url.PathEscape(g.branch))
	status, b, err := g.do(ctx, http.MethodGet, u, nil)
	if err != nil {
		return err
	}
	if aerr := g.authError(status, b); aerr != nil {
		return aerr
	}
	if status == http.StatusNotFound {
		return fmt.Errorf("repository %s/%s or branch %s not found, or the token has no access to it", g.owner, g.repo, g.branch)
	}
	return nil
}

type commitResult struct {
	SHA, URL string
}

// put creates or updates a file. sha must be the current blob SHA ("" for a new file).
func (g *githubClient) put(ctx context.Context, path, content, sha, message string) (commitResult, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "github.put_contents", trace.WithSpanKind(trace.SpanKindClient),
		trace.WithAttributes(attribute.String("github.repo", g.owner+"/"+g.repo), attribute.String("github.path", path)))
	defer span.End()
	body := map[string]any{
		"message":   message,
		"content":   base64.StdEncoding.EncodeToString([]byte(content)),
		"branch":    g.branch,
		"committer": map[string]string{"name": g.authorName, "email": g.authorEmail},
	}
	if sha != "" {
		body["sha"] = sha
	}
	status, b, err := g.do(ctx, http.MethodPut, g.contentsURL(path), body)
	if err != nil {
		telemetry.RecordError(span, err)
		return commitResult{}, err
	}
	if aerr := g.authError(status, b); aerr != nil {
		telemetry.RecordError(span, aerr)
		return commitResult{}, aerr
	}
	switch status {
	case http.StatusOK, http.StatusCreated:
	case http.StatusConflict, http.StatusUnprocessableEntity:
		err := fmt.Errorf("%w: github %d: %s", actions.ErrPreconditionChanged, status, githubMessage(b))
		telemetry.RecordError(span, err)
		return commitResult{}, err
	default:
		err := fmt.Errorf("github put %s: HTTP %d: %s", path, status, githubMessage(b))
		telemetry.RecordError(span, err)
		return commitResult{}, err
	}
	var r struct {
		Commit struct {
			SHA     string `json:"sha"`
			HTMLURL string `json:"html_url"`
		} `json:"commit"`
	}
	if err := json.Unmarshal(b, &r); err != nil {
		return commitResult{}, fmt.Errorf("github put %s: %w", path, err)
	}
	return commitResult{SHA: r.Commit.SHA, URL: r.Commit.HTMLURL}, nil
}

func newHTTPClient() *http.Client { return telemetry.HTTPClient(30 * time.Second) }

// currentToken returns the token, re-reading token_file when it may have rotated.
func (g *githubClient) currentToken() string {
	if g.tokenFile == "" {
		return g.token
	}
	g.tokenMu.Lock()
	defer g.tokenMu.Unlock()
	if time.Since(g.tokenAt) < 30*time.Second {
		return g.token
	}
	if b, err := os.ReadFile(g.tokenFile); err == nil {
		if t := strings.TrimSpace(string(b)); t != "" {
			g.token = t
		}
	}
	g.tokenAt = time.Now()
	return g.token
}
