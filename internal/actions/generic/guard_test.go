package generic

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestGuard(t *testing.T) {
	require.Error(t, guard("Secret", map[string]any{}))
	require.Error(t, guard("ClusterRoleBinding", map[string]any{}))
	require.Error(t, guard("Deployment", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{"serviceAccountName": "argocd-application-controller"}}}}))
	require.Error(t, guard("Pod", map[string]any{"spec": map[string]any{"automountServiceAccountToken": true}}))
	require.Error(t, guard("Deployment", []any{map[string]any{"op": "replace", "path": "/spec/template/spec/serviceAccountName", "value": "x"}}))
	require.NoError(t, guard("Deployment", map[string]any{"spec": map[string]any{"template": map[string]any{"spec": map[string]any{
		"containers": []any{map[string]any{"name": "app", "resources": map[string]any{"limits": map[string]any{"memory": "256Mi"}}}}}}}}))
}
