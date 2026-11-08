package api

import (
	"context"
	"crypto/subtle"
	"errors"
	"io"
	"net/http"
	"net/url"
	"os"
	"strings"
	"time"

	"github.com/danielgtaylor/huma/v2"

	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/buildinfo"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/heartbeat"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/incident"
)

// ---- meta ----

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

// ---- mutes ----

type ListMutesInput struct {
	Active bool `query:"active"`
}

type CreateMuteInput struct {
	Body struct {
		Matchers             []domain.Matcher `json:"matchers" minItems:"1"`
		StartsAt             *time.Time       `json:"starts_at,omitempty"`
		EndsAt               *time.Time       `json:"ends_at,omitempty"`
		Duration             string           `json:"duration,omitempty" example:"2h"`
		Reason               string           `json:"reason"`
		MirrorToAlertmanager bool             `json:"mirror_to_alertmanager,omitempty"`
		Source               string           `json:"source,omitempty" doc:"Source whose Alertmanager gets the silence (default: first source)"`
	}
}

func (s *Server) registerMutes() {
	svc := s.deps.Incidents
	if svc == nil {
		return
	}
	tag := "Mutes"
	huma.Register(s.api, op(http.MethodGet, "/mutes", "list-mutes", "List mutes", domain.RoleViewer, tag),
		func(ctx context.Context, in *ListMutesInput) (*ItemsOutput[Mute], error) {
			ms, err := svc.Mutes(ctx, in.Active)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(ms, func(m *ent.Mute) Mute { return toMute(svc, m) })), nil
		})
	huma.Register(s.api, op(http.MethodPost, "/mutes", "create-mute", "Create a mute", domain.RoleOperator, tag),
		func(ctx context.Context, in *CreateMuteInput) (*MuteOutput, error) {
			b := in.Body
			now := svc.Now()
			start := now
			if b.StartsAt != nil {
				start = *b.StartsAt
			}
			var end time.Time
			switch {
			case b.EndsAt != nil:
				end = *b.EndsAt
			case b.Duration != "":
				d, err := parseDuration(b.Duration)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity("invalid duration")
				}
				end = start.Add(d)
			default:
				return nil, huma.Error422UnprocessableEntity("ends_at or duration is required")
			}
			mirror := ""
			if b.MirrorToAlertmanager {
				mirror = b.Source
				if mirror == "" && len(s.cfg.Sources) > 0 {
					mirror = s.cfg.Sources[0].Name
				}
			}
			m, err := svc.CreateMute(ctx, actor(ctx), incident.CreateMuteInput{Matchers: b.Matchers, StartsAt: start, EndsAt: end, Reason: b.Reason, MirrorTo: mirror})
			if err != nil {
				return nil, apiErr(err)
			}
			return &MuteOutput{Body: toMute(svc, m)}, nil
		})
	huma.Register(s.api, op(http.MethodDelete, "/mutes/{id}", "expire-mute", "Expire a mute now", domain.RoleOperator, tag),
		func(ctx context.Context, in *IDInput) (*MuteOutput, error) {
			m, err := svc.ExpireMute(ctx, actor(ctx), in.ID)
			if err != nil {
				return nil, apiErr(err)
			}
			return &MuteOutput{Body: toMute(svc, m)}, nil
		})
}

// ---- auth & profile ----

type AuthConfigOutput struct {
	Body struct {
		LocalEnabled bool   `json:"local_enabled"`
		OIDCEnabled  bool   `json:"oidc_enabled"`
		OIDCName     string `json:"oidc_name"`
	}
}

type LoginInput struct {
	UserAgent string `header:"User-Agent"`
	Body      struct {
		Email    string `json:"email"`
		Password string `json:"password"`
	}
}

type Identity struct {
	ChannelType  string `json:"channel_type"`
	ExternalName string `json:"external_name"`
}

type Me struct {
	ID               int        `json:"id"`
	Email            string     `json:"email"`
	DisplayName      string     `json:"display_name"`
	Role             string     `json:"role" enum:"viewer,operator,admin"`
	AuthMethod       string     `json:"auth_method"`
	CSRFToken        string     `json:"csrf_token"`
	Identities       []Identity `json:"identities"`
	TraceURLTemplate string     `json:"trace_url_template"`
}

type MeOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
	Body      Me
}

type LogoutOutput struct {
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type RedirectOutput struct {
	Status    int
	Location  string      `header:"Location"`
	SetCookie http.Cookie `header:"Set-Cookie"`
}

type OIDCLoginInput struct {
	Redirect string `query:"redirect"`
}

type OIDCCallbackInput struct {
	State     string `query:"state"`
	Code      string `query:"code"`
	Error     string `query:"error"`
	ErrorDesc string `query:"error_description"`
	UserAgent string `header:"User-Agent"`
}

type LinkCodeOutput struct {
	Body struct {
		Code         string    `json:"code"`
		ExpiresAt    time.Time `json:"expires_at"`
		Instructions string    `json:"instructions"`
	}
}

type UnlinkInput struct {
	ChannelType string `path:"channel_type"`
}

func (s *Server) sessionCookie(tok string, expires time.Time) http.Cookie {
	return http.Cookie{Name: sessionCookie, Value: tok, Path: "/", HttpOnly: true, Secure: s.cfg.CookieSecure(), //nolint:gosec // Secure is on for https public_url (or auth.cookie_secure)
		SameSite: http.SameSiteLaxMode, Expires: expires}
}

func (s *Server) me(ctx context.Context, p auth.Principal) (Me, error) {
	m := Me{ID: p.UserID, Email: p.Email, DisplayName: p.Name, Role: string(p.Role), AuthMethod: p.AuthMethod,
		CSRFToken: p.CSRFToken, Identities: []Identity{}, TraceURLTemplate: s.cfg.Telemetry.Tracing.UIURLTemplate}
	if p.UserID != 0 {
		ids, err := s.deps.Auth.Identities(ctx, p.UserID)
		if err != nil {
			return m, err
		}
		for _, i := range ids {
			m.Identities = append(m.Identities, Identity{ChannelType: i.ChannelType, ExternalName: i.ExternalName})
		}
	}
	return m, nil
}

func clientIP(ctx huma.Context) string {
	addr := ctx.RemoteAddr()
	if i := strings.LastIndexByte(addr, ':'); i > 0 {
		return addr[:i]
	}
	return addr
}

func (s *Server) registerAuth() {
	a := s.deps.Auth
	if a == nil {
		return
	}
	tag := "Auth"
	huma.Register(s.api, op(http.MethodGet, "/auth/config", "auth-config", "Enabled login methods", domain.RoleNone, tag),
		func(ctx context.Context, _ *struct{}) (*AuthConfigOutput, error) {
			out := &AuthConfigOutput{}
			out.Body.LocalEnabled, out.Body.OIDCEnabled, out.Body.OIDCName = s.cfg.Auth.Local.Enabled, s.cfg.Auth.OIDC.Enabled, s.cfg.Auth.OIDC.Name
			return out, nil
		})

	huma.Register(s.api, op(http.MethodPost, "/auth/login", "login", "Log in with email and password", domain.RoleNone, tag),
		func(ctx context.Context, in *LoginInput) (*MeOutput, error) {
			ip := ""
			if hc, ok := ctx.Value(humaCtxKey{}).(huma.Context); ok {
				ip = clientIP(hc)
			}
			tok, sess, u, err := a.Login(ctx, in.Body.Email, in.Body.Password, in.UserAgent, ip)
			if err != nil {
				if errors.Is(err, auth.ErrInvalidCredentials) || errors.Is(err, auth.ErrDisabled) {
					return nil, huma.Error401Unauthorized(err.Error())
				}
				return nil, huma.Error400BadRequest(err.Error())
			}
			p := auth.Principal{UserID: u.ID, Email: u.Email, Name: auth.DisplayName(u), Role: domain.Role(u.Role),
				Method: "session", AuthMethod: "local", SessionID: sess.ID, CSRFToken: sess.CsrfToken}
			m, err := s.me(ctx, p)
			if err != nil {
				return nil, apiErr(err)
			}
			return &MeOutput{SetCookie: s.sessionCookie(tok, sess.ExpiresAt), Body: m}, nil
		})

	huma.Register(s.api, op(http.MethodPost, "/auth/logout", "logout", "Log out", domain.RoleViewer, tag),
		func(ctx context.Context, _ *struct{}) (*LogoutOutput, error) {
			if p, ok := principal(ctx); ok && p.SessionID != 0 {
				_ = a.Logout(ctx, p.SessionID)
			}
			c := s.sessionCookie("", time.Unix(0, 0)) //nolint:gosec // clearing cookie, same attributes as sessionCookie
			c.MaxAge = -1
			return &LogoutOutput{SetCookie: c}, nil
		})

	huma.Register(s.api, op(http.MethodGet, "/auth/oidc/login", "oidc-login", "Start OIDC login (redirects)", domain.RoleNone, tag),
		func(ctx context.Context, in *OIDCLoginInput) (*RedirectOutput, error) {
			u, err := a.BeginOIDC(ctx, in.Redirect)
			if err != nil {
				s.log.ErrorContext(ctx, "oidc login failed", "error", err)
				return &RedirectOutput{Status: http.StatusFound, Location: "/login?error=" + url.QueryEscape("SSO is unavailable: "+err.Error())}, nil
			}
			return &RedirectOutput{Status: http.StatusFound, Location: u}, nil
		})

	huma.Register(s.api, op(http.MethodGet, "/auth/oidc/callback", "oidc-callback", "OIDC redirect URI", domain.RoleNone, tag),
		func(ctx context.Context, in *OIDCCallbackInput) (*RedirectOutput, error) {
			fail := func(msg string) (*RedirectOutput, error) {
				return &RedirectOutput{Status: http.StatusFound, Location: "/login?error=" + url.QueryEscape(msg)}, nil
			}
			if in.Error != "" {
				return fail(in.Error + ": " + in.ErrorDesc)
			}
			ip := ""
			if hc, ok := ctx.Value(humaCtxKey{}).(huma.Context); ok {
				ip = clientIP(hc)
			}
			tok, redirect, err := a.CompleteOIDC(ctx, in.State, in.Code, in.UserAgent, ip)
			if err != nil {
				s.log.WarnContext(ctx, "oidc callback failed", "error", err)
				return fail(err.Error())
			}
			return &RedirectOutput{Status: http.StatusFound, Location: redirect,
				SetCookie: s.sessionCookie(tok, time.Now().Add(s.cfg.Auth.SessionTTL))}, nil
		})

	huma.Register(s.api, op(http.MethodGet, "/me", "get-me", "Current user", domain.RoleViewer, "Profile"),
		func(ctx context.Context, _ *struct{}) (*struct{ Body Me }, error) {
			p, _ := principal(ctx)
			m, err := s.me(ctx, p)
			if err != nil {
				return nil, apiErr(err)
			}
			return &struct{ Body Me }{Body: m}, nil
		})

	huma.Register(s.api, op(http.MethodPost, "/me/link-code", "create-link-code", "Code to link a chat account (/link CODE)", domain.RoleViewer, "Profile"),
		func(ctx context.Context, _ *struct{}) (*LinkCodeOutput, error) {
			p, _ := principal(ctx)
			if p.UserID == 0 {
				return nil, huma.Error400BadRequest("API tokens without a user can't link chat accounts")
			}
			lc, err := a.CreateLinkCode(ctx, p.UserID)
			if err != nil {
				return nil, apiErr(err)
			}
			out := &LinkCodeOutput{}
			out.Body.Code, out.Body.ExpiresAt = lc.Code, lc.ExpiresAt
			out.Body.Instructions = "Send /link " + lc.Code + " to the infra-monitor bot (private chat or the alerts group) within 10 minutes."
			return out, nil
		})

	huma.Register(s.api, op(http.MethodDelete, "/me/identities/{channel_type}", "unlink-identity", "Unlink a chat account", domain.RoleViewer, "Profile"),
		func(ctx context.Context, in *UnlinkInput) (*struct{}, error) {
			p, _ := principal(ctx)
			return nil, apiErr(a.UnlinkIdentity(ctx, p.UserID, in.ChannelType))
		})
}

type humaCtxKey struct{}

// ---- admin ----

type CreateUserInput struct {
	Body struct {
		Email       string `json:"email" format:"email"`
		DisplayName string `json:"display_name,omitempty"`
		Role        string `json:"role" enum:"viewer,operator,admin"`
		Password    string `json:"password,omitempty" doc:"Leave empty for SSO-only users"`
	}
}

type PatchUserInput struct {
	ID   int `path:"id"`
	Body struct {
		Role        *string `json:"role,omitempty" enum:"viewer,operator,admin"`
		Disabled    *bool   `json:"disabled,omitempty"`
		DisplayName *string `json:"display_name,omitempty"`
		Password    *string `json:"password,omitempty"`
	}
}

type UserOutput struct{ Body User }

type CreateTokenInput struct {
	Body struct {
		Name      string `json:"name"`
		Role      string `json:"role" enum:"viewer,operator,admin"`
		ExpiresIn string `json:"expires_in,omitempty" example:"720h"`
	}
}

type CreatedToken struct {
	APIToken
	Token string `json:"token" doc:"Shown once"`
}

func (s *Server) registerAdmin() {
	a := s.deps.Auth
	if a == nil {
		return
	}
	tag := "Admin"
	huma.Register(s.api, op(http.MethodGet, "/users", "list-users", "List users", domain.RoleAdmin, tag),
		func(ctx context.Context, _ *struct{}) (*ItemsOutput[User], error) {
			us, err := a.Users(ctx)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(us, toUser)), nil
		})
	huma.Register(s.api, op(http.MethodPost, "/users", "create-user", "Create a user", domain.RoleAdmin, tag),
		func(ctx context.Context, in *CreateUserInput) (*UserOutput, error) {
			role, err := domain.ParseRole(in.Body.Role)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			u, err := a.CreateUser(ctx, in.Body.Email, in.Body.DisplayName, role, in.Body.Password)
			if ent.IsConstraintError(err) {
				return nil, huma.Error409Conflict("a user with this email exists")
			}
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return &UserOutput{Body: toUser(u)}, nil
		})
	huma.Register(s.api, op(http.MethodPatch, "/users/{id}", "update-user", "Update a user", domain.RoleAdmin, tag),
		func(ctx context.Context, in *PatchUserInput) (*UserOutput, error) {
			p, _ := principal(ctx)
			upd := auth.UserUpdate{Disabled: in.Body.Disabled, DisplayName: in.Body.DisplayName, Password: in.Body.Password}
			if in.Body.Role != nil {
				r, err := domain.ParseRole(*in.Body.Role)
				if err != nil {
					return nil, huma.Error422UnprocessableEntity(err.Error())
				}
				if in.ID == p.UserID && r != domain.RoleAdmin {
					return nil, huma.Error422UnprocessableEntity("you can't remove your own admin role")
				}
				upd.Role = &r
			}
			if in.ID == p.UserID && in.Body.Disabled != nil && *in.Body.Disabled {
				return nil, huma.Error422UnprocessableEntity("you can't disable yourself")
			}
			u, err := a.UpdateUser(ctx, in.ID, upd)
			if ent.IsNotFound(err) {
				return nil, huma.Error404NotFound("user not found")
			}
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return &UserOutput{Body: toUser(u)}, nil
		})
	huma.Register(s.api, op(http.MethodGet, "/tokens", "list-tokens", "List API tokens", domain.RoleAdmin, tag),
		func(ctx context.Context, _ *struct{}) (*ItemsOutput[APIToken], error) {
			ts, err := a.Tokens(ctx)
			if err != nil {
				return nil, apiErr(err)
			}
			return items(mapSlice(ts, toToken)), nil
		})
	huma.Register(s.api, op(http.MethodPost, "/tokens", "create-token", "Create an API token", domain.RoleAdmin, tag),
		func(ctx context.Context, in *CreateTokenInput) (*struct{ Body CreatedToken }, error) {
			role, err := domain.ParseRole(in.Body.Role)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			var ttl time.Duration
			if in.Body.ExpiresIn != "" {
				if ttl, err = parseDuration(in.Body.ExpiresIn); err != nil {
					return nil, huma.Error422UnprocessableEntity("invalid expires_in")
				}
			}
			raw, t, err := a.CreateToken(ctx, in.Body.Name, role, nil, ttl)
			if err != nil {
				return nil, huma.Error422UnprocessableEntity(err.Error())
			}
			return &struct{ Body CreatedToken }{Body: CreatedToken{APIToken: toToken(t), Token: raw}}, nil
		})
	huma.Register(s.api, op(http.MethodDelete, "/tokens/{id}", "revoke-token", "Revoke an API token", domain.RoleAdmin, tag),
		func(ctx context.Context, in *IDInput) (*struct{}, error) {
			if err := a.RevokeToken(ctx, in.ID); err != nil {
				if ent.IsNotFound(err) {
					return nil, huma.Error404NotFound("token not found")
				}
				return nil, apiErr(err)
			}
			return nil, nil
		})
}

// ---- status ----

type SourceStatus struct {
	Name             string     `json:"name"`
	Type             string     `json:"type"`
	AlertmanagerURL  string     `json:"alertmanager_url"`
	LastReceivedAt   *time.Time `json:"last_received_at,omitempty"`
	LastReconciledAt *time.Time `json:"last_reconciled_at,omitempty"`
	LastError        string     `json:"last_error"`
	LastErrorAt      *time.Time `json:"last_error_at,omitempty"`
	ReceivedTotal    int64      `json:"received_total"`
}

type ChannelStatus struct {
	Name         string          `json:"name"`
	Type         string          `json:"type"`
	Mode         string          `json:"mode"`
	Receiving    bool            `json:"receiving" doc:"This replica runs the receiver (lease holder for polling)"`
	Leader       bool            `json:"leader" doc:"Same as receiving (kept for the UI)"`
	Connected    bool            `json:"connected" doc:"Last delivery succeeded"`
	Capabilities map[string]bool `json:"capabilities"`
	LastError    string          `json:"last_error"`
	LastErrorAt  *time.Time      `json:"last_error_at,omitempty"`
	LastSentAt   *time.Time      `json:"last_sent_at,omitempty"`
}

type HeartbeatStatus struct {
	Key                     string     `json:"key"`
	State                   string     `json:"state" enum:"unknown,ok,silent"`
	LastSeenAt              *time.Time `json:"last_seen_at,omitempty"`
	ExpectedIntervalSeconds int64      `json:"expected_interval_seconds"`
	SilentAfterSeconds      int64      `json:"silent_after_seconds"`
	LastPingAt              *time.Time `json:"last_ping_at,omitempty"`
	LastPingError           string     `json:"last_ping_error"`
	PingConfigured          bool       `json:"ping_configured"`
	IncidentID              *int       `json:"incident_id,omitempty"`
}

type AgentStatus struct {
	Enabled       bool   `json:"enabled"`
	MaxConcurrent int    `json:"max_concurrent"`
	MaxPerHour    int    `json:"max_per_hour"`
	Type          string `json:"type"`
	Model         string `json:"model"`
	URLHost       string `json:"url_host"`
}

type StatusOutput struct {
	Body struct {
		Version    string            `json:"version"`
		Replica    string            `json:"replica"`
		Sources    []SourceStatus    `json:"sources"`
		Channels   []ChannelStatus   `json:"channels"`
		Heartbeats []HeartbeatStatus `json:"heartbeats"`
		Agent      AgentStatus       `json:"agent"`
		Leases     []string          `json:"leases"`
	}
}

func (s *Server) registerStatus() {
	if s.deps.Incidents == nil {
		return
	}
	huma.Register(s.api, op(http.MethodGet, "/status", "get-status", "Sources, channels, heartbeats and agent status", domain.RoleViewer, "System"),
		func(ctx context.Context, _ *struct{}) (*StatusOutput, error) {
			out := &StatusOutput{}
			b := &out.Body
			b.Version = buildinfo.Version
			b.Replica, _ = os.Hostname()
			b.Leases = []string{}
			if s.deps.Status != nil {
				b.Replica = s.deps.Status.Replica()
				b.Leases = s.deps.Status.HeldLeases()
			}
			client := s.deps.Incidents.Ent()
			rows, err := client.Source.Query().All(ctx)
			if err != nil {
				return nil, apiErr(err)
			}
			byName := map[string]*ent.Source{}
			for _, r := range rows {
				byName[r.Name] = r
			}
			b.Sources = []SourceStatus{}
			for _, sc := range s.cfg.Sources {
				st := SourceStatus{Name: sc.Name, Type: sc.Type, AlertmanagerURL: sc.AlertmanagerURL}
				if r := byName[sc.Name]; r != nil {
					st.LastReceivedAt, st.LastReconciledAt, st.LastError, st.LastErrorAt, st.ReceivedTotal = r.LastReceivedAt, r.LastReconciledAt, r.LastError, r.LastErrorAt, r.ReceivedTotal
				}
				b.Sources = append(b.Sources, st)
			}
			b.Channels = []ChannelStatus{}
			if s.deps.Dispatcher != nil {
				modes := map[string]string{}
				for _, c := range s.cfg.Channels {
					modes[c.Name] = c.Mode
				}
				for _, st := range s.deps.Dispatcher.Statuses() {
					b.Channels = append(b.Channels, ChannelStatus{Name: st.Name, Type: st.Type, Mode: modes[st.Name], Receiving: st.Receiving, Leader: st.Receiving,
						Connected:    st.LastErrorAt == nil || (st.LastSentAt != nil && st.LastSentAt.After(*st.LastErrorAt)),
						Capabilities: st.Capabilities,
						LastError:    st.LastError, LastErrorAt: st.LastErrorAt, LastSentAt: st.LastSentAt})
				}
			}
			hbs, err := client.Heartbeat.Query().Order(ent.Asc(heartbeat.FieldKey)).All(ctx)
			if err != nil {
				return nil, apiErr(err)
			}
			b.Heartbeats = []HeartbeatStatus{}
			for _, h := range hbs {
				b.Heartbeats = append(b.Heartbeats, HeartbeatStatus{Key: h.Key, State: string(h.State), LastSeenAt: h.LastSeenAt,
					ExpectedIntervalSeconds: h.ExpectedIntervalSeconds, SilentAfterSeconds: int64(s.cfg.Heartbeat.SilentAfter.Seconds()),
					LastPingAt: h.LastPingAt, LastPingError: h.LastPingError, PingConfigured: s.cfg.Heartbeat.PingURL != "", IncidentID: h.IncidentID})
			}
			ag := s.cfg.Agent
			b.Agent = AgentStatus{Enabled: ag.Enabled, Type: ag.Type, Model: ag.Model, MaxConcurrent: ag.MaxConcurrent, MaxPerHour: ag.MaxPerHour}
			if u, err := url.Parse(ag.URL); err == nil {
				b.Agent.URLHost = u.Host
			}
			return out, nil
		})
}

// ---- source webhooks ----

type WebhookInput struct {
	Name          string `path:"name"`
	Authorization string `header:"Authorization"`
	RawBody       []byte
}

type WebhookOutput struct {
	Status int
	Body   struct {
		Received   int `json:"received"`
		Queued     int `json:"queued"`
		Heartbeats int `json:"heartbeats"`
	}
}

func checkSourceAuth(header string, sa sourceAuth) bool {
	if sa.bearer == "" && sa.basicUser == "" {
		return true
	}
	if sa.bearer != "" {
		if tok, ok := strings.CutPrefix(header, "Bearer "); ok && subtle.ConstantTimeCompare([]byte(tok), []byte(sa.bearer)) == 1 {
			return true
		}
	}
	if sa.basicUser != "" {
		r := &http.Request{Header: http.Header{"Authorization": []string{header}}}
		if u, p, ok := r.BasicAuth(); ok &&
			subtle.ConstantTimeCompare([]byte(u), []byte(sa.basicUser)) == 1 &&
			subtle.ConstantTimeCompare([]byte(p), []byte(sa.basicPass)) == 1 {
			return true
		}
	}
	return false
}

type sourceAuth struct{ bearer, basicUser, basicPass string }

func (s *Server) registerSources() {
	svc := s.deps.Incidents
	if svc == nil || s.deps.Parsers == nil {
		return
	}
	for _, sc := range s.cfg.Sources {
		if sc.Auth.BearerToken == "" && sc.Auth.BasicUsername == "" {
			s.log.Warn("source webhook has no authentication configured", "source", sc.Name)
		}
	}
	o := op(http.MethodPost, "/sources/{name}/webhook", "source-webhook", "Receive alerts from a source (Alertmanager webhook v4)", domain.RoleNone, "Sources")
	o.DefaultStatus = http.StatusAccepted
	o.MaxBodyBytes = 10 << 20
	o.Errors = []int{http.StatusUnauthorized, http.StatusNotFound, http.StatusBadRequest}
	huma.Register(s.api, o, func(ctx context.Context, in *WebhookInput) (*WebhookOutput, error) {
		sc, ok := s.cfg.Source(in.Name)
		if !ok {
			return nil, huma.Error404NotFound("unknown source")
		}
		if !checkSourceAuth(in.Authorization, sourceAuth{sc.Auth.BearerToken, sc.Auth.BasicUsername, sc.Auth.BasicPassword}) {
			return nil, huma.Error401Unauthorized("invalid source credentials")
		}
		parse := s.deps.Parsers[sc.Type]
		if parse == nil {
			return nil, huma.Error400BadRequest("no parser for source type " + sc.Type)
		}
		sigs, err := parse(in.RawBody, sc.Name)
		if err != nil {
			return nil, huma.Error400BadRequest("invalid payload: " + err.Error())
		}
		res, err := svc.IngestSignals(ctx, sc.Name, sigs)
		if err != nil {
			return nil, apiErr(err)
		}
		out := &WebhookOutput{Status: http.StatusAccepted}
		out.Body.Received, out.Body.Queued, out.Body.Heartbeats = res.Received, res.Queued, res.Heartbeats
		return out, nil
	})
}

// registerChannelWebhooks mounts inbound webhooks of chat channels (e.g. Telegram webhook mode).
func (s *Server) registerChannelWebhooks() {
	if s.deps.Dispatcher == nil {
		return
	}
	s.router.Post(prefix+"/channels/{name}/webhook", func(w http.ResponseWriter, r *http.Request) {
		name := chiURLParam(r, "name")
		h := s.deps.Dispatcher.WebhookHandler(name)
		if h == nil {
			writeJSONError(w, http.StatusNotFound, "no webhook for this channel")
			return
		}
		r.Body = http.MaxBytesReader(w, r.Body, 1<<20)
		h.ServeHTTP(w, r)
	})
}

var _ = io.EOF
