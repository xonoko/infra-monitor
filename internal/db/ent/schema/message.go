package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Message is one chat message in the incident conversation (any channel).
type Message struct{ ent.Schema }

func (Message) Mixin() []ent.Mixin { return []ent.Mixin{CreatedMixin{}} }

func (Message) Fields() []ent.Field {
	return []ent.Field{
		field.Int("incident_id"),
		field.Enum("role").Values("user", "agent", "system"),
		field.Int("author_id").Optional().Nillable(),
		field.String("author_name").Default(""),
		field.String("channel").Default("ui"), // ui | api | telegram | slack | system (channel type)
		field.String("channel_name").Default(""),
		field.Text("content"),
		field.Ints("action_ids").Default([]int{}),
		field.Int("reply_to_id").Optional().Nillable(),
		field.Enum("status").Values("done", "pending", "failed").Default("done"),
	}
}

func (Message) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("incident", Incident.Type).Ref("messages").Field("incident_id").Unique().Required(),
		edge.From("author", User.Type).Ref("messages").Field("author_id").Unique(),
	}
}

func (Message) Indexes() []ent.Index { return []ent.Index{index.Fields("incident_id", "created_at")} }
