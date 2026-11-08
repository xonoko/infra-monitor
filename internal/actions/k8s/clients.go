// Package k8s implements Kubernetes remediation executors (rollout restart, delete pod, scale)
// and per-cluster client selection (in-cluster ServiceAccount or kubeconfig per `cluster` label).
package k8s

import (
	"fmt"
	"net/http"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"k8s.io/apimachinery/pkg/api/meta"
	"k8s.io/client-go/discovery"
	"k8s.io/client-go/discovery/cached/memory"
	"k8s.io/client-go/dynamic"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/restmapper"
	"k8s.io/client-go/tools/clientcmd"

	"github.com/xonoko/infra-monitor/internal/config"
)

// Clients returns a Kubernetes client for a cluster (the alert's `cluster` label).
type Clients interface {
	For(cluster string) (kubernetes.Interface, error)
}

// ClientSet loads clients lazily and caches them.
type ClientSet struct {
	cfg config.ActionsConfig

	mu    sync.Mutex
	cache map[string]kubernetes.Interface
	// inCluster is replaceable in tests.
	inCluster func() (*rest.Config, error)

	extraMu  sync.Mutex
	dynamics map[string]dynamic.Interface
	mappers  map[string]meta.ResettableRESTMapper
}

// NewClients creates a lazy client set; nothing connects until For is called.
func NewClients(cfg config.ActionsConfig) (*ClientSet, error) {
	for name, path := range cfg.Kubeconfigs {
		if path == "" {
			return nil, fmt.Errorf("actions.kubeconfigs[%s]: empty path", name)
		}
	}
	return &ClientSet{cfg: cfg, cache: map[string]kubernetes.Interface{}, inCluster: rest.InClusterConfig}, nil
}

// For returns the client for cluster. A kubeconfig configured for the cluster wins;
// otherwise the in-cluster ServiceAccount serves cluster == in_cluster_name, any cluster when
// in_cluster_name is empty, and alerts without a cluster label.
func (c *ClientSet) For(cluster string) (kubernetes.Interface, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if cl, ok := c.cache[cluster]; ok {
		return cl, nil
	}
	rc, err := c.restConfig(cluster)
	if err != nil {
		return nil, err
	}
	rc = rest.CopyConfig(rc)
	rc.UserAgent = "infra-monitor"
	rc.Wrap(func(rt http.RoundTripper) http.RoundTripper { return otelhttp.NewTransport(rt) })
	cl, err := kubernetes.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("kubernetes client for cluster %q: %w", cluster, err)
	}
	c.cache[cluster] = cl
	return cl, nil
}

func (c *ClientSet) restConfig(cluster string) (*rest.Config, error) {
	if path, ok := c.cfg.Kubeconfigs[cluster]; ok {
		rc, err := clientcmd.BuildConfigFromFlags("", path)
		if err != nil {
			return nil, fmt.Errorf("load kubeconfig for cluster %q: %w", cluster, err)
		}
		return rc, nil
	}
	if c.cfg.InCluster && (cluster == "" || c.cfg.InClusterName == "" || c.cfg.InClusterName == cluster) {
		rc, err := c.inCluster()
		if err != nil {
			return nil, fmt.Errorf("in-cluster config for cluster %q: %w", cluster, err)
		}
		return rc, nil
	}
	return nil, fmt.Errorf("no kubernetes credentials configured for cluster %q (set actions.kubeconfigs or actions.in_cluster_name)", cluster)
}

// RESTConfig returns a copy of the cluster's REST config, optionally impersonating a user
// (e.g. "system:serviceaccount:infra-monitor:infra-monitor-approved"). The copy is not traced:
// callers that build HTTP clients wrap it themselves (exec streams must not be wrapped).
func (c *ClientSet) RESTConfig(cluster, impersonate string) (*rest.Config, error) {
	c.mu.Lock()
	rc, err := c.restConfig(cluster)
	c.mu.Unlock()
	if err != nil {
		return nil, err
	}
	rc = rest.CopyConfig(rc)
	rc.UserAgent = "infra-monitor"
	if impersonate != "" {
		rc.Impersonate = rest.ImpersonationConfig{UserName: impersonate}
	}
	return rc, nil
}

// tracedConfig returns RESTConfig wrapped with otelhttp.
func (c *ClientSet) tracedConfig(cluster, impersonate string) (*rest.Config, error) {
	rc, err := c.RESTConfig(cluster, impersonate)
	if err != nil {
		return nil, err
	}
	rc.Wrap(func(rt http.RoundTripper) http.RoundTripper { return otelhttp.NewTransport(rt) })
	return rc, nil
}

// Dynamic returns a cached dynamic client for cluster, optionally impersonating a user.
func (c *ClientSet) Dynamic(cluster, impersonate string) (dynamic.Interface, error) {
	key := cluster + "\x00" + impersonate
	c.extraMu.Lock()
	defer c.extraMu.Unlock()
	if c.dynamics == nil {
		c.dynamics = map[string]dynamic.Interface{}
	}
	if d, ok := c.dynamics[key]; ok {
		return d, nil
	}
	rc, err := c.tracedConfig(cluster, impersonate)
	if err != nil {
		return nil, err
	}
	d, err := dynamic.NewForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("dynamic client for cluster %q: %w", cluster, err)
	}
	c.dynamics[key] = d
	return d, nil
}

// Typed returns a typed clientset for cluster, optionally impersonating a user (not cached).
func (c *ClientSet) Typed(cluster, impersonate string) (kubernetes.Interface, error) {
	rc, err := c.tracedConfig(cluster, impersonate)
	if err != nil {
		return nil, err
	}
	return kubernetes.NewForConfig(rc)
}

// Mapper returns a cached discovery-backed REST mapper for cluster (discovery uses the pod's own
// identity). Call ResetMapper after a NoKindMatch error to refresh it (new CRDs).
func (c *ClientSet) Mapper(cluster string) (meta.ResettableRESTMapper, error) {
	c.extraMu.Lock()
	defer c.extraMu.Unlock()
	if c.mappers == nil {
		c.mappers = map[string]meta.ResettableRESTMapper{}
	}
	if m, ok := c.mappers[cluster]; ok {
		return m, nil
	}
	rc, err := c.tracedConfig(cluster, "")
	if err != nil {
		return nil, err
	}
	dc, err := discovery.NewDiscoveryClientForConfig(rc)
	if err != nil {
		return nil, fmt.Errorf("discovery client for cluster %q: %w", cluster, err)
	}
	m := restmapper.NewDeferredDiscoveryRESTMapper(memory.NewMemCacheClient(dc))
	c.mappers[cluster] = m
	return m, nil
}

// ResetMapper drops cached discovery for cluster.
func (c *ClientSet) ResetMapper(cluster string) {
	c.extraMu.Lock()
	defer c.extraMu.Unlock()
	if m, ok := c.mappers[cluster]; ok {
		m.Reset()
	}
}
