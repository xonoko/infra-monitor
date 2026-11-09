package gitops

import (
	"context"
	"errors"
	"fmt"
	"path"
	"sort"
	"strings"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/xonoko/infra-monitor/internal/redact"
)

var applicationGVR = schema.GroupVersionResource{Group: "argoproj.io", Version: "v1alpha1", Resource: "applications"}

const (
	maxApps      = 3
	maxFiles     = 4
	maxFileBytes = 6000
)

// Sources tells the agent which files in the GitOps repo configure a namespace: it reads the Argo CD
// Applications deploying there and the repo files they use (chart values and helm valueFiles).
// The agent's own tools can't do this reliably (large Application lists, no repo access).
type Sources struct {
	exec    *Executor
	dynamic func(cluster string) (dynamic.Interface, error)

	mu    sync.Mutex
	at    time.Time
	apps  []unstructured.Unstructured
	err   error
	ttl   time.Duration
	files map[string]fileEntry
}

type fileEntry struct {
	content string
	err     error
	at      time.Time
}

// NewSources reads Applications through dyn (the actions.gitops.argocd_cluster client).
func NewSources(e *Executor, dyn func(cluster string) (dynamic.Interface, error)) *Sources {
	return &Sources{exec: e, dynamic: dyn, ttl: time.Minute, files: map[string]fileEntry{}}
}

// Context describes the GitOps source of namespace ns in cluster ("" when no Application targets it).
func (s *Sources) Context(ctx context.Context, cluster, ns string) (string, error) {
	if ns == "" {
		return "", nil
	}
	apps, err := s.applications(ctx)
	if err != nil {
		return "", err
	}
	matched := matchApps(apps, cluster, ns)
	if len(matched) == 0 {
		return "", nil
	}
	var b strings.Builder
	fmt.Fprintf(&b, "Namespace %s is deployed by Argo CD from the GitOps repo %s (branch %s). Fix configuration with gitops.change on these files "+
		"(paths are relative to the repo root); live k8s.* changes to these objects are reverted on sync and are rejected.\n",
		ns, s.exec.cfg.Repo, s.exec.cfg.Branch)
	files := 0
	for _, app := range matched {
		selfHeal, _, _ := unstructured.NestedBool(app.Object, "spec", "syncPolicy", "automated", "selfHeal")
		fmt.Fprintf(&b, "\nArgo CD Application %s (selfHeal=%v):\n", app.GetName(), selfHeal)
		for _, f := range s.appFiles(app) {
			if f.inline != "" {
				fmt.Fprintf(&b, "- %s\n", f.inline)
				continue
			}
			if files >= maxFiles {
				fmt.Fprintf(&b, "- %s: %s (not shown)\n", f.role, f.path)
				continue
			}
			files++
			content, err := s.read(ctx, f.path)
			switch {
			case errors.Is(err, errAbsent):
				fmt.Fprintf(&b, "- %s: %s (file does not exist yet; create it with content)\n", f.role, f.path)
			case err != nil:
				fmt.Fprintf(&b, "- %s: %s (could not read: %s)\n", f.role, f.path, redact.Text(err.Error()))
			default:
				suffix := ""
				if len(content) > maxFileBytes {
					content, suffix = content[:maxFileBytes], "\n... (truncated)"
				}
				fmt.Fprintf(&b, "- %s: %s\n```yaml\n%s%s\n```\n", f.role, f.path, strings.TrimRight(redact.Text(content), "\n"), suffix)
			}
		}
	}
	return b.String(), nil
}

func (s *Sources) applications(ctx context.Context) ([]unstructured.Unstructured, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	if !s.at.IsZero() && time.Since(s.at) < s.ttl {
		return s.apps, s.err
	}
	s.apps, s.err = nil, nil
	dyn, err := s.dynamic(s.exec.cfg.ArgoCDCluster)
	if err == nil {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		var list *unstructured.UnstructuredList
		list, err = dyn.Resource(applicationGVR).Namespace(s.exec.cfg.ArgoCDNamespace).List(cctx, metav1.ListOptions{})
		cancel()
		if err == nil {
			s.apps = list.Items
		}
	}
	if err != nil {
		s.err = fmt.Errorf("list Argo CD applications in %s: %w", s.exec.cfg.ArgoCDNamespace, err)
	}
	s.at = time.Now()
	return s.apps, s.err
}

func (s *Sources) read(ctx context.Context, p string) (string, error) {
	s.mu.Lock()
	if e, ok := s.files[p]; ok && time.Since(e.at) < s.ttl {
		s.mu.Unlock()
		return e.content, e.err
	}
	s.mu.Unlock()
	cctx, cancel := context.WithTimeout(ctx, 10*time.Second)
	f, err := s.exec.gh.get(cctx, p)
	cancel()
	s.mu.Lock()
	s.files[p] = fileEntry{content: f.content, err: err, at: time.Now()}
	s.mu.Unlock()
	return f.content, err
}

// matchApps returns the Applications deploying to ns, preferring those whose destination is cluster.
func matchApps(apps []unstructured.Unstructured, cluster, ns string) []unstructured.Unstructured {
	var exact, nsOnly []unstructured.Unstructured
	for _, a := range apps {
		dns, _, _ := unstructured.NestedString(a.Object, "spec", "destination", "namespace")
		if dns != ns {
			continue
		}
		name, _, _ := unstructured.NestedString(a.Object, "spec", "destination", "name")
		if cluster != "" && name == cluster {
			exact = append(exact, a)
		} else {
			nsOnly = append(nsOnly, a)
		}
	}
	out := exact
	if len(out) == 0 {
		out = nsOnly
	}
	sort.Slice(out, func(i, j int) bool { return out[i].GetName() < out[j].GetName() })
	if len(out) > maxApps {
		out = out[:maxApps]
	}
	return out
}

type appFile struct {
	role   string
	path   string
	inline string // a line of context instead of a file
}

// appFiles lists the repo files an Application renders from, lowest precedence first.
func (s *Sources) appFiles(app unstructured.Unstructured) []appFile {
	var sources []map[string]any
	if list, ok, _ := unstructured.NestedSlice(app.Object, "spec", "sources"); ok {
		for _, x := range list {
			if m, ok := x.(map[string]any); ok {
				sources = append(sources, m)
			}
		}
	} else if m, ok, _ := unstructured.NestedMap(app.Object, "spec", "source"); ok {
		sources = append(sources, m)
	}
	refs := map[string]string{} // ref name -> repoURL
	for _, src := range sources {
		if ref, _ := src["ref"].(string); ref != "" {
			refs[ref], _ = src["repoURL"].(string)
		}
	}
	var out []appFile
	seen := map[string]bool{}
	add := func(f appFile) {
		if f.path != "" {
			if seen[f.path] || s.exec.allowedPath(f.path) != nil {
				return
			}
			seen[f.path] = true
		}
		out = append(out, f)
	}
	for _, src := range sources {
		repoURL, _ := src["repoURL"].(string)
		dir, _ := src["path"].(string)
		if dir == "" {
			continue
		}
		dir = path.Clean(strings.TrimPrefix(dir, "/"))
		local := s.exec.sameRepo(repoURL)
		helm, _ := src["helm"].(map[string]any)
		switch {
		case !local:
			out = append(out, appFile{inline: fmt.Sprintf("chart/manifests %s in %s (another repository: not changeable with gitops.change)", dir, repoURL)})
		case helm == nil:
			add(appFile{inline: fmt.Sprintf("manifests directory: %s", dir)})
		default:
			add(appFile{role: "chart defaults", path: path.Join(dir, "values.yaml")})
		}
		if helm == nil {
			continue
		}
		vfs, _, _ := unstructured.NestedStringSlice(helm, "valueFiles")
		for i, vf := range vfs {
			role := "values file"
			if i == len(vfs)-1 {
				role = "values file (highest precedence)"
			}
			p, repo := path.Join(dir, vf), repoURL
			if strings.HasPrefix(vf, "$") {
				name, rest, _ := strings.Cut(strings.TrimPrefix(vf, "$"), "/")
				p, repo = path.Clean(rest), refs[name]
			}
			if !s.exec.sameRepo(repo) {
				out = append(out, appFile{inline: fmt.Sprintf("values file %s in %s (another repository)", vf, repo)})
				continue
			}
			add(appFile{role: role, path: p})
		}
		if vo, ok := helm["valuesObject"].(map[string]any); ok && len(vo) > 0 {
			keys := make([]string, 0, len(vo))
			for k := range vo {
				keys = append(keys, k)
			}
			sort.Strings(keys)
			out = append(out, appFile{inline: "inline helm values set in the Application itself (override the files): " + strings.Join(keys, ", ")})
		}
	}
	return out
}

// sameRepo reports whether repoURL is the configured GitOps repository.
func (e *Executor) sameRepo(repoURL string) bool {
	u := strings.ToLower(strings.TrimSpace(repoURL))
	u = strings.TrimSuffix(strings.TrimSuffix(u, "/"), ".git")
	for _, p := range []string{"https://", "http://", "ssh://", "git@"} {
		u = strings.TrimPrefix(u, p)
	}
	u = strings.Replace(u, ":", "/", 1) // git@github.com:owner/repo
	host, rest, _ := strings.Cut(u, "/")
	if host != "github.com" && !strings.Contains(e.cfg.APIURL, host) {
		return false
	}
	return strings.EqualFold(rest, e.cfg.Repo)
}
