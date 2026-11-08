package channel

import (
	"fmt"
	"strconv"
	"strings"
)

// ButtonAction is what a callback button does. It encodes into at most 64 bytes
// (Telegram's callback_data limit), e.g. "ay:42" (approve action 42) or "im:7:1h" (mute incident 7 for 1h).
type ButtonAction struct {
	Op         string // approve | reject | ack | mute | reinvestigate | resolve
	IncidentID int
	ActionID   int
	Arg        string
}

var opCodes = map[string]string{
	"approve": "ay", "reject": "an", "ack": "ia", "mute": "im", "reinvestigate": "ir", "resolve": "ix",
}

var codeOps = func() map[string]string {
	m := map[string]string{}
	for k, v := range opCodes {
		m[v] = k
	}
	return m
}()

// Encode returns the compact form.
func (a ButtonAction) Encode() string {
	code := opCodes[a.Op]
	id := a.IncidentID
	if a.Op == "approve" || a.Op == "reject" {
		id = a.ActionID
	}
	s := code + ":" + strconv.Itoa(id)
	if a.Arg != "" {
		s += ":" + a.Arg
	}
	if len(s) > 64 {
		s = s[:64]
	}
	return s
}

// DecodeButtonAction parses Encode's output.
func DecodeButtonAction(s string) (ButtonAction, error) {
	parts := strings.SplitN(s, ":", 3)
	if len(parts) < 2 {
		return ButtonAction{}, fmt.Errorf("invalid button data %q", s)
	}
	op, ok := codeOps[parts[0]]
	if !ok {
		return ButtonAction{}, fmt.Errorf("unknown button op %q", parts[0])
	}
	id, err := strconv.Atoi(parts[1])
	if err != nil || id <= 0 {
		return ButtonAction{}, fmt.Errorf("invalid id in %q", s)
	}
	a := ButtonAction{Op: op}
	if op == "approve" || op == "reject" {
		a.ActionID = id
	} else {
		a.IncidentID = id
	}
	if len(parts) == 3 {
		a.Arg = parts[2]
	}
	return a, nil
}
