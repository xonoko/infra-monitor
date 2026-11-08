package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ChannelMessage records one message delivered to an external channel,
// so replies and button presses can be routed back and messages edited.
type ChannelMessage struct{ ent.Schema }

func (ChannelMessage) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ChannelMessage) Fields() []ent.Field {
	return []ent.Field{
		field.Int("thread_id"),
		field.String("channel"),
		field.String("external_chat_id"),
		field.String("external_message_id"),
		field.Enum("kind").Values("card", "entry", "proposal"),
		field.Int("timeline_event_id").Optional().Nillable(),
		field.Int("action_id").Optional().Nillable(),
		field.String("hash").Default(""),
	}
}

func (ChannelMessage) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("thread", ChannelThread.Type).Ref("messages").Field("thread_id").Unique().Required().
			Annotations(entsql.OnDelete(entsql.Cascade)),
	}
}

func (ChannelMessage) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("channel", "external_chat_id", "external_message_id").Unique(),
		index.Fields("action_id"),
	}
}
