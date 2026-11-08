// Package k8s implements Kubernetes remediation executors (rollout restart, delete pod, scale)
// and per-cluster client selection (in-cluster ServiceAccount or kubeconfig per `cluster` label).
package k8s

import (
	"fmt"
	"net/http"
	"sync"

	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
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
