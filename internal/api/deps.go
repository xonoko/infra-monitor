package api

import (
	"io/fs"
	"net/http"
)

// Deps are the services the API exposes. Nil services disable their routes.
type Deps struct {
	DB      Pinger
	Metrics http.Handler
	UI      fs.FS
}

// FullDepsForSpec returns placeholder deps so every route is registered when dumping the spec.
func FullDepsForSpec() Deps { return Deps{} }
