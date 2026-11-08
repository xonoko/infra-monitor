package generic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// errUnprepared is returned by Execute: generic actions only run through ExecutePrepared.
var errUnprepared = errors.New("needs a prepared proposal: generic actions run only after a precise human approval")

// Executors returns the generic executors (kubectl.exec only when exec is enabled).
func Executors(backend Backend, cfg config.ActionsConfig) []actions.Executor {
	b := base{backend: backend, cfg: cfg}
	out := []actions.Executor{&Patch{b}, &Apply{b}, &Delete{b}}
	if cfg.Generic.ExecEnabled {
		out = append(out, &Exec{b})
	}
	return out
}

type base struct {
	backend Backend
	cfg     config.ActionsConfig
}

// RequiresApproval: generic actions are never auto-approved.
func (base) RequiresApproval() bool { return true }

func (base) Execute(context.Context, map[string]any, actions.Target) (string, error) {
	return "", errUnprepared
}

func (b base) cluster(t actions.Target) (Cluster, error) { return b.backend.For(t.Cluster) }

// ref builds the target reference; namespace defaults to the incident namespace for namespaced kinds.
func (b base) ref(ctx context.Context, cl Cluster, apiVersion, kind, ns, name string, t actions.Target) (Ref, error) {
	r := Ref{APIVersion: apiVersion, Kind: kind, Namespace: ns, Name: name}
	namespaced, err := cl.Namespaced(ctx, apiVersion, kind)
	if err != nil {
		return r, err
	}
	if !namespaced {
		r.Namespace = ""
	} else if r.Namespace == "" {
		r.Namespace = t.Namespace
		if r.Namespace == "" {
			return r, fmt.Errorf("%s is namespaced: give a namespace", kind)
		}
	}
	return r, nil
}

func clusterLabel(c string) string {
	if c == "" {
		return "(default cluster)"
	}
	return c
}

func target(cluster string, r Ref) string {
	s := clusterLabel(cluster) + " / "
	if r.Namespace != "" {
		s += r.Namespace + " / "
	}
	return s + r.APIVersion + " " + r.Kind + " " + r.Name
}

// annotate fills core-infrastructure and Argo CD information.
func (b base) annotate(ctx context.Context, cl Cluster, p *actions.Prepared, ns string, live *unstructured.Unstructured) {
	if ns != "" {
		ann, err := cl.NamespaceAnnotations(ctx, ns)
		if err != nil {
			ann = nil
		}
		prefix := strings.TrimSuffix(b.cfg.AnnotationPrefix, "/")
		if prefix == "" {
			prefix = "infra-monitor.xonoko.com"
		}
		p.CoreInfrastructure = actions.CoreNamespace(ns, ann, prefix)
	}
	if app := argoApp(live); app != "" {
		p.ArgoManaged = app
		p.Warnings = append(p.Warnings, argoWarning(app))
	}
	if p.CoreInfrastructure {
		p.Warnings = append(p.Warnings, "Core infrastructure namespace "+ns+": double-check before approving.")
	}
}

func str(p map[string]any, k string) string {
	if v, ok := p[k].(string); ok {
		return strings.TrimSpace(v)
	}
	return ""
}

func validName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("invalid name %q: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func validNamespace(ns string) error {
	if ns == "" {
		return nil
	}
	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return fmt.Errorf("invalid namespace %q: %s", ns, strings.Join(errs, "; "))
	}
	return nil
}

func validAPIVersionKind(apiVersion, kind string) error {
	if kind == "" {
		return errors.New("kind is required")
	}
	if _, err := schema.ParseGroupVersion(apiVersion); err != nil || apiVersion == "" {
		return fmt.Errorf("invalid apiVersion %q", apiVersion)
	}
	return nil
}

var workloadKinds = map[string]bool{"Deployment": true, "StatefulSet": true, "DaemonSet": true}

var dataKinds = map[string]bool{
	"PersistentVolumeClaim": true, "PersistentVolume": true, "Namespace": true, "Secret": true, "StatefulSet": true,
	"CustomResourceDefinition": true,
}

func failedDryRun(p *actions.Prepared, err error) *actions.Prepared {
	p.DryRunOK = false
	p.DryRun = "Server-side dry-run failed: " + err.Error()
	p.Effect = "Not applicable: the API server rejected the dry-run, so this proposal can't be approved as is."
	if p.Rollback == "" {
		p.Rollback = "—"
	}
	return p
}

func preconditionError(want, got string) error {
	return fmt.Errorf("%w (prepared %s, now %s)", actions.ErrPreconditionChanged, want, got)
}

// ---------------- k8s.patch ----------------

// Patch is "k8s.patch": a strategic-merge, JSON merge or JSON patch on any object.
type Patch struct{ base }

func (*Patch) Type() string { return "k8s.patch" }
func (*Patch) Description() string {
	return "Patch ANY Kubernetes object (e.g. raise a container's resources.limits.memory, change env, replicas, an annotation). " +
		"Needs explicit human approval, which sees the exact patch, a server-side dry-run diff and the rollback. " +
		"If the object is managed by Argo CD (GitOps), a live patch is reverted by selfHeal: propose gitops.change on the values file instead " +
		"(optionally plus this patch as a clearly labeled temporary fix)."
}
func (*Patch) Risk() string { return "medium" }
func (*Patch) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["apiVersion", "kind", "name", "patch_type", "patch"],
  "properties": {
    "apiVersion": {"type": "string", "description": "e.g. apps/v1, v1, networking.k8s.io/v1"},
    "kind": {"type": "string", "description": "e.g. Deployment, ConfigMap"},
    "namespace": {"type": "string", "description": "Defaults to the incident namespace for namespaced kinds"},
    "name": {"type": "string"},
    "patch_type": {"type": "string", "enum": ["strategic", "merge", "json"], "description": "strategic (built-in kinds; lists like containers merge by name), merge (RFC 7386, lists are replaced), json (RFC 6902 operations)"},
    "patch": {"type": ["object", "array"], "description": "The patch body: an object for strategic/merge, an array of operations for json. Example strategic patch raising a memory limit: {\"spec\":{\"template\":{\"spec\":{\"containers\":[{\"name\":\"app\",\"resources\":{\"limits\":{\"memory\":\"256Mi\"}}}]}}}}"}
  }
}`)
}

func patchType(s string) (string, error) {
	switch s {
	case "strategic":
		return "application/strategic-merge-patch+json", nil
	case "merge":
		return "application/merge-patch+json", nil
	case "json":
		return "application/json-patch+json", nil
	}
	return "", fmt.Errorf("patch_type must be strategic, merge or json, got %q", s)
}

func (e *Patch) Validate(_ context.Context, p map[string]any, _ actions.Target) error {
	if err := guard(str(p, "kind"), p["patch"]); err != nil {
		return err
	}
	if err := validAPIVersionKind(str(p, "apiVersion"), str(p, "kind")); err != nil {
		return err
	}
	if err := validName(str(p, "name")); err != nil {
		return err
	}
	if err := validNamespace(str(p, "namespace")); err != nil {
		return err
	}
	pt := str(p, "patch_type")
	if _, err := patchType(pt); err != nil {
		return err
	}
	switch v := p["patch"].(type) {
	case []any:
		if pt != "json" {
			return errors.New("an array patch needs patch_type json")
		}
		if len(v) == 0 {
			return errors.New("empty json patch")
		}
	case map[string]any:
		if pt == "json" {
			return errors.New("patch_type json needs an array of operations")
		}
		if len(v) == 0 {
			return errors.New("empty patch")
		}
	default:
		return errors.New("patch must be an object or an array")
	}
	return nil
}

func (e *Patch) Summary(p map[string]any, t actions.Target) string {
	ns := str(p, "namespace")
	if ns == "" {
		ns = t.Namespace
	}
	return fmt.Sprintf("%s patch %s/%s -n %s (cluster %s)", str(p, "patch_type"), strings.ToLower(str(p, "kind")), str(p, "name"), ns, clusterLabel(t.Cluster))
}

func (e *Patch) payload(p map[string]any) (string, []byte, string, string, error) {
	pt, err := patchType(str(p, "patch_type"))
	if err != nil {
		return "", nil, "", "", err
	}
	data, err := json.Marshal(p["patch"])
	if err != nil {
		return "", nil, "", "", err
	}
	if str(p, "patch_type") == "json" {
		pretty, _ := json.MarshalIndent(p["patch"], "", "  ")
		return pt, data, string(pretty), "json", nil
	}
	return pt, data, toYAML(p["patch"]), "yaml", nil
}

func (e *Patch) Prepare(ctx context.Context, params map[string]any, t actions.Target) (*actions.Prepared, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "k8s.patch.prepare")
	defer span.End()
	cl, err := e.cluster(t)
	if err != nil {
		return nil, err
	}
	ref, err := e.ref(ctx, cl, str(params, "apiVersion"), str(params, "kind"), str(params, "namespace"), str(params, "name"), t)
	if err != nil {
		return nil, err
	}
	pt, data, payload, format, err := e.payload(params)
	if err != nil {
		return nil, err
	}
	live, err := cl.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", ref, err)
	}
	p := &actions.Prepared{
		Target: target(t.Cluster, ref), Operation: str(params, "patch_type") + " patch (server-side, field manager " + FieldManager + ")",
		Payload: payload, PayloadFormat: format, Precondition: live.GetResourceVersion(), Risk: "medium",
	}
	e.annotate(ctx, cl, p, ref.Namespace, live)
	if p.CoreInfrastructure && workloadKinds[ref.Kind] {
		p.Risk = "high"
	}
	if str(params, "patch_type") == "json" && strings.Contains(string(data), `"remove"`) {
		p.Risk = "high"
	}
	dry, err := cl.Patch(ctx, ref, ptType(pt), data, true)
	if err != nil {
		return failedDryRun(p, err), nil
	}
	before, after := sanitize(live), sanitize(dry)
	p.DryRunOK = true
	p.DryRun = unifiedDiff(toYAML(before), toYAML(after), "live", "after (server-side dry-run)")
	ch := changesBetween(before, after)
	p.Effect = describeEffect(ref.Kind, ref.Name, ch)
	for _, c := range ch {
		if strings.HasSuffix(c.Path, "spec.replicas") && fmt.Sprint(c.New) == "0" {
			p.Risk = "high"
			p.Warnings = append(p.Warnings, "Scales to 0 replicas: the workload stops serving.")
		}
	}
	if len(ch) == 0 {
		p.Warnings = append(p.Warnings, "The dry-run shows no change.")
		p.Rollback = "none needed (no change)"
		return p, nil
	}
	rb, err := rollbackMergePatch(before, after)
	if err != nil {
		p.Rollback = "could not compute an automatic rollback: " + err.Error()
		return p, nil
	}
	p.RollbackAction = &actions.RollbackAction{Type: "k8s.patch", Params: map[string]any{
		"apiVersion": ref.APIVersion, "kind": ref.Kind, "namespace": ref.Namespace, "name": ref.Name, "patch_type": "merge", "patch": rb,
	}}
	p.Rollback = "Merge patch restoring the previous values:\n" + toYAML(rb)
	return p, nil
}

func (e *Patch) ExecutePrepared(ctx context.Context, params map[string]any, t actions.Target, p *actions.Prepared) (string, error) {
	if p == nil {
		return "", errUnprepared
	}
	cl, err := e.cluster(t)
	if err != nil {
		return "", err
	}
	ref, err := e.ref(ctx, cl, str(params, "apiVersion"), str(params, "kind"), str(params, "namespace"), str(params, "name"), t)
	if err != nil {
		return "", err
	}
	pt, data, _, _, err := e.payload(params)
	if err != nil {
		return "", err
	}
	live, err := cl.Get(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", ref, err)
	}
	if live.GetResourceVersion() != p.Precondition {
		return "", preconditionError(p.Precondition, live.GetResourceVersion())
	}
	res, err := cl.Patch(ctx, ref, ptType(pt), data, false)
	if err != nil {
		return "", fmt.Errorf("patch %s: %w", ref, err)
	}
	ch := changesBetween(sanitize(live), sanitize(res))
	return fmt.Sprintf("patched %s %s (resourceVersion %s → %s). %s", ref.Kind, ref.Name, live.GetResourceVersion(), res.GetResourceVersion(),
		describeEffect(ref.Kind, ref.Name, ch)), nil
}

// ---------------- k8s.apply ----------------

// Apply is "k8s.apply": server-side apply of a full manifest (create or update).
type Apply struct{ base }

func (*Apply) Type() string { return "k8s.apply" }
func (*Apply) Description() string {
	return "Create or update ANY Kubernetes object from a full manifest with server-side apply (field manager infra-monitor, no force). " +
		"Needs explicit human approval with a dry-run diff. Prefer k8s.patch for small changes to existing objects, and gitops.change for Argo CD managed objects."
}
func (*Apply) Risk() string { return "medium" }
func (*Apply) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["manifest"],
  "properties": {
    "manifest": {
      "type": "object",
      "description": "Full object: apiVersion, kind, metadata.name, metadata.namespace (for namespaced kinds; defaults to the incident namespace) and the desired fields.",
      "required": ["apiVersion", "kind", "metadata"],
      "properties": {
        "apiVersion": {"type": "string"},
        "kind": {"type": "string"},
        "metadata": {"type": "object", "required": ["name"], "properties": {"name": {"type": "string"}, "namespace": {"type": "string"}}}
      }
    }
  }
}`)
}

func manifest(p map[string]any) (*unstructured.Unstructured, error) {
	m, ok := p["manifest"].(map[string]any)
	if !ok {
		return nil, errors.New("manifest must be an object")
	}
	o := (&unstructured.Unstructured{Object: m}).DeepCopy()
	for _, k := range []string{"managedFields", "resourceVersion", "uid", "creationTimestamp", "generation", "selfLink"} {
		unstructured.RemoveNestedField(o.Object, "metadata", k)
	}
	return o, nil
}

func (e *Apply) Validate(_ context.Context, p map[string]any, _ actions.Target) error {
	o, err := manifest(p)
	if err != nil {
		return err
	}
	if err := guard(o.GetKind(), o.Object); err != nil {
		return err
	}
	if err := validAPIVersionKind(o.GetAPIVersion(), o.GetKind()); err != nil {
		return err
	}
	if err := validName(o.GetName()); err != nil {
		return err
	}
	return validNamespace(o.GetNamespace())
}

func (e *Apply) Summary(p map[string]any, t actions.Target) string {
	o, err := manifest(p)
	if err != nil {
		return "apply (invalid manifest)"
	}
	ns := o.GetNamespace()
	if ns == "" {
		ns = t.Namespace
	}
	return fmt.Sprintf("apply %s/%s -n %s (cluster %s)", strings.ToLower(o.GetKind()), o.GetName(), ns, clusterLabel(t.Cluster))
}

func (e *Apply) resolve(ctx context.Context, cl Cluster, params map[string]any, t actions.Target) (*unstructured.Unstructured, Ref, error) {
	o, err := manifest(params)
	if err != nil {
		return nil, Ref{}, err
	}
	ref, err := e.ref(ctx, cl, o.GetAPIVersion(), o.GetKind(), o.GetNamespace(), o.GetName(), t)
	if err != nil {
		return nil, ref, err
	}
	o.SetNamespace(ref.Namespace)
	return o, ref, nil
}

func (e *Apply) Prepare(ctx context.Context, params map[string]any, t actions.Target) (*actions.Prepared, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "k8s.apply.prepare")
	defer span.End()
	cl, err := e.cluster(t)
	if err != nil {
		return nil, err
	}
	o, ref, err := e.resolve(ctx, cl, params, t)
	if err != nil {
		return nil, err
	}
	live, err := cl.Get(ctx, ref)
	exists := true
	if apierrors.IsNotFound(err) {
		exists, live = false, nil
	} else if err != nil {
		return nil, fmt.Errorf("get %s: %w", ref, err)
	}
	p := &actions.Prepared{Target: target(t.Cluster, ref), Payload: toYAML(o.Object), PayloadFormat: "yaml", Risk: "medium"}
	if exists {
		p.Operation = "server-side apply (update, field manager " + FieldManager + ", no force)"
		p.Precondition = live.GetResourceVersion()
	} else {
		p.Operation = "server-side apply (create, field manager " + FieldManager + ")"
		p.Precondition = "absent"
	}
	e.annotate(ctx, cl, p, ref.Namespace, live)
	if p.CoreInfrastructure || dataKinds[ref.Kind] {
		p.Risk = "high"
	}
	dry, err := cl.Apply(ctx, o, true)
	if err != nil {
		return failedDryRun(p, err), nil
	}
	before, after := sanitize(live), sanitize(dry)
	beforeYAML := ""
	if exists {
		beforeYAML = toYAML(before)
	}
	p.DryRunOK = true
	p.DryRun = unifiedDiff(beforeYAML, toYAML(after), "live", "after (server-side dry-run)")
	if !exists {
		p.Effect = fmt.Sprintf("Creates %s %s.", ref.Kind, ref.Name)
		p.RollbackAction = &actions.RollbackAction{Type: "k8s.delete", Params: map[string]any{
			"apiVersion": ref.APIVersion, "kind": ref.Kind, "namespace": ref.Namespace, "name": ref.Name,
		}}
		p.Rollback = fmt.Sprintf("Delete the created %s %s.", ref.Kind, ref.Name)
		return p, nil
	}
	ch := changesBetween(before, after)
	p.Effect = describeEffect(ref.Kind, ref.Name, ch)
	if len(ch) == 0 {
		p.Warnings = append(p.Warnings, "The dry-run shows no change.")
		p.Rollback = "none needed (no change)"
		return p, nil
	}
	rb, err := rollbackMergePatch(before, after)
	if err != nil {
		p.Rollback = "could not compute an automatic rollback: " + err.Error()
		return p, nil
	}
	p.RollbackAction = &actions.RollbackAction{Type: "k8s.patch", Params: map[string]any{
		"apiVersion": ref.APIVersion, "kind": ref.Kind, "namespace": ref.Namespace, "name": ref.Name, "patch_type": "merge", "patch": rb,
	}}
	p.Rollback = "Merge patch restoring the previous values:\n" + toYAML(rb)
	return p, nil
}

func (e *Apply) ExecutePrepared(ctx context.Context, params map[string]any, t actions.Target, p *actions.Prepared) (string, error) {
	if p == nil {
		return "", errUnprepared
	}
	cl, err := e.cluster(t)
	if err != nil {
		return "", err
	}
	o, ref, err := e.resolve(ctx, cl, params, t)
	if err != nil {
		return "", err
	}
	live, err := cl.Get(ctx, ref)
	now := "absent"
	switch {
	case apierrors.IsNotFound(err):
	case err != nil:
		return "", fmt.Errorf("get %s: %w", ref, err)
	default:
		now = live.GetResourceVersion()
	}
	if now != p.Precondition {
		return "", preconditionError(p.Precondition, now)
	}
	res, err := cl.Apply(ctx, o, false)
	if err != nil {
		return "", fmt.Errorf("apply %s: %w", ref, err)
	}
	if now == "absent" {
		return fmt.Sprintf("created %s %s (resourceVersion %s)", ref.Kind, ref.Name, res.GetResourceVersion()), nil
	}
	return fmt.Sprintf("applied %s %s (resourceVersion %s → %s). %s", ref.Kind, ref.Name, now, res.GetResourceVersion(),
		describeEffect(ref.Kind, ref.Name, changesBetween(sanitize(live), sanitize(res)))), nil
}

// ---------------- k8s.delete ----------------

// Delete is "k8s.delete": delete any object, guarded by its resourceVersion.
type Delete struct{ base }

func (*Delete) Type() string { return "k8s.delete" }
func (*Delete) Description() string {
	return "Delete ANY Kubernetes object (guarded by its resourceVersion). Needs explicit human approval; the card shows the full object and how to recreate it. " +
		"For a crashlooping pod prefer k8s.delete_pod; for Argo CD managed objects prefer gitops.change."
}
func (*Delete) Risk() string { return "high" }
func (*Delete) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["apiVersion", "kind", "name"],
  "properties": {
    "apiVersion": {"type": "string"},
    "kind": {"type": "string"},
    "namespace": {"type": "string", "description": "Defaults to the incident namespace for namespaced kinds"},
    "name": {"type": "string"},
    "propagation": {"type": "string", "enum": ["Foreground", "Background", "Orphan"], "description": "Dependents deletion policy (default: the API default)"}
  }
}`)
}

func (e *Delete) Validate(_ context.Context, p map[string]any, _ actions.Target) error {
	if err := validAPIVersionKind(str(p, "apiVersion"), str(p, "kind")); err != nil {
		return err
	}
	if err := validName(str(p, "name")); err != nil {
		return err
	}
	return validNamespace(str(p, "namespace"))
}

func (e *Delete) Summary(p map[string]any, t actions.Target) string {
	ns := str(p, "namespace")
	if ns == "" {
		ns = t.Namespace
	}
	return fmt.Sprintf("delete %s/%s -n %s (cluster %s)", strings.ToLower(str(p, "kind")), str(p, "name"), ns, clusterLabel(t.Cluster))
}

func propagation(p map[string]any) *metav1DeletionPropagation {
	v := str(p, "propagation")
	if v == "" {
		return nil
	}
	dp := metav1DeletionPropagation(v)
	return &dp
}

func (e *Delete) Prepare(ctx context.Context, params map[string]any, t actions.Target) (*actions.Prepared, error) {
	ctx, span := telemetry.Tracer().Start(ctx, "k8s.delete.prepare")
	defer span.End()
	cl, err := e.cluster(t)
	if err != nil {
		return nil, err
	}
	ref, err := e.ref(ctx, cl, str(params, "apiVersion"), str(params, "kind"), str(params, "namespace"), str(params, "name"), t)
	if err != nil {
		return nil, err
	}
	live, err := cl.Get(ctx, ref)
	if err != nil {
		return nil, fmt.Errorf("get %s: %w", ref, err)
	}
	obj := sanitize(live)
	p := &actions.Prepared{
		Target: target(t.Cluster, ref), Operation: "delete (precondition resourceVersion " + live.GetResourceVersion() + ")",
		Payload: toYAML(obj), PayloadFormat: "yaml", Precondition: live.GetResourceVersion(), Risk: "medium",
	}
	if prop := str(params, "propagation"); prop != "" {
		p.Operation += ", propagation " + prop
	}
	e.annotate(ctx, cl, p, ref.Namespace, live)
	if dataKinds[ref.Kind] || p.CoreInfrastructure {
		p.Risk = "high"
	}
	if dataKinds[ref.Kind] {
		p.Warnings = append(p.Warnings, fmt.Sprintf("Deleting a %s can lose data that recreating the object does not bring back.", ref.Kind))
	}
	if err := cl.Delete(ctx, ref, live.GetResourceVersion(), propagation(params), true); err != nil {
		return failedDryRun(p, err), nil
	}
	p.DryRunOK = true
	p.DryRun = "Server-side dry-run: delete accepted."
	p.Effect = fmt.Sprintf("Deletes %s %s.", ref.Kind, ref.Name)
	for _, or := range live.GetOwnerReferences() {
		if or.Controller != nil && *or.Controller {
			p.Effect += fmt.Sprintf(" It is controlled by %s %s, which will normally recreate it.", or.Kind, or.Name)
		}
	}
	p.RollbackAction = &actions.RollbackAction{Type: "k8s.apply", Params: map[string]any{"manifest": obj}}
	p.Rollback = "Recreate the object from the manifest above with k8s.apply (spec and metadata only; data and status are not restored)."
	return p, nil
}

func (e *Delete) ExecutePrepared(ctx context.Context, params map[string]any, t actions.Target, p *actions.Prepared) (string, error) {
	if p == nil {
		return "", errUnprepared
	}
	cl, err := e.cluster(t)
	if err != nil {
		return "", err
	}
	ref, err := e.ref(ctx, cl, str(params, "apiVersion"), str(params, "kind"), str(params, "namespace"), str(params, "name"), t)
	if err != nil {
		return "", err
	}
	live, err := cl.Get(ctx, ref)
	if err != nil {
		return "", fmt.Errorf("get %s: %w", ref, err)
	}
	if live.GetResourceVersion() != p.Precondition {
		return "", preconditionError(p.Precondition, live.GetResourceVersion())
	}
	if err := cl.Delete(ctx, ref, p.Precondition, propagation(params), false); err != nil {
		if apierrors.IsConflict(err) {
			return "", preconditionError(p.Precondition, "modified")
		}
		return "", fmt.Errorf("delete %s: %w", ref, err)
	}
	return fmt.Sprintf("deleted %s %s (resourceVersion %s)", ref.Kind, ref.Name, p.Precondition), nil
}

// ---------------- kubectl.exec ----------------

// Exec is "kubectl.exec": run an exact command in a container (no dry-run, no rollback).
type Exec struct{ base }

func (*Exec) Type() string { return "kubectl.exec" }
func (*Exec) Description() string {
	return "Run an exact command (argv, no shell unless you explicitly call sh -c) in a running container, e.g. flush a cache or trigger an app-level repair. " +
		"Imperative: no dry-run and no automatic rollback, so prefer declarative actions when possible. Needs explicit human approval."
}
func (*Exec) Risk() string { return "high" }
func (*Exec) Schema() json.RawMessage {
	return json.RawMessage(`{
  "type": "object",
  "additionalProperties": false,
  "required": ["pod", "command"],
  "properties": {
    "namespace": {"type": "string", "description": "Defaults to the incident namespace"},
    "pod": {"type": "string"},
    "container": {"type": "string", "description": "Defaults to the pod's default container"},
    "command": {"type": "array", "minItems": 1, "items": {"type": "string"}, "description": "Exact argv, e.g. [\"redis-cli\", \"FLUSHALL\"]"}
  }
}`)
}

func command(p map[string]any) ([]string, error) {
	raw, ok := p["command"].([]any)
	if !ok || len(raw) == 0 {
		return nil, errors.New("command must be a non-empty array of strings")
	}
	out := make([]string, 0, len(raw))
	for _, v := range raw {
		s, ok := v.(string)
		if !ok {
			return nil, errors.New("command items must be strings")
		}
		out = append(out, s)
	}
	return out, nil
}

func (e *Exec) Validate(_ context.Context, p map[string]any, _ actions.Target) error {
	if err := validName(str(p, "pod")); err != nil {
		return err
	}
	if err := validNamespace(str(p, "namespace")); err != nil {
		return err
	}
	_, err := command(p)
	return err
}

func (e *Exec) Summary(p map[string]any, t actions.Target) string {
	ns := str(p, "namespace")
	if ns == "" {
		ns = t.Namespace
	}
	cmd, _ := command(p)
	b, _ := json.Marshal(cmd)
	return fmt.Sprintf("exec %s in pod/%s -n %s (cluster %s)", b, str(p, "pod"), ns, clusterLabel(t.Cluster))
}

func (e *Exec) pod(ctx context.Context, cl Cluster, params map[string]any, t actions.Target) (*unstructured.Unstructured, Ref, string, error) {
	ns := str(params, "namespace")
	if ns == "" {
		ns = t.Namespace
	}
	ref := Ref{APIVersion: "v1", Kind: "Pod", Namespace: ns, Name: str(params, "pod")}
	if ns == "" {
		return nil, ref, "", errors.New("namespace is required")
	}
	pod, err := cl.Get(ctx, ref)
	if err != nil {
		return nil, ref, "", fmt.Errorf("get %s: %w", ref, err)
	}
	container := str(params, "container")
	if container == "" {
		container = pod.GetAnnotations()["kubectl.kubernetes.io/default-container"]
	}
	if container == "" {
		cs, _, _ := unstructured.NestedSlice(pod.Object, "spec", "containers")
		if len(cs) > 0 {
			if m, ok := cs[0].(map[string]any); ok {
				container, _ = m["name"].(string)
			}
		}
	}
	return pod, ref, container, nil
}

func (e *Exec) Prepare(ctx context.Context, params map[string]any, t actions.Target) (*actions.Prepared, error) {
	cl, err := e.cluster(t)
	if err != nil {
		return nil, err
	}
	pod, ref, container, err := e.pod(ctx, cl, params, t)
	if err != nil {
		return nil, err
	}
	cmd, err := command(params)
	if err != nil {
		return nil, err
	}
	payload, _ := json.MarshalIndent(map[string]any{"pod": ref.Name, "container": container, "command": cmd}, "", "  ")
	p := &actions.Prepared{
		Target: target(t.Cluster, ref) + " (container " + container + ")", Operation: "exec (imperative, runs once)",
		Payload: string(payload), PayloadFormat: "json", Precondition: string(pod.GetUID()), Risk: "high",
		DryRun: "exec cannot be dry-run", DryRunOK: true, Rollback: "none (imperative command)",
	}
	e.annotate(ctx, cl, p, ref.Namespace, nil)
	phase, _, _ := unstructured.NestedString(pod.Object, "status", "phase")
	if phase != "Running" {
		p.DryRunOK = false
		p.DryRun = fmt.Sprintf("pod is %s, not Running: exec is not possible", phase)
	}
	b, _ := json.Marshal(cmd)
	p.Effect = fmt.Sprintf("Runs %s in container %s of pod %s. The effect depends on the command; its output is recorded.", b, container, ref.Name)
	p.Warnings = append(p.Warnings, "Imperative command: no dry-run and no automatic rollback.")
	return p, nil
}

func (e *Exec) ExecutePrepared(ctx context.Context, params map[string]any, t actions.Target, p *actions.Prepared) (string, error) {
	if p == nil {
		return "", errUnprepared
	}
	cl, err := e.cluster(t)
	if err != nil {
		return "", err
	}
	pod, ref, container, err := e.pod(ctx, cl, params, t)
	if err != nil {
		return "", err
	}
	if string(pod.GetUID()) != p.Precondition {
		return "", preconditionError(p.Precondition, string(pod.GetUID()))
	}
	cmd, err := command(params)
	if err != nil {
		return "", err
	}
	res, err := cl.Exec(ctx, ref.Namespace, ref.Name, container, cmd, e.cfg.Generic.ExecTimeout)
	if err != nil {
		return "", fmt.Errorf("exec in %s: %w", ref, err)
	}
	out := fmt.Sprintf("exit code %d\n--- stdout\n%s\n--- stderr\n%s", res.ExitCode, res.Stdout, res.Stderr)
	if res.ExitCode != 0 {
		return out, fmt.Errorf("command exited with code %d", res.ExitCode)
	}
	return out, nil
}
