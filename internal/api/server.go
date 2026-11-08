// Package api exposes the HTTP API (huma v2 on chi, OpenAPI at /api/v1/openapi.json),
// health and metrics endpoints, the SSE stream and the embedded UI.
package api

import (
	"context"
	"encoding/json"
	"io/fs"
	"log/slog"
	"net/http"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"
	"github.com/danielgtaylor/huma/v2/adapters/humachi"
	"github.com/go-chi/chi/v5"
	"github.com/go-chi/chi/v5/middleware"
	"go.opentelemetry.io/contrib/instrumentation/net/http/otelhttp"
	"go.opentelemetry.io/otel/attribute"
	"go.opentelemetry.io/otel/trace"

	"github.com/xonoko/infra-monitor/internal/buildinfo"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/domain"
)

const prefix = "/api/v1"

// Pinger checks a dependency for /readyz.
type Pinger interface {
	Ping(ctx context.Context) error
}

type Server struct {
	cfg    *config.Config
	log    *slog.Logger
	deps   Deps
	router chi.Router
	api    huma.API
}

// New builds the router. Services in deps may be nil (their routes are then not registered),
// which keeps tests small.
func New(cfg *config.Config, log *slog.Logger, deps Deps) *Server {
	r := chi.NewRouter()
	r.Use(middleware.RequestID)
	if cfg.Server.TrustProxy {
		r.Use(forwardedFor)
	}
	r.Use(middleware.Recoverer)
	r.Use(spanNamer)
	r.Use(accessLog(log))

	hc := huma.DefaultConfig("infra-monitor API", buildinfo.Version)
	hc.Info.Description = "Alert and incident hub: ingestion, AI investigation, actions and a unified timeline across UI and chat channels."
	hc.OpenAPIPath = prefix + "/openapi"
	hc.DocsPath = prefix + "/docs"
	hc.SchemasPath = prefix + "/schemas"
	hc.Servers = []*huma.Server{{URL: cfg.Server.PublicURL}}
	hc.Components.SecuritySchemes = map[string]*huma.SecurityScheme{
		"bearer":  {Type: "http", Scheme: "bearer", Description: "API token (`infra-monitor token create`)"},
		"session": {Type: "apiKey", In: "cookie", Name: sessionCookie, Description: "Browser session; unsafe methods also need the X-CSRF-Token header"},
	}
	api := humachi.New(r, hc)

	s := &Server{cfg: cfg, log: log, deps: deps, router: r, api: api}
	api.UseMiddleware(s.authMiddleware)

	r.Get("/healthz", func(w http.ResponseWriter, _ *http.Request) { _, _ = w.Write([]byte("ok")) })
	r.Get("/readyz", s.readyz)
	if deps.Metrics != nil {
		r.Handle("/metrics", deps.Metrics)
	}
	r.Get("/openapi.json", func(w http.ResponseWriter, req *http.Request) {
		http.Redirect(w, req, prefix+"/openapi.json", http.StatusMovedPermanently)
	})

	s.registerMeta()
	s.registerAuth()
	s.registerIncidents()
	s.registerActions()
	s.registerMutes()
	s.registerStatus()
	s.registerAdmin()
	s.registerSources()
	s.registerEvents()
	s.registerChannelWebhooks()

	ui := deps.UI
	if ui == nil {
		ui = emptyFS{}
	}
	r.NotFound(func(w http.ResponseWriter, req *http.Request) {
		if strings.HasPrefix(req.URL.Path, prefix+"/") {
			writeJSONError(w, http.StatusNotFound, "not found")
			return
		}
		spaHandler(ui).ServeHTTP(w, req)
	})
	return s
}

// Handler returns the root handler wrapped with OpenTelemetry server instrumentation.
func (s *Server) Handler() http.Handler {
	return otelhttp.NewHandler(s.router, "http.server",
		otelhttp.WithFilter(func(r *http.Request) bool {
			return r.URL.Path != "/healthz" && r.URL.Path != "/readyz" && r.URL.Path != "/metrics"
		}),
	)
}

// API exposes the huma API (used to dump the OpenAPI spec).
func (s *Server) API() huma.API { return s.api }

func (s *Server) readyz(w http.ResponseWriter, r *http.Request) {
	if s.deps.DB != nil {
		ctx, cancel := context.WithTimeout(r.Context(), 2*time.Second)
		defer cancel()
		if err := s.deps.DB.Ping(ctx); err != nil {
			http.Error(w, "database unreachable", http.StatusServiceUnavailable)
			return
		}
	}
	_, _ = w.Write([]byte("ready"))
}

// spanNamer renames the server span to "METHOD /route/{pattern}" once chi has routed.
func spanNamer(next http.Handler) http.Handler {
	return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		next.ServeHTTP(w, r)
		span := trace.SpanFromContext(r.Context())
		if rc := chi.RouteContext(r.Context()); rc != nil {
			if p := rc.RoutePattern(); p != "" {
				span.SetName(r.Method + " " + p)
				span.SetAttributes(attribute.String("http.route", p))
			}
		}
	})
}

func accessLog(log *slog.Logger) func(http.Handler) http.Handler {
	return func(next http.Handler) http.Handler {
		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			if r.URL.Path == "/healthz" || r.URL.Path == "/readyz" || r.URL.Path == "/metrics" {
				next.ServeHTTP(w, r)
				return
			}
			start := time.Now()
			ww := middleware.NewWrapResponseWriter(w, r.ProtoMajor)
			next.ServeHTTP(ww, r)
			level := slog.LevelDebug
			if ww.Status() >= 500 {
				level = slog.LevelError
			} else if ww.Status() >= 400 {
				level = slog.LevelInfo
			}
			log.Log(r.Context(), level, "http request", "method", r.Method, "path", r.URL.Path, "status", ww.Status(),
				"bytes", ww.BytesWritten(), "duration_ms", time.Since(start).Milliseconds(), "request_id", middleware.GetReqID(r.Context()))
		})
	}
}

func writeJSONError(w http.ResponseWriter, status int, msg string) {
	w.Header().Set("Content-Type", "application/problem+json")
	w.WriteHeader(status)
	_ = json.NewEncoder(w).Encode(map[string]any{"title": http.StatusText(status), "status": status, "detail": msg})
}

type emptyFS struct{}

func (emptyFS) Open(string) (fs.File, error) { return nil, fs.ErrNotExist }

// op is a small helper to declare an operation with its minimum role.
// role "" means public (no authentication).
func op(method, path, id, summary string, role domain.Role, tags ...string) huma.Operation {
	o := huma.Operation{
		OperationID: id,
		Method:      method,
		Path:        prefix + path,
		Summary:     summary,
		Tags:        tags,
		Metadata:    map[string]any{"role": role},
	}
	if role != domain.RoleNone {
		o.Security = []map[string][]string{{"bearer": {}}, {"session": {}}}
		o.Errors = []int{http.StatusUnauthorized, http.StatusForbidden}
	}
	return o
}
