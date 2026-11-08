package redact

import (
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestText(t *testing.T) {
	cases := map[string]string{
		"password=hunter2 user=bob":                         "password=<redacted> user=bob",
		`{"api_key": "abc123", "name": "x"}`:                `{"api_key": <redacted>, "name": "x"}`,
		"DB_PASSWORD: s3cr3t":                               "DB_PASSWORD: <redacted>",
		"Authorization: Bearer abcdefghijklmnop":            "Authorization: Bearer <redacted>",
		"postgres://app:pa55@db:5432/x":                     "postgres://app:<redacted>@db:5432/x",
		"token ghp_abcdefghijklmnopqrstuvwxyz0123456789 ok": "token <redacted> ok",
		"memory: 256Mi":                                     "memory: 256Mi",
	}
	for in, want := range cases {
		require.Equal(t, want, Text(in), in)
	}
	pem := "-----BEGIN RSA PRIVATE KEY-----\nMIIabc\n-----END RSA PRIVATE KEY-----"
	require.Equal(t, mask, Text(pem))
	require.NotContains(t, Text("x eyJhbGciOiJIUzI1NiJ9.eyJzdWIiOiIxMjM0NTY3ODkwIn0.abcdefghijklmnop y"), "eyJ")
}

func TestSecretManifest(t *testing.T) {
	in := `apiVersion: v1
kind: Secret
metadata:
  name: db
data:
  password: cGFzcw==
  username: YWRtaW4=
type: Opaque`
	out := Text(in)
	require.NotContains(t, out, "cGFzcw==")
	require.NotContains(t, out, "YWRtaW4=")
	require.Contains(t, out, "type: Opaque")
	require.Contains(t, out, "name: db")
	diff := "-  password: b2xk\n+  password: bmV3"
	require.True(t, strings.Contains(Text("kind: Secret\ndata:\n"+diff), mask))
}

func TestMap(t *testing.T) {
	m := Map(map[string]any{"patch": map[string]any{"env": []any{map[string]any{"name": "X", "value": "token=abc"}}}}).(map[string]any)
	v := m["patch"].(map[string]any)["env"].([]any)[0].(map[string]any)["value"]
	require.Equal(t, "token=<redacted>", v)
}
