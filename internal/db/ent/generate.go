package ent

//go:generate go run -mod=mod entgo.io/ent/cmd/ent generate --feature sql/upsert,sql/execquery,sql/lock,sql/modifier,sql/versioned-migration,intercept ./schema
