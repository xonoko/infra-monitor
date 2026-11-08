package incident

import (
	"context"
	"slices"
	"strconv"
	"strings"

	"github.com/xonoko/infra-monitor/internal/db/ent"
)

// NamespaceAnnotations reads Kubernetes namespace annotations (implemented by actions/k8s).
type NamespaceAnnotations interface {
	Annotations(ctx context.Context, cluster, namespace string) (map[string]string, error)
}

// autoPolicy is the effective auto-approve policy for an incident.
type autoPolicy struct {
	types          []string
	maxAutoPerHour int
	source         string // "config" or "namespace <ns>"
}

// autoPolicy starts from actions.auto_approve / max_auto_per_hour and applies the incident
// namespace's annotations:
//
//	<prefix>/auto-approve: "agent.reinvestigate,k8s.delete_pod"   ("none" or "" = nothing automatic)
//	<prefix>/max-auto-per-hour: "1"
//
// If the namespace can't be read, the global config applies.
func (s *Service) autoPolicy(ctx context.Context, inc *ent.Incident) autoPolicy {
	p := autoPolicy{types: s.cfg.Actions.AutoApprove, maxAutoPerHour: s.cfg.Actions.MaxAutoPerHour, source: "config"}
	if s.nsAnnotations == nil || inc.Namespace == "" {
		return p
	}
	ann, err := s.nsAnnotations.Annotations(ctx, inc.Cluster, inc.Namespace)
	if err != nil {
		s.log.DebugContext(ctx, "namespace annotations unavailable, using config", "cluster", inc.Cluster, "namespace", inc.Namespace, "error", err)
		return p
	}
	prefix := strings.TrimSuffix(s.cfg.Actions.AnnotationPrefix, "/") + "/"
	if v, ok := ann[prefix+"auto-approve"]; ok {
		p.types = nil
		for _, t := range strings.Split(v, ",") {
			if t = strings.TrimSpace(t); t != "" && t != "none" {
				p.types = append(p.types, t)
			}
		}
		p.source = "namespace " + inc.Namespace
	}
	if v, ok := ann[prefix+"max-auto-per-hour"]; ok {
		if n, err := strconv.Atoi(strings.TrimSpace(v)); err == nil && n >= 0 {
			p.maxAutoPerHour = n
			p.source = "namespace " + inc.Namespace
		}
	}
	return p
}

func (p autoPolicy) allows(typ string) bool { return slices.Contains(p.types, typ) }
