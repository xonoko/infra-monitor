package channel

import (
	"testing"

	"github.com/stretchr/testify/require"
)

func TestButtonActionRoundTrip(t *testing.T) {
	for _, a := range []ButtonAction{
		{Op: "approve", ActionID: 42},
		{Op: "reject", ActionID: 7},
		{Op: "ack", IncidentID: 3},
		{Op: "mute", IncidentID: 3, Arg: "1h"},
		{Op: "reinvestigate", IncidentID: 99999999},
		{Op: "resolve", IncidentID: 1},
	} {
		s := a.Encode()
		require.LessOrEqual(t, len(s), 64)
		got, err := DecodeButtonAction(s)
		require.NoError(t, err)
		require.Equal(t, a, got)
	}
	_, err := DecodeButtonAction("zz:1")
	require.Error(t, err)
	_, err = DecodeButtonAction("ay:x")
	require.Error(t, err)
}
