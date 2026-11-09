package actions

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sort"

	"github.com/santhosh-tekuri/jsonschema/v6"
)

// ErrUnknownType is returned for unregistered or disabled action types.
var ErrUnknownType = errors.New("unknown or disabled action type")

// Info describes a registered action type (for the API and the agent prompt).
type Info struct {
	Type        string          `json:"type"`
	Description string          `json:"description"`
	Risk        string          `json:"risk" enum:"low,medium,high"`
	Schema      json.RawMessage `json:"params_schema"`
	AutoApprove bool            `json:"auto_approve"`
}

type Registry struct {
	execs   map[string]Executor
	schemas map[string]*jsonschema.Schema
	enabled map[string]bool
	auto    map[string]bool
}

// NewRegistry creates a registry. Only types listed in enabled are usable (nil = all registered).
func NewRegistry(enabled, autoApprove []string) *Registry {
	r := &Registry{execs: map[string]Executor{}, schemas: map[string]*jsonschema.Schema{}, auto: map[string]bool{}}
	if enabled != nil {
		r.enabled = map[string]bool{}
		for _, t := range enabled {
			r.enabled[t] = true
		}
	}
	for _, t := range autoApprove {
		r.auto[t] = true
	}
	return r
}

// Register compiles the executor's schema and adds it.
func (r *Registry) Register(e Executor) error {
	doc, err := jsonschema.UnmarshalJSON(bytes.NewReader(e.Schema()))
	if err != nil {
		return fmt.Errorf("%s: schema: %w", e.Type(), err)
	}
	c := jsonschema.NewCompiler()
	url := "mem://actions/" + e.Type() + ".json"
	if err := c.AddResource(url, doc); err != nil {
		return fmt.Errorf("%s: schema: %w", e.Type(), err)
	}
	s, err := c.Compile(url)
	if err != nil {
		return fmt.Errorf("%s: compile schema: %w", e.Type(), err)
	}
	r.execs[e.Type()] = e
	r.schemas[e.Type()] = s
	return nil
}

// Get returns an enabled executor.
func (r *Registry) Get(typ string) (Executor, bool) {
	e, ok := r.execs[typ]
	if !ok || (r.enabled != nil && !r.enabled[typ]) {
		return nil, false
	}
	return e, true
}

// AutoApprove reports whether proposals of this type run without human approval.
func (r *Registry) AutoApprove(typ string) bool { return r.auto[typ] }

// Types lists enabled action types, sorted.
func (r *Registry) Types() []Info {
	var out []Info
	for t, e := range r.execs {
		if _, ok := r.Get(t); !ok {
			continue
		}
		out = append(out, Info{Type: t, Description: e.Description(), Risk: e.Risk(), Schema: e.Schema(), AutoApprove: r.auto[t]})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].Type < out[j].Type })
	return out
}

// Validate checks type, JSON schema and executor policy.
func (r *Registry) Validate(ctx context.Context, typ string, params map[string]any, t Target) error {
	e, ok := r.Get(typ)
	if !ok {
		return fmt.Errorf("%w: %q", ErrUnknownType, typ)
	}
	if params == nil {
		params = map[string]any{}
	}
	// Round-trip through JSON so numbers/types match what the schema validator expects.
	b, err := json.Marshal(params)
	if err != nil {
		return err
	}
	inst, err := jsonschema.UnmarshalJSON(bytes.NewReader(b))
	if err != nil {
		return err
	}
	if err := r.schemas[typ].Validate(inst); err != nil {
		return fmt.Errorf("invalid params for %s: %w", typ, err)
	}
	return e.Validate(ctx, params, t)
}

// Enable adds types to the enabled set (for executors with their own on/off switch).
func (r *Registry) Enable(types ...string) {
	if r.enabled == nil {
		return // everything is enabled
	}
	for _, t := range types {
		r.enabled[t] = true
	}
}
