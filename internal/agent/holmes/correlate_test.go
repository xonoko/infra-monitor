package holmes

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestParseCorrelation(t *testing.T) {
	r, err := parseCorrelation(`{"same_as": 9, "reason": "same demo-api OOM"}`)
	require.NoError(t, err)
	require.Equal(t, 9, r.SameAs)
	require.Equal(t, "same demo-api OOM", r.Reason)

	r, err = parseCorrelation("Looking at it...\n```json\n{\"same_as\": 0, \"reason\": \"different workloads\"}\n```")
	require.NoError(t, err)
	require.Equal(t, 0, r.SameAs)

	_, err = parseCorrelation(`{"reason": "no id"}`)
	require.Error(t, err)
	_, err = parseCorrelation("I think they are related")
	require.Error(t, err)
}
