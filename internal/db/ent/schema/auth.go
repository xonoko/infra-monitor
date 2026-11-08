package schema

import (
	"entgo.io/ent"
	"entgo.io/ent/schema/edge"
	"entgo.io/ent/schema/field"
	"entgo.io/ent/schema/index"
)

// ExternalIdentity links a user to an account in a channel (e.g. a Telegram user id).
type ExternalIdentity struct{ ent.Schema }

func (ExternalIdentity) Mixin() []ent.Mixin { return []ent.Mixin{TimeMixin{}} }

func (ExternalIdentity) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id"),
		field.String("channel_type"),
		field.String("external_id"),
		field.String("external_name").Default(""),
	}
}

func (ExternalIdentity) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("identities").Field("user_id").Unique().Required()}
}

func (ExternalIdentity) Indexes() []ent.Index {
	return []ent.Index{index.Fields("channel_type", "external_id").Unique(), index.Fields("user_id", "channel_type").Unique()}
}

// LinkCode is a short-lived code a user sends to a channel bot to link identities.
type LinkCode struct{ ent.Schema }

func (LinkCode) Mixin() []ent.Mixin { return []ent.Mixin{CreatedMixin{}} }

func (LinkCode) Fields() []ent.Field {
	return []ent.Field{
		field.Int("user_id"),
		field.String("code").Unique(),
		field.Time("expires_at"),
	}
}

func (LinkCode) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("link_codes").Field("user_id").Unique().Required()}
}

// Session is a server-side browser session (stored in Postgres so every replica sees it).
type Session struct{ ent.Schema }

func (Session) Mixin() []ent.Mixin { return []ent.Mixin{CreatedMixin{}} }

func (Session) Fields() []ent.Field {
	return []ent.Field{
		field.String("token_hash").Unique().Sensitive(),
		field.String("csrf_token").Sensitive(),
		field.Int("user_id"),
		field.Time("expires_at"),
		field.Time("last_seen_at"),
		field.String("user_agent").Default(""),
		field.String("ip").Default(""),
		field.String("auth_method").Default("local"),
	}
}

func (Session) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("sessions").Field("user_id").Unique().Required()}
}

func (Session) Indexes() []ent.Index { return []ent.Index{index.Fields("expires_at")} }

// APIToken is a hashed bearer token for automation.
type APIToken struct{ ent.Schema }

func (APIToken) Mixin() []ent.Mixin { return []ent.Mixin{CreatedMixin{}} }

func (APIToken) Fields() []ent.Field {
	return []ent.Field{
		field.String("name"),
		field.String("token_hash").Unique().Sensitive(),
		field.String("prefix"),
		field.Enum("role").Values("viewer", "operator", "admin"),
		field.Int("user_id").Optional().Nillable(),
		field.Time("last_used_at").Optional().Nillable(),
		field.Time("expires_at").Optional().Nillable(),
		field.Time("revoked_at").Optional().Nillable(),
	}
}

func (APIToken) Edges() []ent.Edge {
	return []ent.Edge{edge.From("user", User.Type).Ref("tokens").Field("user_id").Unique()}
}

// OIDCState holds an in-flight OIDC login (state, PKCE verifier, nonce).
type OIDCState struct{ ent.Schema }

func (OIDCState) Mixin() []ent.Mixin { return []ent.Mixin{CreatedMixin{}} }

func (OIDCState) Fields() []ent.Field {
	return []ent.Field{
		field.String("state").Unique(),
		field.String("verifier").Sensitive(),
		field.String("nonce"),
		field.String("redirect_to").Default("/"),
		field.Time("expires_at"),
	}
}
