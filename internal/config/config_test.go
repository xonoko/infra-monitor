package config

import (
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/stretchr/testify/require"
)

func write(t *testing.T, body string) string {
	p := filepath.Join(t.TempDir(), "config.yaml")
	require.NoError(t, os.WriteFile(p, []byte(body), 0o600))
	return p
}

func TestLoadDefaultsEnvAndSecretFiles(t *testing.T) {
	dir := t.TempDir()
	tokenFile := filepath.Join(dir, "token")
	require.NoError(t, os.WriteFile(tokenFile, []byte("s3cret\n"), 0o600))
	p := write(t, `
server:
  public_url: https://im.example.com/
sources:
  - name: am
    auth:
      bearer_token_file: `+tokenFile+`
retention:
  resolved_incidents: 90d
`)
	t.Setenv("INFRA_MONITOR_DATABASE_HOST", "db.internal")
	t.Setenv("INFRA_MONITOR_CORRELATION_DEBOUNCE", "10s")
	cfg, err := Load(p)
	require.NoError(t, err)
	require.Equal(t, "https://im.example.com", cfg.Server.PublicURL)
	require.Equal(t, "db.internal", cfg.Database.Host)
	require.Equal(t, 10*time.Second, cfg.Correlation.Debounce)
	require.Equal(t, 90*24*time.Hour, cfg.Retention.ResolvedIncidents)
	require.Equal(t, "s3cret", cfg.Sources[0].Auth.BearerToken)
	require.Equal(t, "alertmanager", cfg.Sources[0].Type)
	require.Equal(t, "cluster", cfg.Sources[0].ClusterLabel)
	require.Equal(t, "require", cfg.Database.SSLMode)
	require.Contains(t, cfg.Database.DatabaseURL(), "sslmode=require")
	require.True(t, cfg.CookieSecure())
}

func TestValidation(t *testing.T) {
	_, err := Load(write(t, `
channels:
  - name: tg
    type: telegram
routing:
  - channels: [nope]
auth:
  local:
    enabled: false
agent:
  enabled: true
`))
	require.Error(t, err)
	msg := err.Error()
	for _, want := range []string{"bot_token", "chat_id", `unknown channel "nope"`, "enable at least one", "agent.url"} {
		require.Contains(t, msg, want)
	}
	_, err = Load(write(t, "sources:\n  - name: a\n  - name: a\n"))
	require.ErrorContains(t, err, "duplicate source")
}
