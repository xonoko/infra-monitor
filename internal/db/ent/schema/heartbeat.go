package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Heartbeat tracks a dead-man's-switch signal per source.
type Heartbeat struct{ ent.Schema }

func (Heartbeat) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Heartbeat) Fields() []ent.Field {
	return []ent.Field{
		field.String("key").Unique(),
		field.Time("last_seen_at").Optional().Nillable(),
		field.Int64("expected_interval_seconds").Default(60),
		field.Enum("state").Values("unknown", "ok", "silent").Default("unknown"),
		field.Int("incident_id").Optional().Nillable(),
		field.Time("last_ping_at").Optional().Nillable(),
		field.String("last_ping_error").Default(""),
	}
}
