package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// Action is a remediation proposed by the agent or a user. It runs only after approval.
type Action struct{ ent.Schema }

func (Action) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (Action) Fields() []ent.Field {
	return []ent.Field{
		field.Int("incident_id"),
		field.String("type"),
		field.JSON("params", map[string]any{}).Default(map[string]any{}),
		field.Text("rationale").Default(""),
		field.Enum("risk").Values("low", "medium", "high").Default("medium"),
		field.Enum("proposed_by").Values("agent", "user", "system"),
		field.Int("proposer_id").Optional().Nillable(),
		field.Int("investigation_id").Optional().Nillable(),
		field.Enum("status").Values("proposed", "approved", "rejected", "running", "succeeded", "failed", "expired").Default("proposed"),
		field.Int("decided_by_id").Optional().Nillable(),
		field.String("decided_by_name").Default(""),
		field.String("decided_via").Default(""),
		field.Time("decided_at").Optional().Nillable(),
		field.Time("started_at").Optional().Nillable(),
		field.Time("finished_at").Optional().Nillable(),
		field.Time("expires_at"),
		field.Text("output").Default(""),
		field.Text("error").Default(""),
		field.String("idempotency_key").Unique(),
		field.String("summary").Default(""), // human readable one-liner, e.g. "rollout restart deploy/foo -n bar"
		// Prepared is the exact proposal shown for approval (target, payload, dry-run, rollback...).
		field.JSON("prepared", map[string]any{}).Optional(),
		// PayloadHash binds an approval to the exact payload and target state.
		field.String("payload_hash").Default(""),
		// RequiresApproval: never auto-approved (generic and gitops actions).
		field.Bool("requires_approval").Default(false),
		// SupersededBy points to the re-prepared proposal when the target changed.
		field.Int("superseded_by").Optional().Nillable(),
	}
}

func (Action) Edges() []ent.Edge {
	return []ent.Edge{
		edge.From("incident", Incident.Type).Ref("actions").Field("incident_id").Unique().Required(),
		edge.From("decided_by", User.Type).Ref("decided_actions").Field("decided_by_id").Unique(),
	}
}

func (Action) Indexes() []ent.Index {
	return []ent.Index{index.Fields("incident_id"), index.Fields("status", "expires_at")}
}
