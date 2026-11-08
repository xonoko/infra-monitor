// Package generic implements approved-only Kubernetes actions: k8s.patch, k8s.apply, k8s.delete
// and kubectl.exec. Every proposal is prepared (exact target, full payload, server-side dry-run,
// diff, rollback, precondition) and runs only after a human approved that exact payload, under an
// impersonated ServiceAccount distinct from the one used for automatic actions.
package generic

import (
	"bytes"
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/tools/remotecommand"
	utilexec "k8s.io/client-go/util/exec"

	"github.com/xonoko/infra-monitor/internal/actions/k8s"
)

// FieldManager is used for server-side apply and patches.
const FieldManager = "infra-monitor"

// Ref identifies one object.
type Ref struct {
	APIVersion string
	Kind       string
	Namespace  string
	Name       string
}

func (r Ref) String() string {
	s := r.APIVersion + " " + r.Kind + " " + r.Name
	if r.Namespace != "" {
		s = r.Namespace + " / " + s
	}
	return s
}

// ExecResult is the outcome of a command run in a container.
type ExecResult struct {
	Stdout, Stderr string
	ExitCode       int
}

// Cluster is the set of operations the executors need on one cluster. The real implementation
// uses the dynamic client; tests use a fake (the client-go fake ignores dry-run).
type Cluster interface {
	// Namespaced reports whether the kind is namespaced (resolves through discovery).
	Namespaced(ctx context.Context, apiVersion, kind string) (bool, error)
	Get(ctx context.Context, ref Ref) (*unstructured.Unstructured, error)
	Patch(ctx context.Context, ref Ref, pt types.PatchType, data []byte, dryRun bool) (*unstructured.Unstructured, error)
	Apply(ctx context.Context, obj *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error)
	Delete(ctx context.Context, ref Ref, resourceVersion string, propagation *metav1.DeletionPropagation, dryRun bool) error
	NamespaceAnnotations(ctx context.Context, ns string) (map[string]string, error)
	Exec(ctx context.Context, ns, pod, container string, command []string, timeout time.Duration) (ExecResult, error)
}

// Backend returns the Cluster for an alert's cluster label.
type Backend interface {
	For(cluster string) (Cluster, error)
}

// NewBackend uses the k8s client set; every operation impersonates `impersonate` when non-empty.
func NewBackend(cs *k8s.ClientSet, impersonate string) Backend {
	return &dynBackend{cs: cs, impersonate: impersonate}
}

type dynBackend struct {
	cs          *k8s.ClientSet
	impersonate string
}

func (b *dynBackend) For(cluster string) (Cluster, error) {
	d, err := b.cs.Dynamic(cluster, b.impersonate)
	if err != nil {
		return nil, err
	}
	return &dynCluster{cs: b.cs, cluster: cluster, impersonate: b.impersonate, dyn: d}, nil
}

type dynCluster struct {
	cs          *k8s.ClientSet
	cluster     string
	impersonate string
	dyn         dynamic.Interface
}

func (c *dynCluster) mapping(apiVersion, kind string) (*meta.RESTMapping, error) {
	gv, err := schema.ParseGroupVersion(apiVersion)
	if err != nil {
		return nil, fmt.Errorf("invalid apiVersion %q: %w", apiVersion, err)
	}
	m, err := c.cs.Mapper(c.cluster)
	if err != nil {
		return nil, err
	}
	gk := schema.GroupKind{Group: gv.Group, Kind: kind}
	mp, err := m.RESTMapping(gk, gv.Version)
	if meta.IsNoMatchError(err) {
		c.cs.ResetMapper(c.cluster)
		mp, err = m.RESTMapping(gk, gv.Version)
	}
	if err != nil {
		return nil, fmt.Errorf("unknown kind %s %s: %w", apiVersion, kind, err)
	}
	return mp, nil
}

func (c *dynCluster) resource(ref Ref) (dynamic.ResourceInterface, error) {
	mp, err := c.mapping(ref.APIVersion, ref.Kind)
	if err != nil {
		return nil, err
	}
	if mp.Scope.Name() == meta.RESTScopeNameNamespace {
		if ref.Namespace == "" {
			return nil, fmt.Errorf("%s is namespaced: namespace is required", ref.Kind)
		}
		return c.dyn.Resource(mp.Resource).Namespace(ref.Namespace), nil
	}
	return c.dyn.Resource(mp.Resource), nil
}

func (c *dynCluster) Namespaced(_ context.Context, apiVersion, kind string) (bool, error) {
	mp, err := c.mapping(apiVersion, kind)
	if err != nil {
		return false, err
	}
	return mp.Scope.Name() == meta.RESTScopeNameNamespace, nil
}

func dryRunOpt(dry bool) []string {
	if dry {
		return []string{metav1.DryRunAll}
	}
	return nil
}

func (c *dynCluster) Get(ctx context.Context, ref Ref) (*unstructured.Unstructured, error) {
	ri, err := c.resource(ref)
	if err != nil {
		return nil, err
	}
	return ri.Get(ctx, ref.Name, metav1.GetOptions{})
}

func (c *dynCluster) Patch(ctx context.Context, ref Ref, pt types.PatchType, data []byte, dryRun bool) (*unstructured.Unstructured, error) {
	ri, err := c.resource(ref)
	if err != nil {
		return nil, err
	}
	return ri.Patch(ctx, ref.Name, pt, data, metav1.PatchOptions{DryRun: dryRunOpt(dryRun), FieldManager: FieldManager})
}

func (c *dynCluster) Apply(ctx context.Context, obj *unstructured.Unstructured, dryRun bool) (*unstructured.Unstructured, error) {
	ri, err := c.resource(Ref{APIVersion: obj.GetAPIVersion(), Kind: obj.GetKind(), Namespace: obj.GetNamespace(), Name: obj.GetName()})
	if err != nil {
		return nil, err
	}
	return ri.Apply(ctx, obj.GetName(), obj, metav1.ApplyOptions{DryRun: dryRunOpt(dryRun), FieldManager: FieldManager, Force: false})
}

func (c *dynCluster) Delete(ctx context.Context, ref Ref, rv string, propagation *metav1.DeletionPropagation, dryRun bool) error {
	ri, err := c.resource(ref)
	if err != nil {
		return err
	}
	opts := metav1.DeleteOptions{DryRun: dryRunOpt(dryRun), PropagationPolicy: propagation}
	if rv != "" {
		opts.Preconditions = &metav1.Preconditions{ResourceVersion: &rv}
	}
	return ri.Delete(ctx, ref.Name, opts)
}

func (c *dynCluster) NamespaceAnnotations(ctx context.Context, ns string) (map[string]string, error) {
	o, err := c.Get(ctx, Ref{APIVersion: "v1", Kind: "Namespace", Name: ns})
	if err != nil {
		return nil, err
	}
	return o.GetAnnotations(), nil
}

// limitedBuffer keeps at most max bytes.
type limitedBuffer struct {
	buf       bytes.Buffer
	max       int
	truncated bool
}

func (l *limitedBuffer) Write(p []byte) (int, error) {
	if room := l.max - l.buf.Len(); room > 0 {
		if len(p) > room {
			l.buf.Write(p[:room])
			l.truncated = true
		} else {
			l.buf.Write(p)
		}
	} else if len(p) > 0 {
		l.truncated = true
	}
	return len(p), nil
}

func (l *limitedBuffer) String() string {
	if l.truncated {
		return l.buf.String() + "\n…(truncated)"
	}
	return l.buf.String()
}

const execOutputLimit = 16 << 10

func (c *dynCluster) Exec(ctx context.Context, ns, pod, container string, command []string, timeout time.Duration) (ExecResult, error) {
	// The exec stream needs the raw (untraced) transport: SPDY upgrades the connection.
	rc, err := c.cs.RESTConfig(c.cluster, c.impersonate)
	if err != nil {
		return ExecResult{}, err
	}
	cl, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return ExecResult{}, err
	}
	req := cl.CoreV1().RESTClient().Post().Resource("pods").Namespace(ns).Name(pod).SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{Container: container, Command: command, Stdout: true, Stderr: true}, scheme.ParameterCodec)
	ex, err := remotecommand.NewSPDYExecutor(rc, "POST", req.URL())
	if err != nil {
		return ExecResult{}, err
	}
	if timeout <= 0 {
		timeout = time.Minute
	}
	ectx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()
	stdout := &limitedBuffer{max: execOutputLimit}
	stderr := &limitedBuffer{max: execOutputLimit}
	err = ex.StreamWithContext(ectx, remotecommand.StreamOptions{Stdout: stdout, Stderr: stderr})
	res := ExecResult{Stdout: stdout.String(), Stderr: stderr.String()}
	var exitErr utilexec.CodeExitError
	if errors.As(err, &exitErr) {
		res.ExitCode = exitErr.Code
		return res, nil
	}
	if err != nil && strings.Contains(err.Error(), "command terminated with exit code") {
		res.ExitCode = -1
		return res, nil
	}
	return res, err
}
