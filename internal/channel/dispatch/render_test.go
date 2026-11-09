package dispatch

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestReopenedLine(t *testing.T) {
	require.Equal(t, "🔁 Firing again · recurrence #3 · previous analysis reused", reopenedLine(map[string]any{"reopen_count": 3, "analysis": "reused"}))
	require.Equal(t, "🔁 Firing again · recurrence #1 · the fix didn't hold, investigating again", reopenedLine(map[string]any{"reopen_count": 1, "analysis": "fix_failed"}))
	require.Equal(t, "🔁 Firing again · recurrence #2 · investigating", reopenedLine(map[string]any{"reopen_count": 2, "analysis": "new"}))
	require.Equal(t, "🔁 Firing again · recurrence #4", reopenedLine(map[string]any{"reopen_count": 4}), "events from before the payload field")
}
