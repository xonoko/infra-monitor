package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"net/http"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"

	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/incident"
)

const (
	sessionCookie = "im_session"
	csrfHeader    = "X-CSRF-Token"
)

type principalKey struct{}

// principal returns the authenticated caller (set by authMiddleware).
func principal(ctx context.Context) (auth.Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(auth.Principal)
	return p, ok
}

func actor(ctx context.Context) domain.Actor {
	p, _ := principal(ctx)
	return p.Actor()
}

func isUnsafe(method string) bool {
	return method != http.MethodGet && method != http.MethodHead && method != http.MethodOptions
}

// authenticate resolves the principal and enforces CSRF for cookie sessions.
func (s *Server) authenticate(r *http.Request) (auth.Principal, int, error) {
	if s.deps.Auth == nil {
		return auth.Principal{}, http.StatusUnauthorized, errors.New("authentication is not configured")
	}
	p, err := s.deps.Auth.Authenticate(r.Context(), r, sessionCookie)
	if err != nil {
		return auth.Principal{}, http.StatusUnauthorized, errors.New("not authenticated")
	}
	if p.Method == "session" && isUnsafe(r.Method) {
		got := r.Header.Get(csrfHeader)
		if got == "" || subtle.ConstantTimeCompare([]byte(got), []byte(p.CSRFToken)) != 1 {
			return auth.Principal{}, http.StatusForbidden, errors.New("missing or invalid CSRF token")
		}
	}
	return p, 0, nil
}

func (s *Server) authMiddleware(ctx huma.Context, next func(huma.Context)) {
	ctx = huma.WithValue(ctx, humaCtxKey{}, ctx)
	role, _ := ctx.Operation().Metadata["role"].(domain.Role)
	if role == domain.RoleNone {
		next(ctx)
		return
	}
	r, _ := humachi.Unwrap(ctx)
	p, status, err := s.authenticate(r)
	if err != nil {
		_ = huma.WriteErr(s.api, ctx, status, err.Error())
		return
	}
	if !p.Role.Allows(role) {
		_ = huma.WriteErr(s.api, ctx, http.StatusForbidden, "requires role "+string(role))
		return
	}
	next(huma.WithValue(ctx, principalKey{}, p))
}

// apiErr maps service errors to HTTP errors.
func apiErr(err error) error {
	if err == nil {
		return nil
	}
	switch {
	case errors.Is(err, incident.ErrForbidden):
		return huma.Error403Forbidden(err.Error())
	case errors.Is(err, incident.ErrNotFound):
		return huma.Error404NotFound(err.Error())
	case errors.Is(err, incident.ErrConflict):
		return huma.Error409Conflict(err.Error())
	case errors.Is(err, incident.ErrInvalid):
		return huma.Error422UnprocessableEntity(err.Error())
	case errors.Is(err, auth.ErrInvalidCredentials), errors.Is(err, auth.ErrDisabled):
		return huma.Error401Unauthorized(err.Error())
	}
	return huma.Error500InternalServerError("internal error", err)
}
