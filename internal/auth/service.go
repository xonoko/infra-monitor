package auth

import (
	"context"
	"crypto/rand"
	"errors"
	"fmt"
	"log/slog"
	"math/big"
	"net/http"
	"strings"
	"sync"
	"time"

	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/apitoken"
	"github.com/xonoko/infra-monitor/internal/db/ent/externalidentity"
	"github.com/xonoko/infra-monitor/internal/db/ent/linkcode"
	"github.com/xonoko/infra-monitor/internal/db/ent/oidcstate"
	"github.com/xonoko/infra-monitor/internal/db/ent/session"
	"github.com/xonoko/infra-monitor/internal/db/ent/user"
	"github.com/xonoko/infra-monitor/internal/domain"
)

var (
	ErrInvalidCredentials = errors.New("invalid email or password")
	ErrUnauthenticated    = errors.New("not authenticated")
	ErrDisabled           = errors.New("user disabled")
	ErrInvalidCode        = errors.New("invalid or expired link code")
)

// TokenPrefix marks infra-monitor API tokens.
const TokenPrefix = "imt_"

// Principal is an authenticated caller.
type Principal struct {
	UserID     int // 0 for tokens without a user
	Email      string
	Name       string
	Role       domain.Role
	Method     string // session | token
	AuthMethod string // local | oidc (sessions)
	SessionID  int
	CSRFToken  string
	TokenID    int
}

// Actor converts the principal into a service actor.
func (p Principal) Actor() domain.Actor {
	ch := domain.OriginUI
	if p.Method == "token" {
		ch = domain.OriginAPI
	}
	return domain.Actor{Type: domain.ActorUser, UserID: p.UserID, Name: p.Name, Role: p.Role, Channel: ch}
}

type Service struct {
	cfg *config.Config
	ent *ent.Client
	log *slog.Logger
	now func() time.Time

	oidcMu sync.Mutex
	oidc   *oidcProvider

	touchMu sync.Mutex
	touched map[int]time.Time
}

func NewService(cfg *config.Config, d *db.DB, log *slog.Logger) *Service {
	return &Service{cfg: cfg, ent: d.Ent, log: log, now: time.Now, touched: map[int]time.Time{}}
}

func displayName(u *ent.User) string {
	if u.DisplayName != "" {
		return u.DisplayName
	}
	if i := strings.IndexByte(u.Email, '@'); i > 0 {
		return u.Email[:i]
	}
	return u.Email
}

func normalizeEmail(e string) string { return strings.ToLower(strings.TrimSpace(e)) }

// ---- users ----

// CreateUser creates a local user. password may be empty (OIDC-only user).
func (s *Service) CreateUser(ctx context.Context, email, name string, role domain.Role, password string) (*ent.User, error) {
	email = normalizeEmail(email)
	if !strings.Contains(email, "@") {
		return nil, fmt.Errorf("invalid email %q", email)
	}
	c := s.ent.User.Create().SetEmail(email).SetDisplayName(name).SetRole(user.Role(role))
	if password != "" {
		h, err := HashPassword(password)
		if err != nil {
			return nil, err
		}
		c.SetPasswordHash(h)
	}
	return c.Save(ctx)
}

// SetPassword sets a user's local password.
func (s *Service) SetPassword(ctx context.Context, email, password string) error {
	h, err := HashPassword(password)
	if err != nil {
		return err
	}
	n, err := s.ent.User.Update().Where(user.Email(normalizeEmail(email))).SetPasswordHash(h).Save(ctx)
	if err != nil {
		return err
	}
	if n == 0 {
		return fmt.Errorf("user %s not found", email)
	}
	return nil
}

// UpdateUser changes role/disabled/name/password (admin).
type UserUpdate struct {
	Role        *domain.Role
	Disabled    *bool
	DisplayName *string
	Password    *string
}

func (s *Service) UpdateUser(ctx context.Context, id int, u UserUpdate) (*ent.User, error) {
	upd := s.ent.User.UpdateOneID(id)
	if u.Role != nil {
		upd.SetRole(user.Role(*u.Role)).SetRoleManagedByOidc(false)
	}
	if u.Disabled != nil {
		upd.SetDisabled(*u.Disabled)
	}
	if u.DisplayName != nil {
		upd.SetDisplayName(*u.DisplayName)
	}
	if u.Password != nil {
		h, err := HashPassword(*u.Password)
		if err != nil {
			return nil, err
		}
		upd.SetPasswordHash(h)
	}
	out, err := upd.Save(ctx)
	if err != nil {
		return nil, err
	}
	if u.Disabled != nil && *u.Disabled {
		_, _ = s.ent.Session.Delete().Where(session.UserID(id)).Exec(ctx)
	}
	return out, nil
}

func (s *Service) Users(ctx context.Context) ([]*ent.User, error) {
	return s.ent.User.Query().Order(ent.Asc(user.FieldEmail)).WithIdentities().All(ctx)
}

func (s *Service) User(ctx context.Context, id int) (*ent.User, error) {
	return s.ent.User.Query().Where(user.ID(id)).WithIdentities().Only(ctx)
}

// ---- local login & sessions ----

// Login verifies credentials and creates a session. Returns the raw session token.
func (s *Service) Login(ctx context.Context, email, password, userAgent, ip string) (string, *ent.Session, *ent.User, error) {
	if !s.cfg.Auth.Local.Enabled {
		return "", nil, nil, errors.New("local login is disabled")
	}
	u, err := s.ent.User.Query().Where(user.Email(normalizeEmail(email))).Only(ctx)
	if err != nil {
		VerifyPassword(dummyHash, password)
		return "", nil, nil, ErrInvalidCredentials
	}
	if u.PasswordHash == nil || !VerifyPassword(*u.PasswordHash, password) {
		return "", nil, nil, ErrInvalidCredentials
	}
	if u.Disabled {
		return "", nil, nil, ErrDisabled
	}
	tok, sess, err := s.CreateSession(ctx, u, "local", userAgent, ip)
	return tok, sess, u, err
}

// CreateSession creates a server-side session for u.
func (s *Service) CreateSession(ctx context.Context, u *ent.User, method, userAgent, ip string) (string, *ent.Session, error) {
	tok := randomToken(32)
	now := s.now()
	sess, err := s.ent.Session.Create().
		SetTokenHash(hashToken(tok)).SetCsrfToken(randomToken(24)).SetUserID(u.ID).
		SetExpiresAt(now.Add(s.cfg.Auth.SessionTTL)).SetLastSeenAt(now).
		SetUserAgent(truncate(userAgent, 300)).SetIP(ip).SetAuthMethod(method).Save(ctx)
	if err != nil {
		return "", nil, err
	}
	_ = s.ent.User.UpdateOne(u).SetLastLoginAt(now).Exec(ctx)
	return tok, sess, nil
}

// Logout deletes the session.
func (s *Service) Logout(ctx context.Context, sessionID int) error {
	return s.ent.Session.DeleteOneID(sessionID).Exec(ctx)
}

// SessionPrincipal resolves a session token.
func (s *Service) SessionPrincipal(ctx context.Context, tok string) (Principal, error) {
	if tok == "" {
		return Principal{}, ErrUnauthenticated
	}
	sess, err := s.ent.Session.Query().Where(session.TokenHash(hashToken(tok))).WithUser().Only(ctx)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	now := s.now()
	if !sess.ExpiresAt.After(now) || sess.Edges.User == nil {
		return Principal{}, ErrUnauthenticated
	}
	u := sess.Edges.User
	if u.Disabled {
		return Principal{}, ErrDisabled
	}
	s.touchSession(ctx, sess.ID, now)
	return Principal{
		UserID: u.ID, Email: u.Email, Name: displayName(u), Role: domain.Role(u.Role),
		Method: "session", AuthMethod: sess.AuthMethod, SessionID: sess.ID, CSRFToken: sess.CsrfToken,
	}, nil
}

// touchSession updates last_seen_at at most once a minute per session (sliding expiry).
func (s *Service) touchSession(ctx context.Context, id int, now time.Time) {
	s.touchMu.Lock()
	last := s.touched[id]
	if now.Sub(last) < time.Minute {
		s.touchMu.Unlock()
		return
	}
	s.touched[id] = now
	s.touchMu.Unlock()
	_ = s.ent.Session.UpdateOneID(id).SetLastSeenAt(now).SetExpiresAt(now.Add(s.cfg.Auth.SessionTTL)).Exec(context.WithoutCancel(ctx))
}

// Authenticate resolves the caller from a Bearer token or the session cookie.
func (s *Service) Authenticate(ctx context.Context, r *http.Request, cookieName string) (Principal, error) {
	if h := r.Header.Get("Authorization"); h != "" {
		tok, ok := strings.CutPrefix(h, "Bearer ")
		if !ok {
			return Principal{}, ErrUnauthenticated
		}
		return s.TokenPrincipal(ctx, strings.TrimSpace(tok))
	}
	c, err := r.Cookie(cookieName)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	return s.SessionPrincipal(ctx, c.Value)
}

// ---- API tokens ----

// CreateToken creates an API token and returns the raw value (shown once).
func (s *Service) CreateToken(ctx context.Context, name string, role domain.Role, userID *int, ttl time.Duration) (string, *ent.APIToken, error) {
	if name == "" {
		return "", nil, errors.New("token name is required")
	}
	raw := TokenPrefix + randomToken(30)
	c := s.ent.APIToken.Create().SetName(name).SetTokenHash(hashToken(raw)).SetPrefix(raw[:len(TokenPrefix)+6]).
		SetRole(apitoken.Role(role)).SetNillableUserID(userID)
	if ttl > 0 {
		c.SetExpiresAt(s.now().Add(ttl))
	}
	t, err := c.Save(ctx)
	return raw, t, err
}

func (s *Service) TokenPrincipal(ctx context.Context, raw string) (Principal, error) {
	if !strings.HasPrefix(raw, TokenPrefix) {
		return Principal{}, ErrUnauthenticated
	}
	t, err := s.ent.APIToken.Query().Where(apitoken.TokenHash(hashToken(raw))).WithUser().Only(ctx)
	if err != nil {
		return Principal{}, ErrUnauthenticated
	}
	now := s.now()
	if t.RevokedAt != nil || (t.ExpiresAt != nil && !t.ExpiresAt.After(now)) {
		return Principal{}, ErrUnauthenticated
	}
	p := Principal{Name: "token:" + t.Name, Role: domain.Role(t.Role), Method: "token", TokenID: t.ID}
	if u := t.Edges.User; u != nil {
		if u.Disabled {
			return Principal{}, ErrDisabled
		}
		p.UserID, p.Email, p.Name = u.ID, u.Email, displayName(u)+" (token "+t.Name+")"
		if domain.Role(u.Role).Level() < p.Role.Level() {
			p.Role = domain.Role(u.Role) // a token never exceeds its owner's role
		}
	}
	if t.LastUsedAt == nil || now.Sub(*t.LastUsedAt) > time.Minute {
		_ = s.ent.APIToken.UpdateOneID(t.ID).SetLastUsedAt(now).Exec(context.WithoutCancel(ctx))
	}
	return p, nil
}

func (s *Service) Tokens(ctx context.Context) ([]*ent.APIToken, error) {
	return s.ent.APIToken.Query().Order(ent.Desc(apitoken.FieldID)).All(ctx)
}

func (s *Service) RevokeToken(ctx context.Context, id int) error {
	return s.ent.APIToken.UpdateOneID(id).SetRevokedAt(s.now()).Exec(ctx)
}

// ---- chat identity linking ----

const linkAlphabet = "ABCDEFGHJKLMNPQRSTUVWXYZ23456789"

// CreateLinkCode creates a short-lived code the user sends to a bot with /link CODE.
func (s *Service) CreateLinkCode(ctx context.Context, userID int) (*ent.LinkCode, error) {
	b := make([]byte, 8)
	for i := range b {
		n, err := rand.Int(rand.Reader, big.NewInt(int64(len(linkAlphabet))))
		if err != nil {
			return nil, err
		}
		b[i] = linkAlphabet[n.Int64()]
	}
	_, _ = s.ent.LinkCode.Delete().Where(linkcode.UserID(userID)).Exec(ctx)
	return s.ent.LinkCode.Create().SetUserID(userID).SetCode(string(b)).SetExpiresAt(s.now().Add(10 * time.Minute)).Save(ctx)
}

// LinkIdentity consumes a link code and links the external account to its user.
func (s *Service) LinkIdentity(ctx context.Context, code, channelType, externalID, externalName string) (*ent.User, error) {
	code = strings.ToUpper(strings.TrimSpace(code))
	var u *ent.User
	err := db.WithTx(ctx, s.ent, func(tx *ent.Tx) error {
		lc, err := tx.LinkCode.Query().Where(linkcode.Code(code)).WithUser().ForUpdate().Only(ctx)
		if err != nil || !lc.ExpiresAt.After(s.now()) {
			return ErrInvalidCode
		}
		u = lc.Edges.User
		if err := tx.LinkCode.DeleteOne(lc).Exec(ctx); err != nil {
			return err
		}
		// one external account per user and channel type; re-linking moves it
		if _, err := tx.ExternalIdentity.Delete().Where(externalidentity.Or(
			externalidentity.And(externalidentity.ChannelType(channelType), externalidentity.ExternalID(externalID)),
			externalidentity.And(externalidentity.ChannelType(channelType), externalidentity.UserID(u.ID)),
		)).Exec(ctx); err != nil {
			return err
		}
		return tx.ExternalIdentity.Create().SetUserID(u.ID).SetChannelType(channelType).SetExternalID(externalID).SetExternalName(externalName).Exec(ctx)
	})
	return u, err
}

// ResolveIdentity maps an external account to a user (nil when unlinked).
func (s *Service) ResolveIdentity(ctx context.Context, channelType, externalID string) (*ent.User, error) {
	id, err := s.ent.ExternalIdentity.Query().Where(externalidentity.ChannelType(channelType), externalidentity.ExternalID(externalID)).WithUser().Only(ctx)
	if ent.IsNotFound(err) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	return id.Edges.User, nil
}

// UnlinkIdentity removes a user's identity for a channel type.
func (s *Service) UnlinkIdentity(ctx context.Context, userID int, channelType string) error {
	_, err := s.ent.ExternalIdentity.Delete().Where(externalidentity.UserID(userID), externalidentity.ChannelType(channelType)).Exec(ctx)
	return err
}

// Identities lists a user's linked identities.
func (s *Service) Identities(ctx context.Context, userID int) ([]*ent.ExternalIdentity, error) {
	return s.ent.ExternalIdentity.Query().Where(externalidentity.UserID(userID)).All(ctx)
}

// ActorFor builds a service actor for a user acting through a channel.
func ActorFor(u *ent.User, channelType, channelName string) domain.Actor {
	return domain.Actor{Type: domain.ActorUser, UserID: u.ID, Name: displayName(u), Role: domain.Role(u.Role), Channel: channelType, ChannelName: channelName}
}

// DisplayName exposes the user's display name.
func DisplayName(u *ent.User) string { return displayName(u) }

// Cleanup deletes expired sessions, OIDC states and link codes.
func (s *Service) Cleanup(ctx context.Context) error {
	now := s.now()
	if _, err := s.ent.Session.Delete().Where(session.ExpiresAtLT(now)).Exec(ctx); err != nil {
		return err
	}
	if _, err := s.ent.OIDCState.Delete().Where(oidcstate.ExpiresAtLT(now)).Exec(ctx); err != nil {
		return err
	}
	_, err := s.ent.LinkCode.Delete().Where(linkcode.ExpiresAtLT(now)).Exec(ctx)
	return err
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	return s[:n]
}
