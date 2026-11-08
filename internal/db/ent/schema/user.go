package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/dialect/entsql"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

type User struct{ ent.Schema }

func (User) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (User) Fields() []ent.Field {
	return []ent.Field{
		field.String("email").Unique(),
		field.String("display_name").Default(""),
		field.Enum("role").Values("viewer", "operator", "admin").Default("viewer"),
		field.String("password_hash").Optional().Nillable().Sensitive(),
		field.String("oidc_issuer").Optional().Nillable(),
		field.String("oidc_subject").Optional().Nillable(),
		field.Bool("role_managed_by_oidc").Default(false),
		field.Bool("disabled").Default(false),
		field.Time("last_login_at").Optional().Nillable(),
	}
}

func (User) Edges() []ent.Edge {
	return []ent.Edge{
		edge.To("identities", ExternalIdentity.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("sessions", Session.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("tokens", APIToken.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("link_codes", LinkCode.Type).Annotations(entsql.OnDelete(entsql.Cascade)),
		edge.To("messages", Message.Type),
		edge.To("decided_actions", Action.Type),
		edge.To("assigned_incidents", Incident.Type),
		edge.To("mutes", Mute.Type),
	}
}

func (User) Indexes() []ent.Index {
	return []ent.Index{index.Fields("oidc_issuer", "oidc_subject").Unique()}
}
