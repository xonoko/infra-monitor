package schema

import (
	"encoding/json"

	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Signal is a normalized alert coming from a source.
type Signal struct{ ent.Schema }

func (Signal) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Signal) Fields() []ent.Field {
	return []ent.Field{
		field.String("source"),
		field.String("fingerprint"),
		field.Enum("status").Values("firing", "resolved"),
		field.String("name").Default(""), // alertname
		field.String("severity").Default(""),
		field.String("cluster").Default(""),
		field.String("namespace").Default(""),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}),
		field.JSON("annotations", map[string]string{}).Default(map[string]string{}),
		field.Time("starts_at"),
		field.Time("ends_at").Optional().Nillable(),
		field.String("generator_url").Default(""),
		field.Time("last_seen_at"),
		field.Int("fire_count").Default(1),
		field.JSON("raw", json.RawMessage{}).Optional(),
		field.Int("incident_id").Optional().Nillable(),
	}
}

func (Signal) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("incident", Incident.Type).Ref("signals").Field("incident_id").Unique(),
	}
}

func (Signal) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("source", "fingerprint").Unique(),
		index.Fields("incident_id", "status"),
		index.Fields("status", "last_seen_at"),
	}
}
