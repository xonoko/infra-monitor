package generic

import (
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"reflect"
	"sort"
	"strings"

	"github.com/pmezard/go-difflib/difflib"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/yaml"
)

// sanitize returns a copy without server-managed noise, for diffs, payloads and rollbacks.
func sanitize(o *unstructured.Unstructured) map[string]any {
	if o == nil {
		return nil
	}
	c := o.DeepCopy().Object
	delete(c, "status")
	if md, ok := c["metadata"].(map[string]any); ok {
		for _, k := range []string{"managedFields", "resourceVersion", "generation", "uid", "creationTimestamp", "selfLink"} {
			delete(md, k)
		}
		if ann, ok := md["annotations"].(map[string]any); ok {
			delete(ann, "kubectl.kubernetes.io/last-applied-configuration")
			delete(ann, "deployment.kubernetes.io/revision")
			if len(ann) == 0 {
				delete(md, "annotations")
			}
		}
	}
	return c
}

func toYAML(v any) string {
	if v == nil {
		return ""
	}
	b, err := yaml.Marshal(v)
	if err != nil {
		return fmt.Sprintf("%v", v)
	}
	return string(b)
}

func unifiedDiff(before, after, fromName, toName string) string {
	d, err := difflib.GetUnifiedDiffString(difflib.UnifiedDiff{
		A: difflib.SplitLines(before), B: difflib.SplitLines(after),
		FromFile: fromName, ToFile: toName, Context: 3,
	})
	if err != nil {
		return ""
	}
	return d
}

// change is one leaf difference between two objects.
type change struct {
	Path     string
	Old, New any
}

func (c change) String() string {
	return fmt.Sprintf("%s: %s → %s", c.Path, short(c.Old), short(c.New))
}

func short(v any) string {
	if v == nil {
		return "(unset)"
	}
	switch t := v.(type) {
	case string:
		return t
	case map[string]any, []any:
		b, _ := json.Marshal(t)
		s := string(b)
		if len(s) > 80 {
			s = s[:77] + "..."
		}
		return s
	default:
		return fmt.Sprint(t)
	}
}

// diffPaths lists leaf changes; lists of objects with a "name" are matched by name (containers[app]).
func diffPaths(before, after any, prefix string, out *[]change) {
	if reflect.DeepEqual(before, after) {
		return
	}
	bm, bok := before.(map[string]any)
	am, aok := after.(map[string]any)
	if bok && aok {
		keys := map[string]bool{}
		for k := range bm {
			keys[k] = true
		}
		for k := range am {
			keys[k] = true
		}
		sorted := make([]string, 0, len(keys))
		for k := range keys {
			sorted = append(sorted, k)
		}
		sort.Strings(sorted)
		for _, k := range sorted {
			diffPaths(bm[k], am[k], join(prefix, k), out)
		}
		return
	}
	bl, blok := before.([]any)
	al, alok := after.([]any)
	if blok && alok && namedList(bl) && namedList(al) {
		idx := map[string]any{}
		for _, e := range bl {
			idx[e.(map[string]any)["name"].(string)] = e
		}
		seen := map[string]bool{}
		for _, e := range al {
			n := e.(map[string]any)["name"].(string)
			seen[n] = true
			diffPaths(idx[n], e, prefix+"["+n+"]", out)
		}
		for _, e := range bl {
			n := e.(map[string]any)["name"].(string)
			if !seen[n] {
				*out = append(*out, change{Path: prefix + "[" + n + "]", Old: e, New: nil})
			}
		}
		return
	}
	*out = append(*out, change{Path: prefix, Old: before, New: after})
}

func namedList(l []any) bool {
	if len(l) == 0 {
		return false
	}
	for _, e := range l {
		m, ok := e.(map[string]any)
		if !ok {
			return false
		}
		if _, ok := m["name"].(string); !ok {
			return false
		}
	}
	return true
}

func join(prefix, k string) string {
	if prefix == "" {
		return k
	}
	return prefix + "." + k
}

func changesBetween(before, after map[string]any) []change {
	var out []change
	diffPaths(any(before), any(after), "", &out)
	return out
}

// describeEffect summarizes changes, noting rolling restarts for pod template changes.
func describeEffect(kind, name string, ch []change) string {
	if len(ch) == 0 {
		return "No change: the object already matches."
	}
	parts := make([]string, 0, len(ch))
	restart := false
	for i, c := range ch {
		if strings.HasPrefix(c.Path, "spec.template.") {
			restart = true
		}
		if i < 8 {
			parts = append(parts, c.String())
		}
	}
	s := "Changes " + strings.Join(parts, "; ")
	if len(ch) > 8 {
		s += fmt.Sprintf("; and %d more", len(ch)-8)
	}
	s += "."
	switch kind {
	case "Deployment", "StatefulSet", "DaemonSet":
		if restart {
			s += fmt.Sprintf(" Triggers a rolling restart of %s/%s.", strings.ToLower(kind), name)
		}
	}
	return s
}

// rollbackMergePatch returns a JSON merge patch that turns `after` back into `before`.
func rollbackMergePatch(before, after map[string]any) (map[string]any, error) {
	bj, err := json.Marshal(before)
	if err != nil {
		return nil, err
	}
	aj, err := json.Marshal(after)
	if err != nil {
		return nil, err
	}
	p, err := jsonpatch.CreateMergePatch(aj, bj)
	if err != nil {
		return nil, err
	}
	var out map[string]any
	if err := json.Unmarshal(p, &out); err != nil {
		return nil, err
	}
	return out, nil
}

// argoApp returns the Argo CD application managing the object ("" when not detected).
func argoApp(o *unstructured.Unstructured) string {
	if o == nil {
		return ""
	}
	ann, labels := o.GetAnnotations(), o.GetLabels()
	if v := ann["argocd.argoproj.io/tracking-id"]; v != "" {
		if i := strings.IndexByte(v, ':'); i > 0 {
			return v[:i]
		}
		return v
	}
	if v := labels["argocd.argoproj.io/instance"]; v != "" {
		return v
	}
	hasArgo := false
	for k := range ann {
		if strings.HasPrefix(k, "argocd.argoproj.io/") {
			hasArgo = true
		}
	}
	for k := range labels {
		if strings.HasPrefix(k, "argocd.argoproj.io/") {
			hasArgo = true
		}
	}
	if hasArgo {
		return labels["app.kubernetes.io/instance"]
	}
	return ""
}

func argoWarning(app string) string {
	return fmt.Sprintf("Argo CD app %q manages this object: selfHeal will revert a live change; prefer gitops.change (or label this as a temporary live fix).", app)
}

// fingerprint pins the desired state of an object (spec, labels, annotations...), ignoring status and
// bookkeeping fields such as resourceVersion, which change on every status update (e.g. a crash-looping
// Deployment) and would otherwise invalidate approvals without any real change.
func fingerprint(o *unstructured.Unstructured) string {
	if o == nil {
		return "absent"
	}
	b, _ := json.Marshal(sanitize(o))
	h := sha256.Sum256(append([]byte(string(o.GetUID())+"\x00"), b...))
	return "spec:" + hex.EncodeToString(h[:12])
}
