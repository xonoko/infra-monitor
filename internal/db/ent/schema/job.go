package schema

import (
	"encoding/json"
	"time"

	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Job is a background job in the Postgres-backed queue.
type Job struct{ ent.Schema }

func (Job) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Job) Fields() []ent.Field {
	return []ent.Field{
		field.Int64("id"),
		field.String("kind"),
		field.JSON("payload", json.RawMessage{}),
		field.Enum("status").Values("pending", "running", "succeeded", "failed", "dead").Default("pending"),
		field.Int("attempts").Default(0),
		field.Int("max_attempts").Default(5),
		field.Time("run_at").Default(time.Now),
		field.String("locked_by").Default(""),
		field.Time("locked_until").Optional().Nillable(),
		field.Text("last_error").Default(""),
		// UniqueKey deduplicates pending jobs (partial unique index).
		field.String("unique_key").Optional().Nillable(),
		field.String("traceparent").Default(""),
		field.Time("finished_at").Optional().Nillable(),
	}
}

func (Job) Indexes() []ent.Index {
	return []ent.Index{
		index.Fields("status", "run_at"),
		index.Fields("unique_key").Unique().Annotations(entsql.IndexWhere("unique_key IS NOT NULL AND status = 'pending'")),
		index.Fields("finished_at"),
	}
}
