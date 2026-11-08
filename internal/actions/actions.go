// Package actions holds the registry of executable remediation actions.
// The agent may only propose registered types; params are validated against each
// executor's JSON schema and policy before anything can run, and nothing runs without
// approval unless the type is configured for auto-approval.
package actions

import (
	"context"
	"encoding/json"
)

// Target is the scope an action runs against, derived from the incident.
type Target struct {
	IncidentID int
	Cluster    string
	Namespace  string
	Labels     map[string]string
	Source     string
}

// Executor implements one action type, e.g. "k8s.rollout_restart".
type Executor interface {
	Type() string
	Description() string
	Risk() string // default risk: low | medium | high
	// Schema is a JSON Schema (draft 2020-12) for params.
	Schema() json.RawMessage
	// Validate applies policy after schema validation (namespace deny lists, bounds...).
	Validate(ctx context.Context, params map[string]any, t Target) error
	// Summary is a one-line human description, e.g. "rollout restart deployment/api -n shop".
	Summary(params map[string]any, t Target) string
	// Execute runs the action and returns human-readable output.
	Execute(ctx context.Context, params map[string]any, t Target) (string, error)
}
