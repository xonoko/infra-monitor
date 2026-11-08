package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Incident groups correlated signals.
type Incident struct{ ent.Schema }

func (Incident) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Incident) Fields() []ent.Field {
	return []ent.Field{
		field.String("title"),
		field.Enum("status").Values("open", "acknowledged", "resolved", "closed").Default("open"),
		field.String("severity").Default(""),
		field.String("grouping_key"),
		field.String("source").Default(""),
		field.String("cluster").Default(""),
		field.String("namespace").Default(""),
		field.JSON("labels", map[string]string{}).Default(map[string]string{}),
		field.Enum("verdict").Values("actionable", "noise", "unknown").Default("unknown"),
		field.Text("summary").Default(""),
		field.Bool("muted").Default(false),
		field.Int("mute_id").Optional().Nillable(),
		field.Bool("internal").Default(false),
		field.Int("assignee_id").Optional().Nillable(),
		field.Time("opened_at").Default(time.Now),
		field.Time("acknowledged_at").Optional().Nillable(),
		field.Time("resolved_at").Optional().Nillable(),
		field.Time("closed_at").Optional().Nillable(),
		field.Time("last_signal_at").Default(time.Now),
		field.Int("reopen_count").Default(0),
		// Channels this incident was routed to (names from config).
		field.Strings("notify_channels").Default([]string{}),
		field.Time("notified_at").Optional().Nillable(),
		field.Enum("investigation_state").Values("none", "pending", "running", "done", "failed", "skipped").Default("none"),
		field.Time("last_investigated_at").Optional().Nillable(),
		// Opaque conversation history returned by the agent (kept for follow-up chat).
		field.JSON("agent_conversation", json.RawMessage{}).Optional(),
		field.String("trace_id").Default(""),
	}
}

func (Incident) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("signals", Signal.Type),
		edge.To("investigations", Investigation.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("actions", Action.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("messages", Message.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("timeline", TimelineEvent.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("threads", ChannelThread.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.From("assignee", User.Type).Ref("assigned_incidents").Field("assignee_id").Unique(),
	}
}

func (Incident) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("grouping_key", "status"),
		index.Fields("status", "last_signal_at"),
		index.Fields("opened_at"),
	}
}
