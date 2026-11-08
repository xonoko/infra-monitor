package actions

import (
	"context"
	"encoding/json"
	"errors"
	"testing"

	"github.com/stretchr/testify/require"
)

type fakeExec struct{}

func (fakeExec) Type() string        { return "test.echo" }
func (fakeExec) Description() string { return "echo" }
func (fakeExec) Risk() string        { return "low" }
func (fakeExec) Schema() json.RawMessage {
	return json.RawMessage(`{"type":"object","required":["msg"],"additionalProperties":false,"properties":{"msg":{"type":"string","minLength":1},"n":{"type":"integer","minimum":0,"maximum":3}}}`)
}
func (fakeExec) Validate(_ context.Context, p map[string]any, t Target) error {
	if t.Namespace == "kube-system" {
		return errors.New("denied")
	}
	return nil
}
func (fakeExec) Summary(p map[string]any, _ Target) string { return "echo " + p["msg"].(string) }
func (fakeExec) Execute(_ context.Context, p map[string]any, _ Target) (string, error) {
	return p["msg"].(string), nil
}

func TestRegistryValidation(t *testing.T) {
	r := NewRegistry([]string{"test.echo"}, nil)
	require.NoError(t, r.Register(fakeExec{}))
	ctx := context.Background()
	require.NoError(t, r.Validate(ctx, "test.echo", map[string]any{"msg": "hi", "n": 2}, Target{}))
	require.Error(t, r.Validate(ctx, "test.echo", map[string]any{"msg": ""}, Target{}))
	require.Error(t, r.Validate(ctx, "test.echo", map[string]any{"msg": "x", "n": 9}, Target{}))
	require.Error(t, r.Validate(ctx, "test.echo", map[string]any{"msg": "x", "extra": 1}, Target{}))
	require.Error(t, r.Validate(ctx, "test.echo", map[string]any{"msg": "x"}, Target{Namespace: "kube-system"}))
	require.ErrorIs(t, r.Validate(ctx, "nope", nil, Target{}), ErrUnknownType)
	require.Len(t, r.Types(), 1)

	disabled := NewRegistry([]string{}, nil)
	require.NoError(t, disabled.Register(fakeExec{}))
	_, ok := disabled.Get("test.echo")
	require.False(t, ok)
}
