package schema

import (
	"time"

	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/mixin"
)

// TimeMixin adds created_at / updated_at.
type TimeMixin struct{ mixin.Schema }

func (TimeMixin) Fields() []ent.Field {
	return []ent.Field{
		field.Time("created_at").Default(time.Now).Immutable(),
		field.Time("updated_at").Default(time.Now).UpdateDefault(time.Now),
	}
}

// CreatedMixin adds created_at only (append-only tables).
type CreatedMixin struct{ mixin.Schema }

func (CreatedMixin) Fields() []ent.Field {
	return []ent.Field{field.Time("created_at").Default(time.Now).Immutable()}
}
