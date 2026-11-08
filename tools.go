//go:build tools

// Package tools pins build-time tool dependencies (ent codegen) in go.mod.
package tools

import (
	_ "entgo.io/ent/cmd/ent"
)
