package holmes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCheck(t *testing.T) {
	c, err := parseCheck(`{"still_needed": false, "reason": "pod already Running with 256Mi"}`)
	require.NoError(t, err)
	require.False(t, c.StillNeeded)
	require.Contains(t, c.Reason, "256Mi")
	c, err = parseCheck("Looked at it.\n```json\n{\"still_needed\": true, \"reason\": \"still OOMKilled\"}\n```")
	require.NoError(t, err)
	require.True(t, c.StillNeeded)
	_, err = parseCheck("I think so")
	require.Error(t, err)
}
