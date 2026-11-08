package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ChannelThread maps an incident to its conversation in an external channel.
type ChannelThread struct{ ent.Schema }

func (ChannelThread) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ChannelThread) Fields() []ent.Field {
	return []ent.Field{
		field.String("channel"), // channel instance name
		field.Int("incident_id"),
		field.String("external_chat_id"),
		field.String("external_thread_id").Default(""), // root message id / thread_ts
		field.String("card_message_id").Default(""),
		field.String("card_hash").Default(""),
		// Cursor: last timeline event id delivered to this thread.
		field.Int("last_event_id").Default(0),
	}
}

func (ChannelThread) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("incident", Incident.Type).Ref("threads").Field("incident_id").Unique().Required(),
		edge.To("messages", ChannelMessage.Type),
	}
}

func (ChannelThread) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("channel", "incident_id").Unique(),
		index.Fields("channel", "external_chat_id", "external_thread_id"),
	}
}
