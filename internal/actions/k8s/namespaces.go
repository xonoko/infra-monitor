package k8s

import (
	"context"
	"sync"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// NamespaceAnnotations reads namespace annotations through the cluster's client, cached for a minute.
type NamespaceAnnotations struct {
	clients Clients
	ttl     time.Duration

	mu    sync.Mutex
	cache map[string]nsEntry
}

type nsEntry struct {
	annotations map[string]string
	err         error
	at          time.Time
}

func NewNamespaceAnnotations(clients Clients) *NamespaceAnnotations {
	return &NamespaceAnnotations{clients: clients, ttl: time.Minute, cache: map[string]nsEntry{}}
}

// Annotations returns the annotations of namespace ns in cluster.
func (n *NamespaceAnnotations) Annotations(ctx context.Context, cluster, ns string) (map[string]string, error) {
	key := cluster + "/" + ns
	n.mu.Lock()
	if e, ok := n.cache[key]; ok && time.Since(e.at) < n.ttl {
		n.mu.Unlock()
		return e.annotations, e.err
	}
	n.mu.Unlock()
	var e nsEntry
	cl, err := n.clients.For(cluster)
	if err != nil {
		e.err = err
	} else {
		cctx, cancel := context.WithTimeout(ctx, 5*time.Second)
		obj, err := cl.CoreV1().Namespaces().Get(cctx, ns, metav1.GetOptions{})
		cancel()
		if err != nil {
			e.err = err
		} else {
			e.annotations = obj.Annotations
		}
	}
	e.at = time.Now()
	n.mu.Lock()
	n.cache[key] = e
	n.mu.Unlock()
	return e.annotations, e.err
}
