package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"math"
	"slices"
	"strings"
	"time"

	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/util/validation"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

const fieldManager = "infra-monitor"

// Policy limits what the executors may touch.
type Policy struct {
	NamespaceDeny  []string
	NamespaceAllow []string
	MaxReplicas    int
}

// PolicyFromConfig builds the policy from the actions config.
func PolicyFromConfig(cfg config.ActionsConfig) Policy {
	max := cfg.MaxReplicas
	if max <= 0 {
		max = 10
	}
	return Policy{NamespaceDeny: cfg.NamespaceDeny, NamespaceAllow: cfg.NamespaceAllow, MaxReplicas: max}
}

// Executors returns every Kubernetes executor.
func Executors(clients Clients, p Policy) []actions.Executor {
	return []actions.Executor{NewRolloutRestart(clients, p), NewDeletePod(clients, p), NewScale(clients, p)}
}

func (p Policy) checkNamespace(ns string) error {
	if ns == "" {
		return errors.New("namespace is required (no namespace param and the incident has no namespace)")
	}
	if errs := validation.IsDNS1123Label(ns); len(errs) > 0 {
		return fmt.Errorf("invalid namespace %q: %s", ns, strings.Join(errs, "; "))
	}
	if slices.Contains(p.NamespaceDeny, ns) {
		return fmt.Errorf("namespace %q is denied by policy (actions.namespace_deny)", ns)
	}
	if len(p.NamespaceAllow) > 0 && !slices.Contains(p.NamespaceAllow, ns) {
		return fmt.Errorf("namespace %q is not in actions.namespace_allow", ns)
	}
	return nil
}

func checkName(name string) error {
	if name == "" {
		return errors.New("name is required")
	}
	if errs := validation.IsDNS1123Subdomain(name); len(errs) > 0 {
		return fmt.Errorf("invalid name %q: %s", name, strings.Join(errs, "; "))
	}
	return nil
}

func str(p map[string]any, k string) string {
	s, _ := p[k].(string)
	return strings.TrimSpace(s)
}

func intParam(p map[string]any, k string) (int, bool) {
	switch v := p[k].(type) {
	case float64:
		if v != math.Trunc(v) {
			return 0, false
		}
		return int(v), true
	case int:
		return v, true
	case int64:
		return int(v), true
	case json.Number:
		n, err := v.Int64()
		return int(n), err == nil
	}
	return 0, false
}

func namespace(p map[string]any, t actions.Target) string {
	if ns := str(p, "namespace"); ns != "" {
		return ns
	}
	return t.Namespace
}

func clusterSuffix(t actions.Target) string {
	if t.Cluster == "" {
		return ""
	}
	return " (cluster " + t.Cluster + ")"
}

func startSpan(ctx context.Context, name string, t actions.Target, ns string, attrs ...attribute.KeyValue) (context.Context, trace.Span) {
	return telemetry.Tracer().Start(ctx, name, trace.WithSpanKind(trace.SpanKindClient), trace.WithAttributes(append(attrs,
		attribute.String("k8s.cluster", t.Cluster), attribute.String("k8s.namespace", ns), attribute.Int("incident.id", t.IncidentID))...))
}

// --- k8s.rollout_restart ---

type RolloutRestart struct {
	clients Clients
	policy  Policy
	now     func() time.Time
}

func NewRolloutRestart(c Clients, p Policy) *RolloutRestart {
	return &RolloutRestart{clients: c, policy: p, now: time.Now}
}

func (*RolloutRestart) Type() string { return "k8s.rollout_restart" }
func (*RolloutRestart) Description() string {
	return "Rolling restart of a Deployment, StatefulSet or DaemonSet (same as `kubectl rollout restart`): pods are recreated one by one with the current spec."
}
func (*RolloutRestart) Risk() string { return "medium" }
func (*RolloutRestart) Schema() json.RawMessage {
	return json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["kind", "name"],
  "properties": {
    "kind": {"type": "string", "enum": ["deployment", "statefulset", "daemonset"], "description": "Workload kind"},
    "name": {"type": "string", "minLength": 1, "maxLength": 253, "description": "Workload name"},
    "namespace": {"type": "string", "maxLength": 63, "description": "Namespace; defaults to the incident's namespace"}
  }
}`)
}

func (e *RolloutRestart) Validate(_ context.Context, p map[string]any, t actions.Target) error {
	switch str(p, "kind") {
	case "deployment", "statefulset", "daemonset":
	default:
		return fmt.Errorf("unsupported kind %q", str(p, "kind"))
	}
	if err := checkName(str(p, "name")); err != nil {
		return err
	}
	return e.policy.checkNamespace(namespace(p, t))
}

func (e *RolloutRestart) Summary(p map[string]any, t actions.Target) string {
	return fmt.Sprintf("rollout restart %s/%s -n %s%s", str(p, "kind"), str(p, "name"), namespace(p, t), clusterSuffix(t))
}

func (e *RolloutRestart) Execute(ctx context.Context, p map[string]any, t actions.Target) (string, error) {
	kind, name, ns := str(p, "kind"), str(p, "name"), namespace(p, t)
	ctx, span := startSpan(ctx, "k8s.rollout_restart", t, ns, attribute.String("k8s.kind", kind), attribute.String("k8s.name", name))
	defer span.End()
	if err := e.Validate(ctx, p, t); err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	cl, err := e.clients.For(t.Cluster)
	if err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	ts := e.now().UTC().Format(time.RFC3339)
	patch, _ := json.Marshal(map[string]any{"spec": map[string]any{"template": map[string]any{"metadata": map[string]any{
		"annotations": map[string]string{"kubectl.kubernetes.io/restartedAt": ts}}}}})
	opts := metav1.PatchOptions{FieldManager: fieldManager}
	switch kind {
	case "deployment":
		_, err = cl.AppsV1().Deployments(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, opts)
	case "statefulset":
		_, err = cl.AppsV1().StatefulSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, opts)
	case "daemonset":
		_, err = cl.AppsV1().DaemonSets(ns).Patch(ctx, name, types.StrategicMergePatchType, patch, opts)
	}
	if err != nil {
		telemetry.RecordError(span, err)
		return "", fmt.Errorf("restart %s/%s in %s: %w", kind, name, ns, err)
	}
	return fmt.Sprintf("restarted %s/%s in namespace %s%s (restartedAt=%s)", kind, name, ns, clusterSuffix(t), ts), nil
}

// --- k8s.delete_pod ---

type DeletePod struct {
	clients Clients
	policy  Policy
}

func NewDeletePod(c Clients, p Policy) *DeletePod { return &DeletePod{clients: c, policy: p} }

func (*DeletePod) Type() string { return "k8s.delete_pod" }
func (*DeletePod) Description() string {
	return "Delete one pod so its controller recreates it (use for a single stuck or unhealthy pod). Pods without a controller are not recreated."
}
func (*DeletePod) Risk() string { return "medium" }
func (*DeletePod) Schema() json.RawMessage {
	return json.RawMessage(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["name"],
  "properties": {
    "name": {"type": "string", "minLength": 1, "maxLength": 253, "description": "Pod name"},
    "namespace": {"type": "string", "maxLength": 63, "description": "Namespace; defaults to the incident's namespace"}
  }
}`)
}

func (e *DeletePod) Validate(_ context.Context, p map[string]any, t actions.Target) error {
	if err := checkName(str(p, "name")); err != nil {
		return err
	}
	return e.policy.checkNamespace(namespace(p, t))
}

func (e *DeletePod) Summary(p map[string]any, t actions.Target) string {
	return fmt.Sprintf("delete pod %s -n %s%s", str(p, "name"), namespace(p, t), clusterSuffix(t))
}

func (e *DeletePod) Execute(ctx context.Context, p map[string]any, t actions.Target) (string, error) {
	name, ns := str(p, "name"), namespace(p, t)
	ctx, span := startSpan(ctx, "k8s.delete_pod", t, ns, attribute.String("k8s.name", name))
	defer span.End()
	if err := e.Validate(ctx, p, t); err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	cl, err := e.clients.For(t.Cluster)
	if err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	pod, err := cl.CoreV1().Pods(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		telemetry.RecordError(span, err)
		return "", fmt.Errorf("get pod %s in %s: %w", name, ns, err)
	}
	owner := "no controller: the pod will NOT be recreated"
	for _, ref := range pod.OwnerReferences {
		if ref.Controller != nil && *ref.Controller {
			owner = "owned by " + ref.Kind + "/" + ref.Name
		}
	}
	if err := cl.CoreV1().Pods(ns).Delete(ctx, name, metav1.DeleteOptions{}); err != nil {
		telemetry.RecordError(span, err)
		return "", fmt.Errorf("delete pod %s in %s: %w", name, ns, err)
	}
	return fmt.Sprintf("deleted pod %s in namespace %s%s (%s)", name, ns, clusterSuffix(t), owner), nil
}

// --- k8s.scale ---

type Scale struct {
	clients Clients
	policy  Policy
}

func NewScale(c Clients, p Policy) *Scale { return &Scale{clients: c, policy: p} }

func (*Scale) Type() string { return "k8s.scale" }
func (*Scale) Description() string {
	return "Set the replica count of a Deployment or StatefulSet (bounded by actions.max_replicas). Scaling to 0 stops the workload."
}
func (*Scale) Risk() string { return "high" }
func (e *Scale) Schema() json.RawMessage {
	return json.RawMessage(fmt.Sprintf(`{
  "$schema": "https://json-schema.org/draft/2020-12/schema",
  "type": "object",
  "additionalProperties": false,
  "required": ["kind", "name", "replicas"],
  "properties": {
    "kind": {"type": "string", "enum": ["deployment", "statefulset"], "description": "Workload kind"},
    "name": {"type": "string", "minLength": 1, "maxLength": 253, "description": "Workload name"},
    "namespace": {"type": "string", "maxLength": 63, "description": "Namespace; defaults to the incident's namespace"},
    "replicas": {"type": "integer", "minimum": 0, "maximum": %d, "description": "Desired replica count"}
  }
}`, e.policy.MaxReplicas))
}

func (e *Scale) Validate(_ context.Context, p map[string]any, t actions.Target) error {
	switch str(p, "kind") {
	case "deployment", "statefulset":
	default:
		return fmt.Errorf("unsupported kind %q", str(p, "kind"))
	}
	if err := checkName(str(p, "name")); err != nil {
		return err
	}
	n, ok := intParam(p, "replicas")
	if !ok {
		return errors.New("replicas must be an integer")
	}
	if n < 0 || n > e.policy.MaxReplicas {
		return fmt.Errorf("replicas must be between 0 and %d", e.policy.MaxReplicas)
	}
	return e.policy.checkNamespace(namespace(p, t))
}

func (e *Scale) Summary(p map[string]any, t actions.Target) string {
	n, _ := intParam(p, "replicas")
	return fmt.Sprintf("scale %s/%s -n %s to %d replicas%s", str(p, "kind"), str(p, "name"), namespace(p, t), n, clusterSuffix(t))
}

func (e *Scale) Execute(ctx context.Context, p map[string]any, t actions.Target) (string, error) {
	kind, name, ns := str(p, "kind"), str(p, "name"), namespace(p, t)
	n, _ := intParam(p, "replicas")
	ctx, span := startSpan(ctx, "k8s.scale", t, ns, attribute.String("k8s.kind", kind), attribute.String("k8s.name", name), attribute.Int("k8s.replicas", n))
	defer span.End()
	if err := e.Validate(ctx, p, t); err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	cl, err := e.clients.For(t.Cluster)
	if err != nil {
		telemetry.RecordError(span, err)
		return "", err
	}
	apps := cl.AppsV1()
	var from int32
	switch kind {
	case "deployment":
		sc, err := apps.Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			telemetry.RecordError(span, err)
			return "", fmt.Errorf("get scale of deployment/%s in %s: %w", name, ns, err)
		}
		from = sc.Spec.Replicas
		sc.Spec.Replicas = int32(n) //nolint:gosec // bounded by policy
		if _, err := apps.Deployments(ns).UpdateScale(ctx, name, sc, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
			telemetry.RecordError(span, err)
			return "", fmt.Errorf("scale deployment/%s in %s: %w", name, ns, err)
		}
	case "statefulset":
		sc, err := apps.StatefulSets(ns).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			telemetry.RecordError(span, err)
			return "", fmt.Errorf("get scale of statefulset/%s in %s: %w", name, ns, err)
		}
		from = sc.Spec.Replicas
		sc.Spec.Replicas = int32(n) //nolint:gosec // bounded by policy
		if _, err := apps.StatefulSets(ns).UpdateScale(ctx, name, sc, metav1.UpdateOptions{FieldManager: fieldManager}); err != nil {
			telemetry.RecordError(span, err)
			return "", fmt.Errorf("scale statefulset/%s in %s: %w", name, ns, err)
		}
	}
	return fmt.Sprintf("scaled %s/%s in namespace %s%s from %d to %d replicas", kind, name, ns, clusterSuffix(t), from, n), nil
}
