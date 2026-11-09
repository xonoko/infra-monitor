// Package gitops implements the "gitops.change" action: a commit to the GitOps repository
// (GitHub contents API), proposed by the agent and executed only after a precise human approval.
package gitops

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path"
	"regexp"
	"strings"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/config"
)

// Type is the action type.
const Type = "gitops.change"

// Executor implements actions.Executor, actions.Preparer, actions.Supervised and actions.PreconditionExecutor.
type Executor struct {
	cfg config.GitOpsConfig
	gh  *githubClient
}

var (
	_ actions.Executor             = (*Executor)(nil)
	_ actions.Preparer             = (*Executor)(nil)
	_ actions.Supervised           = (*Executor)(nil)
	_ actions.PreconditionExecutor = (*Executor)(nil)
)

// New builds the executor. cfg.Repo is "owner/name".
func New(cfg config.GitOpsConfig) (*Executor, error) {
	owner, repo, ok := strings.Cut(cfg.Repo, "/")
	if !ok || owner == "" || repo == "" || strings.Contains(repo, "/") {
		return nil, fmt.Errorf("actions.gitops.repo must be owner/name, got %q", cfg.Repo)
	}
	if cfg.Token == "" {
		return nil, errors.New("actions.gitops.token (or token_file) is required")
	}
	if cfg.Branch == "" {
		cfg.Branch = "main"
	}
	if cfg.APIURL == "" {
		cfg.APIURL = "https://api.github.com"
	}
	if cfg.AuthorName == "" {
		cfg.AuthorName = "infra-monitor"
	}
	if cfg.AuthorEmail == "" {
		cfg.AuthorEmail = "infra-monitor@users.noreply.github.com"
	}
	return &Executor{cfg: cfg, gh: &githubClient{
		api: strings.TrimRight(cfg.APIURL, "/"), owner: owner, repo: repo, branch: cfg.Branch, token: cfg.Token, tokenFile: cfg.TokenFile,
		authorName: cfg.AuthorName, authorEmail: cfg.AuthorEmail, http: newHTTPClient(),
	}}, nil
}

func (e *Executor) Type() string           { return Type }
func (e *Executor) Risk() string           { return "medium" }
func (e *Executor) RequiresApproval() bool { return true }

func (e *Executor) Description() string {
	return fmt.Sprintf("Commit a change to the GitOps repository %s (branch %s), which Argo CD then syncs to the cluster. "+
		"Use it when the target is managed by Argo CD (annotation argocd.argoproj.io/tracking-id or an Argo instance label): "+
		"a live kubectl change would be reverted by selfHeal. Find the file that defines the value (read the Argo CD Application's "+
		"source path/values files with the kubernetes tools), keep the change minimal, and prefer `edits` (exact old/new snippets, "+
		"each old snippet unique in the file). The message is one conventional line, e.g. \"fix(demo-api): raise memory limit to 256Mi\". "+
		"Requires human approval of the exact diff.", e.cfg.Repo, e.cfg.Branch)
}

func (e *Executor) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["path", "message"],
  "properties": {
    "path": {"type": "string", "minLength": 1, "description": "File path in the repository, relative to its root, e.g. gitops/clusters/manager/values-demo.yaml"},
    "message": {"type": "string", "minLength": 3, "maxLength": 72, "description": "One-line conventional commit message, e.g. fix(demo-api): raise memory limit to 256Mi"},
    "edits": {"type": "array", "minItems": 1, "description": "Exact replacements; each old must occur exactly once in the current file",
      "items": {"type": "object", "additionalProperties": false, "required": ["old", "new"],
        "properties": {"old": {"type": "string", "minLength": 1}, "new": {"type": "string"}}}},
    "content": {"type": "string", "description": "Full new file content (new file or complete rewrite). Use edits for small changes."},
    "diff": {"type": "string", "description": "Unified diff of this single file (applied strictly)"}
  }
}`)
}

type edit struct {
	Old string `json:"old"`
	New string `json:"new"`
}

type params struct {
	Path    string  `json:"path"`
	Message string  `json:"message"`
	Edits   []edit  `json:"edits,omitempty"`
	Content *string `json:"content,omitempty"`
	Diff    string  `json:"diff,omitempty"`
}

func parseParams(m map[string]any) (params, error) {
	var p params
	b, err := json.Marshal(m)
	if err != nil {
		return p, err
	}
	if err := json.Unmarshal(b, &p); err != nil {
		return p, fmt.Errorf("invalid params: %w", err)
	}
	return p, nil
}

// aiWords: commits never mention AI (Lucas's rule).
var aiWords = regexp.MustCompile(`(?i)(\b(ai|a\.i\.|llm|llms|agent|agents|claude|anthropic|openai|gpt|chatgpt)\b|co-?authored)`)

// allowedPath checks that p is a clean relative path under the allowed prefixes.
func (e *Executor) allowedPath(p string) error {
	if p == "" {
		return errors.New("path is required")
	}
	clean := path.Clean(p)
	if strings.HasPrefix(p, "/") || clean != p || clean == "." || strings.HasPrefix(clean, "../") || clean == ".." || strings.Contains(p, "\\") {
		return fmt.Errorf("path %q must be a clean relative path inside the repository", p)
	}
	for _, seg := range strings.Split(clean, "/") {
		if seg == ".." || seg == ".git" {
			return fmt.Errorf("path %q is not allowed", p)
		}
	}
	if len(e.cfg.PathPrefixes) > 0 {
		ok := false
		for _, pre := range e.cfg.PathPrefixes {
			pre = strings.TrimSuffix(pre, "/")
			if clean == pre || strings.HasPrefix(clean, pre+"/") {
				ok = true
				break
			}
		}
		if !ok {
			return fmt.Errorf("path %q is outside the allowed prefixes %v", p, e.cfg.PathPrefixes)
		}
	}
	return nil
}

func (e *Executor) validate(p params) error {
	if err := e.allowedPath(p.Path); err != nil {
		return err
	}
	msg := p.Message
	if strings.ContainsAny(msg, "\r\n") {
		return errors.New("commit message must be a single line")
	}
	if l := len([]rune(strings.TrimSpace(msg))); l < 3 || l > 72 {
		return fmt.Errorf("commit message must be 3-72 characters (got %d)", l)
	}
	if aiWords.MatchString(msg) {
		return fmt.Errorf("commit message %q must not mention AI or the agent", msg)
	}
	modes := 0
	if len(p.Edits) > 0 {
		modes++
	}
	if p.Content != nil {
		modes++
	}
	if p.Diff != "" {
		modes++
	}
	if modes != 1 {
		return errors.New("exactly one of edits, content or diff is required")
	}
	for i, ed := range p.Edits {
		if ed.Old == "" {
			return fmt.Errorf("edits[%d].old must not be empty", i)
		}
		if ed.Old == ed.New {
			return fmt.Errorf("edits[%d] changes nothing", i)
		}
	}
	return nil
}

// Validate checks params and policy without contacting GitHub.
func (e *Executor) Validate(_ context.Context, m map[string]any, _ actions.Target) error {
	p, err := parseParams(m)
	if err != nil {
		return err
	}
	return e.validate(p)
}

func (e *Executor) Summary(m map[string]any, _ actions.Target) string {
	p, _ := parseParams(m)
	return fmt.Sprintf("commit to %s@%s: %s (%s)", e.cfg.Repo, e.cfg.Branch, p.Path, p.Message)
}

// apply computes the new content from the current one.
func apply(p params, cur string, exists bool) (string, error) {
	switch {
	case len(p.Edits) > 0:
		if !exists {
			return "", fmt.Errorf("%s does not exist on the branch: use content to create it", p.Path)
		}
		out := cur
		for i, ed := range p.Edits {
			switch n := strings.Count(out, ed.Old); n {
			case 0:
				return "", fmt.Errorf("edits[%d]: old text not found in %s: %q", i, p.Path, ed.Old)
			case 1:
				out = strings.Replace(out, ed.Old, ed.New, 1)
			default:
				return "", fmt.Errorf("edits[%d]: old text is ambiguous (%d occurrences in %s); include more context", i, n, p.Path)
			}
		}
		return out, nil
	case p.Content != nil:
		return *p.Content, nil
	default:
		if !exists {
			cur = ""
		}
		return applyUnifiedDiff(cur, p.Diff)
	}
}

func (e *Executor) target(p params) string {
	return fmt.Sprintf("github.com/%s@%s:%s", e.cfg.Repo, e.cfg.Branch, p.Path)
}

var highRiskPath = regexp.MustCompile(`(?i)(infrastructure|argocd|kube-system|cert-manager|authentik)`)

// Prepare reads the current file, applies the change in memory and renders the exact diff.
func (e *Executor) Prepare(ctx context.Context, m map[string]any, _ actions.Target) (*actions.Prepared, error) {
	p, err := parseParams(m)
	if err != nil {
		return nil, err
	}
	if err := e.validate(p); err != nil {
		return nil, err
	}
	cur, err := e.gh.get(ctx, p.Path)
	exists := true
	if errors.Is(err, errAbsent) {
		exists, err = false, nil
	}
	if err != nil {
		return nil, err
	}
	risk := "medium"
	if highRiskPath.MatchString(p.Path) {
		risk = "high"
	}
	pr := &actions.Prepared{
		Target:        e.target(p),
		Operation:     fmt.Sprintf("commit to %s (%s)", e.cfg.Branch, map[bool]string{true: "update file", false: "create file"}[exists]),
		PayloadFormat: "diff",
		Risk:          risk,
		Precondition:  "absent",
	}
	if exists {
		pr.Precondition = cur.sha
	}
	next, aerr := apply(p, cur.content, exists)
	if aerr != nil {
		pr.Payload = describeRequested(p)
		pr.DryRun = "does not apply: " + aerr.Error()
		pr.DryRunOK = false
		pr.Effect = "nothing (the change does not apply to the current file)"
		return pr, nil
	}
	if exists && next == cur.content {
		pr.Payload = describeRequested(p)
		pr.DryRun = "the change is already present: nothing to commit"
		pr.DryRunOK = false
		pr.Effect = "nothing"
		return pr, nil
	}
	diff := unifiedDiff(p.Path, cur.content, next, exists)
	added, removed := diffStats(diff)
	pr.Payload = diff
	pr.DryRun = diff + "\napplies cleanly to " + pr.Target + " (blob " + shortSHA(pr.Precondition) + ")"
	pr.DryRunOK = true
	pr.Effect = fmt.Sprintf("commit '%s' to %s@%s changing %s (+%d −%d lines); Argo CD will sync it", p.Message, e.cfg.Repo, e.cfg.Branch, p.Path, added, removed)
	if exists {
		pr.Rollback = unifiedDiff(p.Path, next, cur.content, true)
		pr.RollbackAction = &actions.RollbackAction{Type: Type, Params: rollbackParams(p, cur.content, next)}
	} else {
		pr.Rollback = "delete " + p.Path + " (new file); the contents API cannot delete through this action, revert the commit"
	}
	return pr, nil
}

func describeRequested(p params) string {
	switch {
	case len(p.Edits) > 0:
		var b strings.Builder
		for i, ed := range p.Edits {
			fmt.Fprintf(&b, "edit %d:\n--- old\n%s\n+++ new\n%s\n", i+1, ed.Old, ed.New)
		}
		return b.String()
	case p.Content != nil:
		return *p.Content
	default:
		return p.Diff
	}
}

// rollbackParams builds a gitops.change that restores the previous content.
func rollbackParams(p params, old, next string) map[string]any {
	msg := "revert: " + p.Message
	if r := []rune(msg); len(r) > 72 {
		msg = string(r[:72])
	}
	out := map[string]any{"path": p.Path, "message": msg}
	// Reverse edits when every new snippet is unique in the new content; otherwise restore the whole file.
	if len(p.Edits) > 0 {
		rev := make([]any, 0, len(p.Edits))
		ok := true
		for i := len(p.Edits) - 1; i >= 0; i-- {
			ed := p.Edits[i]
			if ed.New == "" || strings.Count(next, ed.New) != 1 {
				ok = false
				break
			}
			rev = append(rev, map[string]any{"old": ed.New, "new": ed.Old})
		}
		if ok {
			out["edits"] = rev
			return out
		}
	}
	out["content"] = old
	return out
}

func shortSHA(s string) string {
	if len(s) > 12 {
		return s[:12]
	}
	return s
}

// ExecutePrepared commits only if the file is still the one that was approved.
func (e *Executor) ExecutePrepared(ctx context.Context, m map[string]any, _ actions.Target, prep *actions.Prepared) (string, error) {
	p, err := parseParams(m)
	if err != nil {
		return "", err
	}
	if err := e.validate(p); err != nil {
		return "", err
	}
	cur, err := e.gh.get(ctx, p.Path)
	exists := true
	if errors.Is(err, errAbsent) {
		exists, err = false, nil
	}
	if err != nil {
		return "", err
	}
	now := "absent"
	if exists {
		now = cur.sha
	}
	if prep == nil || prep.Precondition != now {
		want := "<none>"
		if prep != nil {
			want = prep.Precondition
		}
		return "", fmt.Errorf("%w: %s is at %s, approved %s", actions.ErrPreconditionChanged, p.Path, shortSHA(now), shortSHA(want))
	}
	next, err := apply(p, cur.content, exists)
	if err != nil {
		return "", err
	}
	res, err := e.gh.put(ctx, p.Path, next, cur.sha, strings.TrimSpace(p.Message))
	if err != nil {
		return "", err
	}
	return fmt.Sprintf("committed %s to %s@%s: %s", shortSHA(res.SHA), e.cfg.Repo, e.cfg.Branch, res.URL), nil
}

// Execute prepares and commits (used when no prepared state is available).
func (e *Executor) Execute(ctx context.Context, m map[string]any, t actions.Target) (string, error) {
	prep, err := e.Prepare(ctx, m, t)
	if err != nil {
		return "", err
	}
	if !prep.DryRunOK {
		return "", errors.New(prep.DryRun)
	}
	return e.ExecutePrepared(ctx, m, t, prep)
}
