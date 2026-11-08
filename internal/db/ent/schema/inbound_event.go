package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// InboundEvent remembers processed inbound channel deliveries (e.g. Telegram update ids)
// so a delivery received twice (poller restart, webhook retry) is handled once.
type InboundEvent struct{ ent.Schema }

func (InboundEvent) Mixin() []ent.Mixin { return []ent.Mixin{CreatedMixin{}} }

func (InboundEvent) Fields() []ent.Field {
	return []ent.Field{
		field.String("channel"),
		field.String("event_id"),
	}
}

func (InboundEvent) Indexes() []ent.Index {
	return []ent.Index{index.Fields("channel", "event_id").Unique(), index.Fields("created_at")}
}
