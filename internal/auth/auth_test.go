package auth_test

import (
	"context"
	"crypto"
	"crypto/rand"
	"crypto/rsa"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"math/big"
	"net/http"
	"net/http/httptest"
	"net/url"
	"testing"
	"time"

	"github.com/stretchr/testify/require"

	"github.com/xonoko/infra-monitor/internal/auth"
	"github.com/xonoko/infra-monitor/internal/config"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/testutil/pgtest"
)

func TestPasswordHashing(t *testing.T) {
	h, err := auth.HashPassword("correct horse battery")
	require.NoError(t, err)
	require.True(t, auth.VerifyPassword(h, "correct horse battery"))
	require.False(t, auth.VerifyPassword(h, "wrong"))
	require.False(t, auth.VerifyPassword("garbage", "x"))
	_, err = auth.HashPassword("short")
	require.Error(t, err)
}

func newSvc(t *testing.T, mutate ...func(*config.Config)) *auth.Service {
	d := pgtest.New(t)
	cfg := &config.Config{Auth: config.AuthConfig{Local: config.LocalAuthConfig{Enabled: true}, SessionTTL: time.Hour}}
	for _, m := range mutate {
		m(cfg)
	}
	return auth.NewService(cfg, d, pgtest.Logger())
}

func TestLocalLoginSessionsTokensAndLinking(t *testing.T) {
	s := newSvc(t)
	ctx := context.Background()
	u, err := s.CreateUser(ctx, "Lucas@Example.com", "Lucas", domain.RoleOperator, "s3cret-password")
	require.NoError(t, err)
	require.Equal(t, "lucas@example.com", u.Email)

	_, _, _, err = s.Login(ctx, "lucas@example.com", "nope", "", "")
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
	_, _, _, err = s.Login(ctx, "ghost@example.com", "nope", "", "")
	require.ErrorIs(t, err, auth.ErrInvalidCredentials)
	tok, _, _, err := s.Login(ctx, "LUCAS@example.com", "s3cret-password", "ua", "1.2.3.4")
	require.NoError(t, err)

	p, err := s.SessionPrincipal(ctx, tok)
	require.NoError(t, err)
	require.Equal(t, domain.RoleOperator, p.Role)
	require.NotEmpty(t, p.CSRFToken)
	require.Equal(t, "Lucas", p.Name)

	req := httptest.NewRequest(http.MethodGet, "/", nil)
	req.AddCookie(&http.Cookie{Name: "im_session", Value: tok})
	p2, err := s.Authenticate(ctx, req, "im_session")
	require.NoError(t, err)
	require.Equal(t, p.UserID, p2.UserID)

	// disabling the user kills sessions
	dis := true
	_, err = s.UpdateUser(ctx, u.ID, auth.UserUpdate{Disabled: &dis})
	require.NoError(t, err)
	_, err = s.SessionPrincipal(ctx, tok)
	require.Error(t, err)
	en := false
	_, err = s.UpdateUser(ctx, u.ID, auth.UserUpdate{Disabled: &en})
	require.NoError(t, err)

	// tokens never exceed their owner's role
	raw, _, err := s.CreateToken(ctx, "ci", domain.RoleAdmin, &u.ID, 0)
	require.NoError(t, err)
	req = httptest.NewRequest(http.MethodGet, "/", nil)
	req.Header.Set("Authorization", "Bearer "+raw)
	tp, err := s.Authenticate(ctx, req, "im_session")
	require.NoError(t, err)
	require.Equal(t, domain.RoleOperator, tp.Role)
	require.Equal(t, "token", tp.Method)
	machine, mt, err := s.CreateToken(ctx, "bot", domain.RoleViewer, nil, time.Hour)
	require.NoError(t, err)
	mp, err := s.TokenPrincipal(ctx, machine)
	require.NoError(t, err)
	require.Equal(t, domain.RoleViewer, mp.Role)
	require.NoError(t, s.RevokeToken(ctx, mt.ID))
	_, err = s.TokenPrincipal(ctx, machine)
	require.Error(t, err)

	// link codes are single-use
	lc, err := s.CreateLinkCode(ctx, u.ID)
	require.NoError(t, err)
	got, err := s.LinkIdentity(ctx, lc.Code, "telegram", "12345", "Lucas G")
	require.NoError(t, err)
	require.Equal(t, u.ID, got.ID)
	_, err = s.LinkIdentity(ctx, lc.Code, "telegram", "999", "x")
	require.ErrorIs(t, err, auth.ErrInvalidCode)
	ru, err := s.ResolveIdentity(ctx, "telegram", "12345")
	require.NoError(t, err)
	require.Equal(t, u.ID, ru.ID)
	none, err := s.ResolveIdentity(ctx, "telegram", "nobody")
	require.NoError(t, err)
	require.Nil(t, none)
}

// ---- fake OIDC provider ----

type fakeIdP struct {
	srv     *httptest.Server
	key     *rsa.PrivateKey
	groups  []string
	nonce   string
	sub     string
	email   string
	gotPKCE string
}

func b64(b []byte) string { return base64.RawURLEncoding.EncodeToString(b) }

func newIdP(t *testing.T) *fakeIdP {
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	require.NoError(t, err)
	f := &fakeIdP{key: key, sub: "user-1", email: "alice@example.com"}
	mux := http.NewServeMux()
	f.srv = httptest.NewServer(mux)
	t.Cleanup(f.srv.Close)
	mux.HandleFunc("/.well-known/openid-configuration", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{
			"issuer": f.srv.URL, "authorization_endpoint": f.srv.URL + "/authorize", "token_endpoint": f.srv.URL + "/token",
			"jwks_uri": f.srv.URL + "/jwks", "userinfo_endpoint": f.srv.URL + "/userinfo",
			"id_token_signing_alg_values_supported": []string{"RS256"},
		})
	})
	mux.HandleFunc("/jwks", func(w http.ResponseWriter, r *http.Request) {
		_ = json.NewEncoder(w).Encode(map[string]any{"keys": []map[string]any{{
			"kty": "RSA", "alg": "RS256", "use": "sig", "kid": "k1",
			"n": b64(key.N.Bytes()), "e": b64(big.NewInt(int64(key.E)).Bytes()),
		}}})
	})
	mux.HandleFunc("/token", func(w http.ResponseWriter, r *http.Request) {
		_ = r.ParseForm()
		f.gotPKCE = r.Form.Get("code_verifier")
		now := time.Now()
		claims := map[string]any{"iss": f.srv.URL, "sub": f.sub, "aud": "infra-monitor", "exp": now.Add(time.Hour).Unix(),
			"iat": now.Unix(), "nonce": f.nonce, "email": f.email, "name": "Alice", "groups": f.groups}
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(map[string]any{"access_token": "at", "token_type": "Bearer", "expires_in": 3600, "id_token": f.sign(t, claims)})
	})
	return f
}

func (f *fakeIdP) sign(t *testing.T, claims map[string]any) string {
	h, _ := json.Marshal(map[string]string{"alg": "RS256", "kid": "k1", "typ": "JWT"})
	p, _ := json.Marshal(claims)
	in := b64(h) + "." + b64(p)
	sum := sha256.Sum256([]byte(in))
	sig, err := rsa.SignPKCS1v15(rand.Reader, f.key, crypto.SHA256, sum[:])
	require.NoError(t, err)
	return in + "." + b64(sig)
}

func TestOIDCLoginMapsGroupsToRoles(t *testing.T) {
	idp := newIdP(t)
	s := newSvc(t, func(c *config.Config) {
		c.Auth.OIDC = config.OIDCConfig{Enabled: true, Issuer: idp.srv.URL, ClientID: "infra-monitor", ClientSecret: "x",
			RedirectURL: "https://im.example.com/api/v1/auth/oidc/callback", Scopes: []string{"openid", "email", "groups"},
			GroupsClaim: "groups", AdminGroups: []string{"infra-admin"}, OperatorGroups: []string{"infra-ops"}, DefaultRole: "none"}
	})
	ctx := context.Background()
	login := func(groups []string) (string, string, error) {
		u, err := s.BeginOIDC(ctx, "/incidents?x=1")
		require.NoError(t, err)
		pu, _ := url.Parse(u)
		require.Equal(t, "S256", pu.Query().Get("code_challenge_method"))
		idp.nonce = pu.Query().Get("nonce")
		idp.groups = groups
		return s.CompleteOIDC(ctx, pu.Query().Get("state"), "code", "ua", "ip")
	}
	_, _, err := login([]string{"randoms"})
	require.ErrorIs(t, err, auth.ErrOIDCRoleDenied)

	tok, redirect, err := login([]string{"infra-admin"})
	require.NoError(t, err)
	require.Equal(t, "/incidents?x=1", redirect)
	require.NotEmpty(t, idp.gotPKCE)
	p, err := s.SessionPrincipal(ctx, tok)
	require.NoError(t, err)
	require.Equal(t, domain.RoleAdmin, p.Role)
	require.Equal(t, "oidc", p.AuthMethod)

	// role follows the groups claim on next login
	tok, _, err = login([]string{"infra-ops"})
	require.NoError(t, err)
	p, err = s.SessionPrincipal(ctx, tok)
	require.NoError(t, err)
	require.Equal(t, domain.RoleOperator, p.Role)
	users, err := s.Users(ctx)
	require.NoError(t, err)
	require.Len(t, users, 1, "same subject, same user")

	// state can't be replayed
	_, _, err = s.CompleteOIDC(ctx, "bogus", "code", "", "")
	require.Error(t, err)
}
