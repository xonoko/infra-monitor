package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// TimelineEvent is the unified, append-only history of an incident.
// Every service mutation writes one in the same transaction.
type TimelineEvent struct{ ent.Schema }

func (TimelineEvent) Mixin() []ent.Mixin { return []ent.Mixin{CreatedMixin{}} }

func (TimelineEvent) Fields() []ent.Field {
	return []ent.Field{
		field.Int("incident_id"),
		field.String("type"),
		field.Enum("actor_type").Values("user", "agent", "system"),
		field.Int("actor_id").Optional().Nillable(),
		field.String("actor_name").Default(""),
		field.String("channel").Default(""),      // channel type of origin: ui|api|telegram|slack|system
		field.String("channel_name").Default(""), // channel instance of origin (for echo suppression)
		field.JSON("payload", map[string]any{}).Default(map[string]any{}),
		field.String("trace_id").Default(""),
	}
}

func (TimelineEvent) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("incident", Incident.Type).Ref("timeline").Field("incident_id").Unique().Required(),
	}
}

func (TimelineEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("incident_id", "id")}
}
