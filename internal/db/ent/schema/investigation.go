package schema

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Investigation is one AI investigation run on an incident.
type Investigation struct{ ent.Schema }

func (Investigation) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Investigation) Fields() []ent.Field {
	return []ent.Field{
		field.Int("incident_id"),
		field.Enum("status").Values("pending", "running", "succeeded", "failed", "timed_out").Default("pending"),
		field.Enum("trigger").Values("auto", "manual", "action").Default("auto"),
		field.String("agent").Default(""),
		field.String("model").Default(""),
		field.Text("analysis").Default(""),
		field.Text("summary").Default(""),
		field.Text("root_cause").Default(""),
		field.Enum("verdict").Values("actionable", "noise", "unknown").Default("unknown"),
		field.Float("confidence").Default(0),
		field.JSON("tool_calls", json.RawMessage{}).Optional(),
		field.Int("prompt_tokens").Default(0),
		field.Int("completion_tokens").Default(0),
		field.Text("error").Default(""),
		field.Int("requested_by_id").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
		field.String("trace_id").Default(""),
	}
}

func (Investigation) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("incident", Incident.Type).Ref("investigations").Field("incident_id").Unique().Required(),
	}
}

func (Investigation) Indexes() []ent.Index {
	return []ent.Index{index.Fields("incident_id", "created_at"), index.Fields("status", "started_at")}
}
