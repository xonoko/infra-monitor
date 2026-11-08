package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"

	"github.com/xonoko/infra-monitor/internal/domain"
)

// Mute suppresses notifications for matching incidents.
type Mute struct{ ent.Schema }

func (Mute) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Mute) Fields() []ent.Field {
	return []ent.Field{
		field.JSON("matchers", []domain.Matcher{}),
		field.Time("starts_at"),
		field.Time("ends_at"),
		field.Text("reason").Default(""),
		field.Int("created_by_id").Optional().Nillable(),
		field.String("created_by_name").Default(""),
		field.Int("incident_id").Optional().Nillable(),
		field.String("alertmanager_silence_id").Default(""),
		field.String("alertmanager_source").Default(""),
		field.Bool("expired_manually").Default(false),
	}
}

func (Mute) Edges() []ent.Edge {
	return []ent.Edge{edge.From("created_by", User.Type).Ref("mutes").Field("created_by_id").Unique()}
}

func (Mute) Indexes() []ent.Index { return []ent.Index{index.Fields("ends_at")} }
