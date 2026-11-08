package k8s

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"
	k8stesting "k8s.io/client-go/testing"

	"github.com/xonoko/infra-monitor/internal/actions"
	"github.com/xonoko/infra-monitor/internal/config"
)

type fakeClients map[string]kubernetes.Interface

func (f fakeClients) For(cluster string) (kubernetes.Interface, error) {
	c, ok := f[cluster]
	if !ok {
		return nil, errors.New("unknown cluster " + cluster)
	}
	return c, nil
}

var policy = Policy{NamespaceDeny: []string{"kube-system"}, MaxReplicas: 5}

func ptr[T any](v T) *T { return &v }

func registry(t *testing.T, cl Clients) *actions.Registry {
	r := actions.NewRegistry(nil, nil)
	for _, e := range Executors(cl, policy) {
		require.NoError(t, r.Register(e))
	}
	return r
}

func TestRolloutRestart(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&appsv1.Deployment{ObjectMeta: metav1.ObjectMeta{Name: "api", Namespace: "shop"}},
		&appsv1.StatefulSet{ObjectMeta: metav1.ObjectMeta{Name: "db", Namespace: "shop"}},
		&appsv1.DaemonSet{ObjectMeta: metav1.ObjectMeta{Name: "agent", Namespace: "shop"}},
	)
	e := NewRolloutRestart(fakeClients{"manager": cs}, policy)
	fixed := time.Date(2026, 11, 8, 12, 0, 0, 0, time.UTC)
	e.now = func() time.Time { return fixed }
	ctx := context.Background()
	tgt := actions.Target{Cluster: "manager", Namespace: "shop", IncidentID: 1}

	r := registry(t, fakeClients{"manager": cs})
	require.NoError(t, r.Validate(ctx, "k8s.rollout_restart", map[string]any{"kind": "deployment", "name": "api"}, tgt))
	require.Error(t, r.Validate(ctx, "k8s.rollout_restart", map[string]any{"kind": "job", "name": "api"}, tgt))
	require.Error(t, r.Validate(ctx, "k8s.rollout_restart", map[string]any{"kind": "deployment"}, tgt))
	require.Error(t, r.Validate(ctx, "k8s.rollout_restart", map[string]any{"kind": "deployment", "name": "api", "extra": 1}, tgt))

	out, err := e.Execute(ctx, map[string]any{"kind": "deployment", "name": "api"}, tgt)
	require.NoError(t, err)
	require.Contains(t, out, "restarted deployment/api in namespace shop (cluster manager)")
	d, err := cs.AppsV1().Deployments("shop").Get(ctx, "api", metav1.GetOptions{})
	require.NoError(t, err)
	require.Equal(t, "2026-11-08T12:00:00Z", d.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"])

	for _, kind := range []string{"statefulset", "daemonset"} {
		name := map[string]string{"statefulset": "db", "daemonset": "agent"}[kind]
		_, err = e.Execute(ctx, map[string]any{"kind": kind, "name": name}, tgt)
		require.NoError(t, err, kind)
	}
	ss, _ := cs.AppsV1().StatefulSets("shop").Get(ctx, "db", metav1.GetOptions{})
	require.NotEmpty(t, ss.Spec.Template.Annotations["kubectl.kubernetes.io/restartedAt"])

	require.Equal(t, "rollout restart deployment/api -n shop (cluster manager)", e.Summary(map[string]any{"kind": "deployment", "name": "api"}, tgt))

	_, err = e.Execute(ctx, map[string]any{"kind": "deployment", "name": "missing"}, tgt)
	require.True(t, apierrors.IsNotFound(errors.Unwrap(err)), err)
}

func TestNamespacePolicy(t *testing.T) {
	cs := fake.NewSimpleClientset()
	ctx := context.Background()
	e := NewRolloutRestart(fakeClients{"": cs}, policy)
	p := map[string]any{"kind": "deployment", "name": "coredns"}
	require.ErrorContains(t, e.Validate(ctx, p, actions.Target{Namespace: "kube-system"}), "denied")
	require.ErrorContains(t, e.Validate(ctx, p, actions.Target{}), "namespace is required")
	require.ErrorContains(t, e.Validate(ctx, map[string]any{"kind": "deployment", "name": "x", "namespace": "kube-system"}, actions.Target{Namespace: "shop"}), "denied")
	require.ErrorContains(t, e.Validate(ctx, map[string]any{"kind": "deployment", "name": "Bad_Name"}, actions.Target{Namespace: "shop"}), "invalid name")

	allow := NewDeletePod(fakeClients{"": cs}, Policy{NamespaceAllow: []string{"shop"}, MaxReplicas: 1})
	require.NoError(t, allow.Validate(ctx, map[string]any{"name": "p"}, actions.Target{Namespace: "shop"}))
	require.ErrorContains(t, allow.Validate(ctx, map[string]any{"name": "p"}, actions.Target{Namespace: "other"}), "namespace_allow")

	// Execute re-validates (defense in depth)
	_, err := e.Execute(ctx, p, actions.Target{Namespace: "kube-system"})
	require.Error(t, err)
	require.Empty(t, cs.Actions())
}

func TestDeletePod(t *testing.T) {
	cs := fake.NewSimpleClientset(
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "api-1", Namespace: "shop",
			OwnerReferences: []metav1.OwnerReference{{Kind: "ReplicaSet", Name: "api-7d9f", Controller: ptr(true)}}}},
		&corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "lonely", Namespace: "shop"}},
	)
	e := NewDeletePod(fakeClients{"manager": cs}, policy)
	ctx := context.Background()
	tgt := actions.Target{Cluster: "manager", Namespace: "shop"}
	out, err := e.Execute(ctx, map[string]any{"name": "api-1"}, tgt)
	require.NoError(t, err)
	require.Contains(t, out, "owned by ReplicaSet/api-7d9f")
	_, err = cs.CoreV1().Pods("shop").Get(ctx, "api-1", metav1.GetOptions{})
	require.True(t, apierrors.IsNotFound(err))

	out, err = e.Execute(ctx, map[string]any{"name": "lonely"}, tgt)
	require.NoError(t, err)
	require.Contains(t, out, "will NOT be recreated")

	_, err = e.Execute(ctx, map[string]any{"name": "ghost"}, tgt)
	require.Error(t, err)
	_, err = e.Execute(ctx, map[string]any{"name": "api-1"}, actions.Target{Cluster: "other", Namespace: "shop"})
	require.ErrorContains(t, err, "unknown cluster")
}

func TestScale(t *testing.T) {
	cs := fake.NewSimpleClientset()
	replicas := map[string]int32{"deployments/api": 3, "statefulsets/db": 1}
	cs.PrependReactor("get", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ga, ok := a.(k8stesting.GetAction)
		if !ok || a.GetSubresource() != "scale" {
			return false, nil, nil
		}
		key := a.GetResource().Resource + "/" + ga.GetName()
		n, ok := replicas[key]
		if !ok {
			return true, nil, apierrors.NewNotFound(a.GetResource().GroupResource(), ga.GetName())
		}
		return true, &autoscalingv1.Scale{ObjectMeta: metav1.ObjectMeta{Name: ga.GetName(), Namespace: a.GetNamespace()}, Spec: autoscalingv1.ScaleSpec{Replicas: n}}, nil
	})
	cs.PrependReactor("update", "*", func(a k8stesting.Action) (bool, runtime.Object, error) {
		ua, ok := a.(k8stesting.UpdateAction)
		if !ok || a.GetSubresource() != "scale" {
			return false, nil, nil
		}
		sc := ua.GetObject().(*autoscalingv1.Scale)
		replicas[a.GetResource().Resource+"/"+sc.Name] = sc.Spec.Replicas
		return true, sc, nil
	})
	e := NewScale(fakeClients{"manager": cs}, policy)
	ctx := context.Background()
	tgt := actions.Target{Cluster: "manager", Namespace: "shop"}

	r := registry(t, fakeClients{"manager": cs})
	require.NoError(t, r.Validate(ctx, "k8s.scale", map[string]any{"kind": "deployment", "name": "api", "replicas": 0}, tgt))
	require.Error(t, r.Validate(ctx, "k8s.scale", map[string]any{"kind": "deployment", "name": "api", "replicas": 6}, tgt), "schema bound from policy")
	require.Error(t, r.Validate(ctx, "k8s.scale", map[string]any{"kind": "deployment", "name": "api", "replicas": 1.5}, tgt))
	require.Error(t, r.Validate(ctx, "k8s.scale", map[string]any{"kind": "daemonset", "name": "api", "replicas": 1}, tgt))

	// params as they arrive from JSON (float64)
	var p map[string]any
	require.NoError(t, json.Unmarshal([]byte(`{"kind":"deployment","name":"api","replicas":1}`), &p))
	out, err := e.Execute(ctx, p, tgt)
	require.NoError(t, err)
	require.Contains(t, out, "from 3 to 1 replicas")
	require.Equal(t, int32(1), replicas["deployments/api"])

	out, err = e.Execute(ctx, map[string]any{"kind": "statefulset", "name": "db", "replicas": 2}, tgt)
	require.NoError(t, err)
	require.Contains(t, out, "scaled statefulset/db")
	require.Equal(t, int32(2), replicas["statefulsets/db"])
	require.Equal(t, "scale statefulset/db -n shop to 2 replicas (cluster manager)", e.Summary(map[string]any{"kind": "statefulset", "name": "db", "replicas": 2}, tgt))

	_, err = e.Execute(ctx, map[string]any{"kind": "deployment", "name": "api", "replicas": 9}, tgt)
	require.ErrorContains(t, err, "between 0 and 5")
}

func TestClientSetSelection(t *testing.T) {
	dir := t.TempDir()
	kc := filepath.Join(dir, "kubeconfig")
	require.NoError(t, os.WriteFile(kc, []byte(`apiVersion: v1
kind: Config
clusters: [{name: edge, cluster: {server: "https://edge.example:6443"}}]
users: [{name: u, user: {token: abc}}]
contexts: [{name: c, context: {cluster: edge, user: u}}]
current-context: c
`), 0o600))
	inClusterCalls := 0
	newSet := func(cfg config.ActionsConfig) *ClientSet {
		cs, err := NewClients(cfg)
		require.NoError(t, err)
		cs.inCluster = func() (*rest.Config, error) {
			inClusterCalls++
			return &rest.Config{Host: "https://in-cluster:443"}, nil
		}
		return cs
	}

	cs := newSet(config.ActionsConfig{InCluster: true, InClusterName: "manager", Kubeconfigs: map[string]string{"edge": kc}})
	a, err := cs.For("edge")
	require.NoError(t, err)
	b, err := cs.For("edge")
	require.NoError(t, err)
	require.Same(t, a, b, "cached")
	_, err = cs.For("manager")
	require.NoError(t, err)
	_, err = cs.For("")
	require.NoError(t, err)
	_, err = cs.For("prod")
	require.ErrorContains(t, err, `no kubernetes credentials configured for cluster "prod"`)
	require.Equal(t, 2, inClusterCalls)

	any := newSet(config.ActionsConfig{InCluster: true})
	_, err = any.For("whatever")
	require.NoError(t, err)

	none := newSet(config.ActionsConfig{InCluster: false})
	_, err = none.For("manager")
	require.Error(t, err)

	bad := newSet(config.ActionsConfig{Kubeconfigs: map[string]string{"x": filepath.Join(dir, "missing")}})
	_, err = bad.For("x")
	require.ErrorContains(t, err, "load kubeconfig")

	_, err = NewClients(config.ActionsConfig{Kubeconfigs: map[string]string{"x": ""}})
	require.Error(t, err)
}
