package domain

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestMatchers(t *testing.T) {
	labels := map[string]string{"alertname": "Watchdog", "severity": "none", "cluster": "manager"}
	for _, tc := range []struct {
		m    string
		want bool
	}{
		{"alertname=Watchdog", true},
		{`alertname="Watchdog"`, true},
		{"alertname!=Watchdog", false},
		{"severity=~none|info", true},
		{"cluster!~prod.*", true},
		{"namespace=", true},
		{"namespace!=", false},
	} {
		m, err := ParseMatcher(tc.m)
		require.NoError(t, err, tc.m)
		require.Equal(t, tc.want, m.Matches(labels), tc.m)
	}
	_, err := ParseMatcher("nonsense")
	require.Error(t, err)
	_, err = ParseMatcher("a=~(")
	require.Error(t, err)
}

func TestRoles(t *testing.T) {
	require.True(t, RoleAdmin.Allows(RoleOperator))
	require.True(t, RoleOperator.Allows(RoleOperator))
	require.False(t, RoleViewer.Allows(RoleOperator))
	require.False(t, RoleNone.Allows(RoleViewer))
	r, err := ParseRole("Admin")
	require.NoError(t, err)
	require.Equal(t, RoleAdmin, r)
}

func TestSeverity(t *testing.T) {
	require.Equal(t, "critical", MaxSeverity("warning", "critical"))
	require.Equal(t, "warning", MaxSeverity("warning", "info"))
}
