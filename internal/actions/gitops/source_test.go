package gitops

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"
	dynfake "k8s.io/client-go/dynamic/fake"

	"github.com/xonoko/infra-monitor/internal/config"
)

func app(name, cluster, ns string, spec map[string]any) *unstructured.Unstructured {
	spec["destination"] = map[string]any{"name": cluster, "namespace": ns}
	return &unstructured.Unstructured{Object: map[string]any{
		"apiVersion": "argoproj.io/v1alpha1", "kind": "Application",
		"metadata": map[string]any{"name": name, "namespace": "argocd"},
		"spec":     spec,
	}}
}

func newSources(t *testing.T, objs ...runtime.Object) (*Sources, *fakeGitHub) {
	t.Helper()
	e, gh := newTest(t, func(c *config.GitOpsConfig) {
		c.ArgoCDNamespace = "argocd"
		c.PathPrefixes = []string{"gitops/"}
	})
	sc := runtime.NewScheme()
	dyn := dynfake.NewSimpleDynamicClientWithCustomListKinds(sc,
		map[schema.GroupVersionResource]string{applicationGVR: "ApplicationList"}, objs...)
	return NewSources(e, func(string) (dynamic.Interface, error) { return dyn, nil }), gh
}

func TestSourcesDescribeMultiSourceHelmApp(t *testing.T) {
	demo := app("manager-demo-incident", "manager", "demo-incident", map[string]any{
		"sources": []any{
			map[string]any{"ref": "values", "repoURL": "https://github.com/lucasgalton/infra-argocd.git", "targetRevision": "main"},
			map[string]any{"repoURL": "https://github.com/lucasgalton/infra-argocd.git", "path": "gitops/charts/workloads/demo-incident",
				"helm": map[string]any{
					"valueFiles":   []any{"$values/gitops/clusters/manager/values-demo.yaml"},
					"valuesObject": map[string]any{"domain": "manager.xonoko.com"},
				}},
		},
		"syncPolicy": map[string]any{"automated": map[string]any{"selfHeal": true}},
	})
	other := app("manager-other", "manager", "other", map[string]any{"source": map[string]any{
		"repoURL": "https://github.com/lucasgalton/infra-argocd.git", "path": "gitops/charts/other"}})
	s, gh := newSources(t, demo, other)
	gh.files["gitops/charts/workloads/demo-incident/values.yaml"] = "demoApi:\n  resources: {}\n"

	text, err := s.Context(context.Background(), "manager", "demo-incident")
	require.NoError(t, err)
	require.Contains(t, text, "Argo CD Application manager-demo-incident (selfHeal=true)")
	require.Contains(t, text, "chart defaults: gitops/charts/workloads/demo-incident/values.yaml")
	require.Contains(t, text, "values file (highest precedence): gitops/clusters/manager/values-demo.yaml")
	require.Contains(t, text, "memory: 64Mi", "the agent sees the current file content")
	require.Contains(t, text, "inline helm values set in the Application itself (override the files): domain")
	require.NotContains(t, text, "manager-other")

	none, err := s.Context(context.Background(), "manager", "nothing-here")
	require.NoError(t, err)
	require.Empty(t, none)
}

func TestSourcesSkipsOtherReposAndDisallowedPaths(t *testing.T) {
	a := app("manager-x", "manager", "x", map[string]any{"sources": []any{
		map[string]any{"repoURL": "https://github.com/xonoko/infra-monitor.git", "path": "deploy/helm/infra-monitor",
			"helm": map[string]any{"valueFiles": []any{"$values/gitops/clusters/manager/values-x.yaml"}}},
		map[string]any{"ref": "values", "repoURL": "git@github.com:lucasgalton/infra-argocd.git"},
	}})
	b := app("manager-y", "manager", "y", map[string]any{"source": map[string]any{
		"repoURL": "https://github.com/lucasgalton/infra-argocd", "path": "charts/y", "helm": map[string]any{}}})
	s, _ := newSources(t, a, b)

	text, err := s.Context(context.Background(), "manager", "x")
	require.NoError(t, err)
	require.Contains(t, text, "another repository")
	require.Contains(t, text, "gitops/clusters/manager/values-x.yaml (file does not exist yet")

	text, err = s.Context(context.Background(), "manager", "y")
	require.NoError(t, err)
	require.NotContains(t, text, "charts/y/values.yaml", "outside path_prefixes: not offered")
}

func TestSameRepo(t *testing.T) {
	e, _ := newTest(t)
	for _, u := range []string{"https://github.com/lucasgalton/infra-argocd.git", "https://github.com/LucasGalton/infra-argocd/",
		"git@github.com:lucasgalton/infra-argocd.git", "ssh://git@github.com/lucasgalton/infra-argocd"} {
		require.True(t, e.sameRepo(u), u)
	}
	for _, u := range []string{"https://github.com/xonoko/infra-monitor.git", "https://gitlab.com/lucasgalton/infra-argocd", ""} {
		require.False(t, e.sameRepo(u), u)
	}
}

func TestSourcesFallBackToRepoFilesWithoutArgoAccess(t *testing.T) {
	e, gh := newTest(t, func(c *config.GitOpsConfig) {
		c.ArgoCDNamespace = "argocd"
		c.PathPrefixes = []string{"gitops/"}
	})
	gh.files["gitops/clusters/manager/values-demo-incident.yaml"] = "demoApi:\n  resources:\n    limits:\n      memory: 64Mi\n"
	gh.files["gitops/charts/workloads/demo-incident/values.yaml"] = "demoApi: {}\n"
	gh.files["gitops/clusters/manager/values-demo-incident-old.yaml"] = "no: match\n"
	gh.files["other/demo-incident/values.yaml"] = "outside: prefix\n"
	forbidden := errors.New(`applications.argoproj.io is forbidden: User "system:serviceaccount:infra-monitor:infra-monitor" cannot list resource "applications"`)
	s := NewSources(e, func(string) (dynamic.Interface, error) { return nil, forbidden })

	text, err := s.Context(context.Background(), "manager", "demo-incident")
	require.NoError(t, err)
	require.Contains(t, text, "could not read the Argo CD Applications")
	require.Contains(t, text, "- gitops/clusters/manager/values-demo-incident.yaml\n```yaml\ndemoApi:")
	require.Contains(t, text, "- gitops/charts/workloads/demo-incident/values.yaml")
	require.Less(t, strings.Index(text, "workloads/demo-incident/values.yaml"), strings.Index(text, "values-demo-incident.yaml"), "chart defaults first")
	require.NotContains(t, text, "values-demo-incident-old")
	require.NotContains(t, text, "other/demo-incident")

	_, err = s.Context(context.Background(), "manager", "nothing")
	require.ErrorIs(t, err, forbidden, "no candidates: the lookup error is reported")
}
