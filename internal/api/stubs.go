package api

import (
	"context"
	"net/http"

	"github.com/danielgtaylor/huma/v2"

	"github.com/xonoko/infra-monitor/internal/buildinfo"
	"github.com/xonoko/infra-monitor/internal/domain"
)

const sessionCookie = "im_session"

func (s *Server) authMiddleware(ctx huma.Context, next func(huma.Context)) { next(ctx) }

type VersionOutput struct {
	Body struct {
		Version string `json:"version"`
		Commit  string `json:"commit"`
		Date    string `json:"date"`
	}
}

func (s *Server) registerMeta() {
	huma.Register(s.api, op(http.MethodGet, "/version", "get-version", "Build version", domain.RoleNone, "System"),
		func(ctx context.Context, _ *struct{}) (*VersionOutput, error) {
			out := &VersionOutput{}
			out.Body.Version, out.Body.Commit, out.Body.Date = buildinfo.Version, buildinfo.Commit, buildinfo.Date
			return out, nil
		})
}
func (s *Server) registerAuth()            {}
func (s *Server) registerIncidents()       {}
func (s *Server) registerActions()         {}
func (s *Server) registerMutes()           {}
func (s *Server) registerStatus()          {}
func (s *Server) registerAdmin()           {}
func (s *Server) registerSources()         {}
func (s *Server) registerEvents()          {}
func (s *Server) registerChannelWebhooks() {}
