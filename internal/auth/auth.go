// Package auth verifies Keycloak access tokens and carries the caller
// (Principal) in the request context.
package auth

import (
	"context"
	"fmt"
	"slices"
	"strings"

	"github.com/coreos/go-oidc/v3/oidc"

	"github.com/ShammiAnand/go-service-template/internal/apperr"
)

// Principal is the authenticated caller.
type Principal struct {
	Subject string   // Keycloak user id ("sub")
	Email   string   // may be empty if the email scope is not granted
	Roles   []string // realm roles
}

// HasRole reports whether p has the realm role.
func (p Principal) HasRole(role string) bool { return slices.Contains(p.Roles, role) }

type principalKey struct{}

// WithPrincipal stores p in ctx.
func WithPrincipal(ctx context.Context, p Principal) context.Context {
	return context.WithValue(ctx, principalKey{}, p)
}

// FromContext returns the caller, if the request was authenticated.
func FromContext(ctx context.Context) (Principal, bool) {
	p, ok := ctx.Value(principalKey{}).(Principal)
	return p, ok
}

// MustFromContext returns the caller of an operation that declares bearer
// security. The auth middleware guarantees a Principal there, so a missing
// one is a wiring bug: it panics, and the recovery middleware returns 500.
func MustFromContext(ctx context.Context) Principal {
	p, ok := FromContext(ctx)
	if !ok {
		panic("auth: no principal in context; operation is missing Security")
	}
	return p
}

// Verifier checks bearer tokens.
type Verifier interface {
	Verify(ctx context.Context, rawToken string) (Principal, error)
}

// KeycloakVerifier validates RS256 access tokens: signature (JWKS),
// issuer, audience and expiry. Signing keys are fetched lazily on the
// first request and refreshed on unknown key ids, so the app starts even
// when Keycloak is still booting.
type KeycloakVerifier struct{ v *oidc.IDTokenVerifier }

// NewKeycloakVerifier builds a verifier. ctx bounds JWKS fetches made by
// background refreshes; pass the process context.
func NewKeycloakVerifier(ctx context.Context, issuerURL, jwksURL, audience string) *KeycloakVerifier {
	keys := oidc.NewRemoteKeySet(ctx, jwksURL)
	return &KeycloakVerifier{v: oidc.NewVerifier(issuerURL, keys, &oidc.Config{
		ClientID:             audience,
		SupportedSigningAlgs: []string{oidc.RS256},
	})}
}

type keycloakClaims struct {
	Email       string `json:"email"`
	RealmAccess struct {
		Roles []string `json:"roles"`
	} `json:"realm_access"`
}

// Verify returns the Principal for a valid token.
func (k *KeycloakVerifier) Verify(ctx context.Context, raw string) (Principal, error) {
	tok, err := k.v.Verify(ctx, raw)
	if err != nil {
		return Principal{}, apperr.Unauthorized("invalid token", err)
	}
	var c keycloakClaims
	if err := tok.Claims(&c); err != nil {
		return Principal{}, apperr.Unauthorized("invalid token claims", err)
	}
	return Principal{Subject: tok.Subject, Email: c.Email, Roles: c.RealmAccess.Roles}, nil
}

// BearerToken extracts the token from an Authorization header value.
func BearerToken(header string) (string, error) {
	scheme, token, ok := strings.Cut(header, " ")
	if !ok || !strings.EqualFold(scheme, "Bearer") || token == "" {
		return "", apperr.Unauthorized("missing bearer token")
	}
	return token, nil
}

// ErrNoRole builds the error for a caller that lacks a role.
func ErrNoRole(role string) error { return apperr.Forbidden(fmt.Sprintf("requires role %q", role)) }
