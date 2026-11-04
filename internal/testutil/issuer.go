// Package testutil holds test helpers. Import it from _test.go files only.
package testutil

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"
	"time"

	"github.com/go-jose/go-jose/v4"
	"github.com/go-jose/go-jose/v4/jwt"

	"github.com/ShammiAnand/go-service-template/internal/auth"
)

// Audience is the audience tokens from Issuer carry by default.
const Audience = "api"

// Issuer is a fake Keycloak realm: it serves a JWKS and signs tokens, so
// tests exercise the real verifier without running Keycloak.
type Issuer struct {
	URL    string
	key    *rsa.PrivateKey
	signer jose.Signer
}

// NewIssuer starts a JWKS server that stops when t ends.
func NewIssuer(t testing.TB) *Issuer {
	t.Helper()
	key, err := rsa.GenerateKey(rand.Reader, 2048)
	if err != nil {
		t.Fatal(err)
	}
	const kid = "test-key"
	jwks := jose.JSONWebKeySet{Keys: []jose.JSONWebKey{{Key: &key.PublicKey, KeyID: kid, Algorithm: "RS256", Use: "sig"}}}
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(jwks)
	}))
	t.Cleanup(srv.Close)

	signer, err := jose.NewSigner(
		jose.SigningKey{Algorithm: jose.RS256, Key: jose.JSONWebKey{Key: key, KeyID: kid}},
		(&jose.SignerOptions{}).WithType("JWT"),
	)
	if err != nil {
		t.Fatal(err)
	}
	return &Issuer{URL: srv.URL + "/realms/test", key: key, signer: signer}
}

// JWKSURL is where the verifier fetches keys.
func (i *Issuer) JWKSURL() string { return i.URL + "/protocol/openid-connect/certs" }

// Verifier returns a real KeycloakVerifier pointed at this issuer.
func (i *Issuer) Verifier(ctx context.Context) *auth.KeycloakVerifier {
	return auth.NewKeycloakVerifier(ctx, i.URL, i.JWKSURL(), Audience)
}

// Claims describe a token. Zero fields get sensible defaults.
type Claims struct {
	Subject  string
	Email    string
	Roles    []string
	Audience string
	Issuer   string
	Expiry   time.Time
}

// Token signs a token for c.
func (i *Issuer) Token(t testing.TB, c Claims) string {
	t.Helper()
	if c.Audience == "" {
		c.Audience = Audience
	}
	if c.Issuer == "" {
		c.Issuer = i.URL
	}
	if c.Expiry.IsZero() {
		c.Expiry = time.Now().Add(5 * time.Minute)
	}
	claims := map[string]any{
		"iss":          c.Issuer,
		"sub":          c.Subject,
		"aud":          c.Audience,
		"exp":          c.Expiry.Unix(),
		"iat":          time.Now().Unix(),
		"email":        c.Email,
		"realm_access": map[string]any{"roles": c.Roles},
	}
	tok, err := jwt.Signed(i.signer).Claims(claims).Serialize()
	if err != nil {
		t.Fatal(err)
	}
	return tok
}
