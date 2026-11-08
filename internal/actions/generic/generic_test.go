package generic

import (
	"context"
	"encoding/json"
	"errors"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	jsonpatch "gopkg.in/evanphx/json-patch.v4"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/config"
)

// fakeCluster honors dry-run (unlike the client-go fake). Strategic patches are approximated with
// JSON merge patches, which is enough to test the preparation logic.
type fakeCluster struct {
	objs      map[string]*unstructured.Unstructured
	nsAnn     map[string]map[string]string
	dryRunErr error
	rv        int
	execs     [][]string
	execRes   ExecResult
}

func key(r Ref) string { return r.Kind + "/" + r.Namespace + "/" + r.Name }

func (f *fakeCluster) Namespaced(_ context.Context, _, kind string) (bool, error) {
	switch kind {
	case "Namespace", "Node", "PersistentVolume", "ClusterRole":
		return false, nil
	}
	return true, nil
}

func (f *fakeCluster) Get(_ context.Context, r Ref) (*unstructured.Unstructured, error) {
	o, ok := f.objs[key(r)]
	if !ok {
		return nil, apierrors.NewNotFound(schema.GroupResource{Resource: strings.ToLower(r.Kind)}, r.Name)
	}
	return o.DeepCopy(), nil
}

func (f *fakeCluster) bump(o *unstructured.Unstructured) {
	f.rv++
	o.SetResourceVersion(strconv.Itoa(f.rv))
}

func (f *fakeCluster) Patch(ctx context.Context, r Ref, pt types.PatchType, data []byte, dryRun bool) (*unstructured.Unstructured, error) {
	if dryRun && f.dryRunErr != nil {
		return nil, f.dryRunErr
	}
	live, err := f.Get(ctx, r)
	if err != nil {
		return nil, err
	}
	cur, _ := json.Marshal(live.Object)
	var out []byte
	if pt == types.JSONPatchType {
		p, err := jsonpatch.DecodePatch(data)
		if err != nil {
			return nil, err
		}
		out, err = p.Apply(cur)
		if err != nil {
			return nil, err
		}
	} else {
		out, err = jsonpatch.MergePatch(cur, data)
		if err != nil {
			return nil, err
		}
	}
	res := &unstructured.Unstructured{}
	if err := json.Unmarshal(out, &res.Object); err != nil {
		return nil, err
	}
	if !dryRun {
		f.bump(res)
		f.objs[key(r)] = res.DeepCopy()
	}
	return res, nil
}

func (f *fakeCluster) Apply(_ context.Context, o *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error) {
	if dryRun && f.dryRunErr != nil {
		return nil, f.dryRunErr
	}
	r := Ref{APIVersion: o.GetAPIVersion(), Kind: o.GetKind(), Namespace: o.GetNamespace(), Name: o.GetName()}
	res := o.DeepCopy()
	if !dryRun {
		f.bump(res)
		f.objs[key(r)] = res.DeepCopy()
	}
	return res, nil
}

func (f *fakeCluster) Delete(ctx context.Context, r Ref, rv string, _ *metav1.DeletionPropagation, dryRun bool) error {
	if dryRun && f.dryRunErr != nil {
		return f.dryRunErr
	}
	live, err := f.Get(ctx, r)
	if err != nil {
		return err
	}
	if rv != "" && live.GetResourceVersion() != rv {
		return apierrors.NewConflict(schema.GroupResource{Resource: r.Kind}, r.Name, errors.New("rv mismatch"))
	}
	if !dryRun {
		delete(f.objs, key(r))
	}
	return nil
}

func (f *fakeCluster) NamespaceAnnotations(_ context.Context, ns string) (map[string]string, error) {
	return f.nsAnn[ns], nil
}

func (f *fakeCluster) Exec(_ context.Context, _, _, _ string, cmd []string, _ time.Duration) (ExecResult, error) {
	f.execs = append(f.execs, cmd)
	return f.execRes, nil
}

type fakeBackend struct{ c *fakeCluster }

func (b fakeBackend) For(string) (Cluster, error) { return b.c, nil }

func deployment(ns string, ann map[string]any) *unstructured.Unstructured {
	md := map[string]any{"name": "demo-api", "namespace": ns, "resourceVersion": "1", "uid": "u-1",
		"managedFields": []any{map[string]any{"manager": "kubectl"}}}
	if ann != nil {
		md["annotations"] = ann
	}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment", "metadata": md,
		"spec": map[string]any{
			"replicas": int64(2),
			"template": map[string]any{"spec": map[string]any{"containers": []any{
				map[string]any{"name": "app", "image": "demo:1", "resources": map[string]any{"limits": map[string]any{"memory": "64Mi"}}},
				map[string]any{"name": "sidecar", "image": "proxy:1"},
			}}},
		},
		"status": map[string]any{"readyReplicas": int64(2)},
	}}
}

func newFake(objs ...*unstructured.Unstructured) *fakeCluster {
	f := &fakeCluster{objs: map[string]*unstructured.Unstructured{}, rv: 10, nsAnn: map[string]map[string]string{
		"vault": {"infra-monitor.xonoko.com/auto-approve": "none"},
	}}
	for _, o := range objs {
		f.objs[key(Ref{Kind: o.GetKind(), Namespace: o.GetNamespace(), Name: o.GetName()})] = o
	}
	return f
}

func cfg(exec bool) config.ActionsConfig {
	return config.ActionsConfig{AnnotationPrefix: "infra-monitor.xonoko.com", Generic: config.GenericActionsConfig{Enabled: true, ExecEnabled: exec, ExecTimeout: time.Second}}
}

func byType(exs []actions.Executor, typ string) actions.Executor {
	for _, e := range exs {
		if e.Type() == typ {
			return e
		}
	}
	return nil
}

var tgt = actions.Target{Cluster: "manager", Namespace: "demo-incident", IncidentID: 7}

func memoryPatch() map[string]any {
	// containers is a merge-replaced list in the fake: send both containers.
	return map[string]any{
		"apiVersion": "apps/v1", "kind": "Deployment", "name": "demo-api", "patch_type": "merge",
		"patch": map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"containers": []any{
			map[string]any{"name": "app", "image": "demo:1", "resources": map[string]any{"limits": map[string]any{"memory": "256Mi"}}},
			map[string]any{"name": "sidecar", "image": "proxy:1"},
		}}}}},
	}
}

func TestPatchPrepareDiffEffectRollbackAndExecute(t *testing.T) {
	f := newFake(deployment("demo-incident", map[string]any{"argocd.argoproj.io/tracking-id": "demo-app:apps/Deployment:demo-incident/demo-api"}))
	exs := Executors(fakeBackend{f}, cfg(false))
	require.Nil(t, byType(exs, "kubectl.exec"), "exec only when enabled")
	ex := byType(exs, "k8s.patch")
	ctx := context.Background()

	_, err := ex.Execute(ctx, memoryPatch(), tgt)
	require.ErrorIs(t, err, errUnprepared)
	require.True(t, ex.(actions.Supervised).RequiresApproval())

	p, err := ex.(actions.Preparer).Prepare(ctx, memoryPatch(), tgt)
	require.NoError(t, err)
	require.True(t, p.DryRunOK)
	require.Equal(t, "manager / demo-incident / apps/v1 Deployment demo-api", p.Target)
	require.True(t, strings.HasPrefix(p.Precondition, "spec:"))
	require.Contains(t, p.Payload, "memory: 256Mi")
	require.Contains(t, p.DryRun, "-            memory: 64Mi")
	require.Contains(t, p.DryRun, "+            memory: 256Mi")
	require.NotContains(t, p.DryRun, "managedFields")
	require.NotContains(t, p.DryRun, "readyReplicas", "status is not part of the diff")
	require.Contains(t, p.Effect, "spec.template.spec.containers[app].resources.limits.memory: 64Mi → 256Mi")
	require.Contains(t, p.Effect, "rolling restart of deployment/demo-api")
	require.Equal(t, "demo-app", p.ArgoManaged)
	require.Contains(t, strings.Join(p.Warnings, " "), "gitops.change")
	require.False(t, p.CoreInfrastructure)
	require.Equal(t, "medium", p.Risk)
	require.NotNil(t, p.RollbackAction)
	require.Equal(t, "k8s.patch", p.RollbackAction.Type)
	require.Contains(t, p.Rollback, "64Mi")
	require.Equal(t, "1", f.objs["Deployment/demo-incident/demo-api"].GetResourceVersion(), "prepare changes nothing")

	// a status-only update (resourceVersion bump) does not invalidate the approval
	f.objs["Deployment/demo-incident/demo-api"].SetResourceVersion("2")
	p2, err := ex.(actions.Preparer).Prepare(ctx, memoryPatch(), tgt)
	require.NoError(t, err)
	require.Equal(t, p.Precondition, p2.Precondition)

	// a real change after approval: refuse
	f.objs["Deployment/demo-incident/demo-api"].SetLabels(map[string]string{"changed": "yes"})
	_, err = ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, memoryPatch(), tgt, p)
	require.ErrorIs(t, err, actions.ErrPreconditionChanged)

	// re-prepared: executes
	p, err = ex.(actions.Preparer).Prepare(ctx, memoryPatch(), tgt)
	require.NoError(t, err)
	out, err := ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, memoryPatch(), tgt, p)
	require.NoError(t, err)
	require.Contains(t, out, "64Mi → 256Mi")
	cs, _, _ := unstructured.NestedSlice(f.objs["Deployment/demo-incident/demo-api"].Object, "spec", "template", "spec", "containers")
	require.Equal(t, "256Mi", cs[0].(map[string]any)["resources"].(map[string]any)["limits"].(map[string]any)["memory"])

	// the rollback action restores the previous value
	rb := p.RollbackAction
	rp, err := ex.(actions.Preparer).Prepare(ctx, rb.Params, tgt)
	require.NoError(t, err)
	require.Contains(t, rp.Effect, "256Mi → 64Mi")
}

func TestCoreNamespaceAndDryRunFailure(t *testing.T) {
	f := newFake(deployment("vault", nil))
	ex := byType(Executors(fakeBackend{f}, cfg(false)), "k8s.patch")
	params := memoryPatch()
	params["namespace"] = "vault"
	p, err := ex.(actions.Preparer).Prepare(context.Background(), params, tgt)
	require.NoError(t, err)
	require.True(t, p.CoreInfrastructure)
	require.Equal(t, "high", p.Risk)

	f.dryRunErr = errors.New(`admission webhook "kyverno" denied the request`)
	p, err = ex.(actions.Preparer).Prepare(context.Background(), params, tgt)
	require.NoError(t, err)
	require.False(t, p.DryRunOK)
	require.Contains(t, p.DryRun, "kyverno")
}

func TestApplyCreateAndUpdate(t *testing.T) {
	f := newFake()
	ex := byType(Executors(fakeBackend{f}, cfg(false)), "k8s.apply")
	ctx := context.Background()
	cm := map[string]any{"manifest": map[string]any{"apiVersion": "v1", "kind": "ConfigMap",
		"metadata": map[string]any{"name": "demo-config"}, "data": map[string]any{"CACHE_MB": "50"}}}
	p, err := ex.(actions.Preparer).Prepare(ctx, cm, tgt)
	require.NoError(t, err)
	require.Equal(t, "absent", p.Precondition)
	require.Contains(t, p.Operation, "create")
	require.Equal(t, "k8s.delete", p.RollbackAction.Type)
	require.Contains(t, p.DryRun, "+data:")
	require.Contains(t, p.Payload, "namespace: demo-incident", "namespace defaulted to the incident's")
	_, err = ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, cm, tgt, p)
	require.NoError(t, err)
	require.Contains(t, f.objs, "ConfigMap/demo-incident/demo-config")

	// prepared as "absent" but now exists: refuse
	_, err = ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, cm, tgt, p)
	require.ErrorIs(t, err, actions.ErrPreconditionChanged)

	cm["manifest"].(map[string]any)["data"] = map[string]any{"CACHE_MB": "20"}
	p, err = ex.(actions.Preparer).Prepare(ctx, cm, tgt)
	require.NoError(t, err)
	require.Contains(t, p.Operation, "update")
	require.Contains(t, p.Effect, "data.CACHE_MB: 50 → 20")
	require.Equal(t, "k8s.patch", p.RollbackAction.Type)
}

func TestDeletePreparesRecreateRollback(t *testing.T) {
	pvc := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim",
		"metadata": map[string]any{"name": "data", "namespace": "demo-incident", "resourceVersion": "5"}}}
	f := newFake(pvc)
	ex := byType(Executors(fakeBackend{f}, cfg(false)), "k8s.delete")
	ctx := context.Background()
	params := map[string]any{"apiVersion": "v1", "kind": "PersistentVolumeClaim", "name": "data"}
	p, err := ex.(actions.Preparer).Prepare(ctx, params, tgt)
	require.NoError(t, err)
	require.Equal(t, "high", p.Risk)
	require.Contains(t, strings.Join(p.Warnings, " "), "lose data")
	require.Equal(t, "k8s.apply", p.RollbackAction.Type)
	require.Contains(t, p.Payload, "name: data")

	f.objs["PersistentVolumeClaim/demo-incident/data"].SetLabels(map[string]string{"changed": "yes"})
	_, err = ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, params, tgt, p)
	require.ErrorIs(t, err, actions.ErrPreconditionChanged)
	p, _ = ex.(actions.Preparer).Prepare(ctx, params, tgt)
	_, err = ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, params, tgt, p)
	require.NoError(t, err)
	require.NotContains(t, f.objs, "PersistentVolumeClaim/demo-incident/data")
}

func TestExec(t *testing.T) {
	pod := &unstructured.Unstructured{Object: map[string]any{"apiVersion": "v1", "kind": "Pod",
		"metadata": map[string]any{"name": "redis-0", "namespace": "demo-incident", "uid": "pod-uid-1"},
		"spec":     map[string]any{"containers": []any{map[string]any{"name": "redis"}}},
		"status":   map[string]any{"phase": "Running"}}}
	f := newFake(pod)
	ex := byType(Executors(fakeBackend{f}, cfg(true)), "kubectl.exec")
	require.NotNil(t, ex)
	ctx := context.Background()
	params := map[string]any{"pod": "redis-0", "command": []any{"redis-cli", "FLUSHALL"}}
	p, err := ex.(actions.Preparer).Prepare(ctx, params, tgt)
	require.NoError(t, err)
	require.Equal(t, "pod-uid-1", p.Precondition)
	require.Contains(t, p.Payload, `"FLUSHALL"`)
	require.Contains(t, p.Target, "container redis")
	require.Equal(t, "high", p.Risk)
	f.execRes = ExecResult{Stdout: "OK", ExitCode: 0}
	out, err := ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, params, tgt, p)
	require.NoError(t, err)
	require.Contains(t, out, "OK")
	require.Equal(t, [][]string{{"redis-cli", "FLUSHALL"}}, f.execs)

	f.execRes = ExecResult{Stderr: "boom", ExitCode: 2}
	out, err = ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, params, tgt, p)
	require.Error(t, err)
	require.Contains(t, out, "boom")

	f.objs["Pod/demo-incident/redis-0"].SetUID("pod-uid-2") // pod replaced
	_, err = ex.(actions.PreconditionExecutor).ExecutePrepared(ctx, params, tgt, p)
	require.ErrorIs(t, err, actions.ErrPreconditionChanged)
}

func TestSchemasAndValidation(t *testing.T) {
	reg := actions.NewRegistry(nil, nil)
	for _, e := range Executors(fakeBackend{newFake()}, cfg(true)) {
		require.NoError(t, reg.Register(e), e.Type())
	}
	ctx := context.Background()
	require.NoError(t, reg.Validate(ctx, "k8s.patch", memoryPatch(), tgt))
	bad := memoryPatch()
	delete(bad, "name")
	require.Error(t, reg.Validate(ctx, "k8s.patch", bad, tgt))
	bad = memoryPatch()
	bad["patch_type"] = "json"
	require.Error(t, reg.Validate(ctx, "k8s.patch", bad, tgt), "json patch must be an array")
	bad = memoryPatch()
	bad["name"] = "Not_A_Name"
	require.Error(t, reg.Validate(ctx, "k8s.patch", bad, tgt))
	require.NoError(t, reg.Validate(ctx, "k8s.patch", map[string]any{"apiVersion": "apps/v1", "kind": "Deployment", "name": "x",
		"patch_type": "json", "patch": []any{map[string]any{"op": "replace", "path": "/spec/replicas", "value": 3}}}, tgt))
	require.Error(t, reg.Validate(ctx, "kubectl.exec", map[string]any{"pod": "p", "command": []any{}}, tgt))
	require.Error(t, reg.Validate(ctx, "k8s.apply", map[string]any{"manifest": map[string]any{"kind": "ConfigMap"}}, tgt))
	require.NoError(t, reg.Validate(ctx, "k8s.delete", map[string]any{"apiVersion": "v1", "kind": "Pod", "name": "p"}, tgt))
}
