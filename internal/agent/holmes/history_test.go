package holmes

import (
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

func TestSanitizeHistoryDropsThinking(t *testing.T) {
	in := json.RawMessage(`[
		{"role":"system","content":"sys"},
		{"role":"assistant","content":[{"type":"thinking","thinking":"hmm","signature":"abc"},{"type":"text","text":"answer"}],
		 "thinking_blocks":[{"type":"thinking","signature":"abc"}],"reasoning_content":"hmm"},
		{"role":"tool","tool_call_id":"t1","content":"pods: 1/1"}
	]`)
	var out []map[string]any
	require.NoError(t, json.Unmarshal(sanitizeHistory(in), &out))
	require.Len(t, out, 3)
	a := out[1]
	require.NotContains(t, a, "thinking_blocks")
	require.NotContains(t, a, "reasoning_content")
	parts := a["content"].([]any)
	require.Len(t, parts, 1)
	require.Equal(t, "text", parts[0].(map[string]any)["type"])
	require.Equal(t, "pods: 1/1", out[2]["content"])
	// invalid JSON is passed through
	require.Equal(t, "x", string(sanitizeHistory(json.RawMessage("x"))))
}

func TestHistoryRejected(t *testing.T) {
	require.True(t, historyRejected(errors.New("messages.1.content.0: Invalid `signature` in `thinking` block")))
	require.False(t, historyRejected(errors.New("HTTP 502 bad gateway")))
}
