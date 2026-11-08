package auth

import (
	"context"
	"errors"
	"fmt"
	"net/url"
	"slices"
	"strings"
	"time"

	"github.com/coreos/go-oidc/v3/oidc"
	"golang.org/x/oauth2"

	"github.com/xonoko/infra-monitor/internal/db"
	"github.com/xonoko/infra-monitor/internal/db/ent"
	"github.com/xonoko/infra-monitor/internal/db/ent/oidcstate"
	"github.com/xonoko/infra-monitor/internal/db/ent/user"
	"github.com/xonoko/infra-monitor/internal/domain"
	"github.com/xonoko/infra-monitor/internal/telemetry"
)

// ErrOIDCRoleDenied is returned when no group grants access and default_role is "none".
var ErrOIDCRoleDenied = errors.New("your account is not allowed to use infra-monitor")

type oidcProvider struct {
	provider *oidc.Provider
	verifier *oidc.IDTokenVerifier
	oauth    oauth2.Config
}

func (s *Service) provider(ctx context.Context) (*oidcProvider, error) {
	s.oidcMu.Lock()
	defer s.oidcMu.Unlock()
	if s.oidc != nil {
		return s.oidc, nil
	}
	o := s.cfg.Auth.OIDC
	if !o.Enabled {
		return nil, errors.New("oidc is disabled")
	}
	pctx := oidc.ClientContext(ctx, telemetry.HTTPClient(15*time.Second))
	p, err := oidc.NewProvider(pctx, o.Issuer)
	if err != nil {
		return nil, fmt.Errorf("oidc discovery %s: %w", o.Issuer, err)
	}
	scopes := o.Scopes
	if !slices.Contains(scopes, oidc.ScopeOpenID) {
		scopes = append([]string{oidc.ScopeOpenID}, scopes...)
	}
	s.oidc = &oidcProvider{
		provider: p,
		verifier: p.Verifier(&oidc.Config{ClientID: o.ClientID}),
		oauth: oauth2.Config{
			ClientID: o.ClientID, ClientSecret: o.ClientSecret, RedirectURL: o.RedirectURL,
			Endpoint: p.Endpoint(), Scopes: scopes,
		},
	}
	return s.oidc, nil
}

// safeRedirect only allows local paths.
func safeRedirect(r string) string {
	if r == "" || !strings.HasPrefix(r, "/") || strings.HasPrefix(r, "//") || strings.HasPrefix(r, "/\\") {
		return "/"
	}
	if u, err := url.Parse(r); err != nil || u.Host != "" || u.Scheme != "" {
		return "/"
	}
	return r
}

// BeginOIDC returns the provider authorization URL (auth code + PKCE + nonce).
func (s *Service) BeginOIDC(ctx context.Context, redirect string) (string, error) {
	p, err := s.provider(ctx)
	if err != nil {
		return "", err
	}
	state, nonce, verifier := randomToken(24), randomToken(24), oauth2.GenerateVerifier()
	if err := s.ent.OIDCState.Create().SetState(state).SetNonce(nonce).SetVerifier(verifier).
		SetRedirectTo(safeRedirect(redirect)).SetExpiresAt(s.now().Add(10 * time.Minute)).Exec(ctx); err != nil {
		return "", err
	}
	return p.oauth.AuthCodeURL(state, oauth2.S256ChallengeOption(verifier), oidc.Nonce(nonce)), nil
}

type oidcClaims struct {
	Subject           string `json:"sub"`
	Email             string `json:"email"`
	EmailVerified     *bool  `json:"email_verified"`
	Name              string `json:"name"`
	PreferredUsername string `json:"preferred_username"`
}

// CompleteOIDC exchanges the code, verifies the ID token, provisions the user and creates a session.
func (s *Service) CompleteOIDC(ctx context.Context, state, code, userAgent, ip string) (string, string, error) {
	p, err := s.provider(ctx)
	if err != nil {
		return "", "", err
	}
	var st *ent.OIDCState
	err = db.WithTx(ctx, s.ent, func(tx *ent.Tx) error {
		st, err = tx.OIDCState.Query().Where(oidcstate.State(state)).ForUpdate().Only(ctx)
		if err != nil {
			return errors.New("unknown or reused login state")
		}
		return tx.OIDCState.DeleteOne(st).Exec(ctx)
	})
	if err != nil {
		return "", "", err
	}
	if !st.ExpiresAt.After(s.now()) {
		return "", "", errors.New("login expired, try again")
	}
	hctx := oidc.ClientContext(ctx, telemetry.HTTPClient(15*time.Second))
	tok, err := p.oauth.Exchange(hctx, code, oauth2.VerifierOption(st.Verifier))
	if err != nil {
		return "", "", fmt.Errorf("oidc code exchange: %w", err)
	}
	rawID, ok := tok.Extra("id_token").(string)
	if !ok {
		return "", "", errors.New("oidc: no id_token in token response")
	}
	idt, err := p.verifier.Verify(hctx, rawID)
	if err != nil {
		return "", "", fmt.Errorf("oidc: invalid id token: %w", err)
	}
	if idt.Nonce != st.Nonce {
		return "", "", errors.New("oidc: nonce mismatch")
	}
	var claims oidcClaims
	if err := idt.Claims(&claims); err != nil {
		return "", "", err
	}
	var all map[string]any
	_ = idt.Claims(&all)
	groups := claimStrings(all[s.cfg.Auth.OIDC.GroupsClaim])
	if claims.Email == "" || len(groups) == 0 {
		// Some providers only put these in userinfo.
		if ui, err := p.provider.UserInfo(hctx, oauth2.StaticTokenSource(tok)); err == nil {
			var uiClaims map[string]any
			_ = ui.Claims(&uiClaims)
			if claims.Email == "" {
				claims.Email = ui.Email
			}
			if len(groups) == 0 {
				groups = claimStrings(uiClaims[s.cfg.Auth.OIDC.GroupsClaim])
			}
			if claims.Name == "" {
				if n, ok := uiClaims["name"].(string); ok {
					claims.Name = n
				}
			}
		}
	}
	if claims.Email == "" {
		return "", "", errors.New("oidc: the provider did not return an email (add the email scope)")
	}
	role := s.mapRole(groups)
	if role == domain.RoleNone {
		return "", "", ErrOIDCRoleDenied
	}
	name := firstNonEmpty(claims.Name, claims.PreferredUsername)
	u, err := s.provisionOIDCUser(ctx, idt.Issuer, idt.Subject, normalizeEmail(claims.Email), name, role)
	if err != nil {
		return "", "", err
	}
	if u.Disabled {
		return "", "", ErrDisabled
	}
	raw, _, err := s.CreateSession(ctx, u, "oidc", userAgent, ip)
	return raw, st.RedirectTo, err
}

func (s *Service) mapRole(groups []string) domain.Role {
	o := s.cfg.Auth.OIDC
	for _, g := range groups {
		if slices.Contains(o.AdminGroups, g) {
			return domain.RoleAdmin
		}
	}
	for _, g := range groups {
		if slices.Contains(o.OperatorGroups, g) {
			return domain.RoleOperator
		}
	}
	if r, err := domain.ParseRole(o.DefaultRole); err == nil {
		return r
	}
	return domain.RoleNone
}

// provisionOIDCUser finds the user by (issuer, subject), then by email, or creates it.
// The role follows the groups claim on every login (unless an admin changed it manually).
func (s *Service) provisionOIDCUser(ctx context.Context, issuer, subject, email, name string, role domain.Role) (*ent.User, error) {
	var out *ent.User
	err := db.WithTx(ctx, s.ent, func(tx *ent.Tx) error {
		u, err := tx.User.Query().Where(user.OidcIssuer(issuer), user.OidcSubject(subject)).ForUpdate().Only(ctx)
		if ent.IsNotFound(err) {
			u, err = tx.User.Query().Where(user.Email(email)).ForUpdate().Only(ctx)
		}
		switch {
		case ent.IsNotFound(err):
			out, err = tx.User.Create().SetEmail(email).SetDisplayName(name).SetRole(user.Role(role)).
				SetOidcIssuer(issuer).SetOidcSubject(subject).SetRoleManagedByOidc(true).Save(ctx)
			return err
		case err != nil:
			return err
		}
		upd := tx.User.UpdateOne(u).SetOidcIssuer(issuer).SetOidcSubject(subject).SetEmail(email)
		if name != "" {
			upd.SetDisplayName(name)
		}
		if u.RoleManagedByOidc || u.PasswordHash == nil {
			upd.SetRole(user.Role(role)).SetRoleManagedByOidc(true)
		} else if role.Level() > domain.Role(u.Role).Level() {
			upd.SetRole(user.Role(role))
		}
		out, err = upd.Save(ctx)
		return err
	})
	return out, err
}

func claimStrings(v any) []string {
	switch t := v.(type) {
	case []any:
		out := make([]string, 0, len(t))
		for _, x := range t {
			if s, ok := x.(string); ok {
				out = append(out, s)
			}
		}
		return out
	case []string:
		return t
	case string:
		if t == "" {
			return nil
		}
		return strings.Split(t, ",")
	}
	return nil
}

func firstNonEmpty(vals ...string) string {
	for _, v := range vals {
		if v != "" {
			return v
		}
	}
	return ""
}
