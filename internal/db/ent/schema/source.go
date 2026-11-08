package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
)

// Source tracks the runtime status of a source defined in the config.
type Source struct{ ent.Schema }

func (Source) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Source) Fields() []ent.Field {
	return []ent.Field{
		field.String("name").Unique().NotEmpty(),
		field.String("type"),
		field.Time("last_received_at").Optional().Nillable(),
		field.Time("last_reconciled_at").Optional().Nillable(),
		field.String("last_error").Optional(),
		field.Time("last_error_at").Optional().Nillable(),
		field.Int64("received_total").Default(0),
	}
}
