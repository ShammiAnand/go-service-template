package auth_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/ShammiAnand/go-service-template/internal/apperr"
	"github.com/ShammiAnand/go-service-template/internal/auth"
	"github.com/ShammiAnand/go-service-template/internal/testutil"
)

func TestKeycloakVerifier(t *testing.T) {
	iss := testutil.NewIssuer(t)
	v := iss.Verifier(t.Context())

	t.Run("valid token", func(t *testing.T) {
		tok := iss.Token(t, testutil.Claims{Subject: "u1", Email: "a@example.com", Roles: []string{"user"}})
		p, err := v.Verify(t.Context(), tok)
		require.NoError(t, err)
		assert.Equal(t, auth.Principal{Subject: "u1", Email: "a@example.com", Roles: []string{"user"}}, p)
		assert.True(t, p.HasRole("user"))
		assert.False(t, p.HasRole("admin"))
	})

	rejects := map[string]testutil.Claims{
		"wrong audience": {Subject: "u1", Audience: "other"},
		"wrong issuer":   {Subject: "u1", Issuer: "https://evil.example.com/realms/test"},
		"expired":        {Subject: "u1", Expiry: time.Now().Add(-time.Minute)},
	}
	for name, c := range rejects {
		t.Run(name, func(t *testing.T) {
			_, err := v.Verify(t.Context(), iss.Token(t, c))
			e, ok := apperr.As(err)
			require.True(t, ok, "want apperr, got %v", err)
			assert.Equal(t, apperr.KindUnauthorized, e.Kind)
		})
	}

	t.Run("garbage", func(t *testing.T) {
		_, err := v.Verify(t.Context(), "not.a.jwt")
		require.Error(t, err)
	})

	t.Run("token from another key", func(t *testing.T) {
		other := testutil.NewIssuer(t)
		tok := other.Token(t, testutil.Claims{Subject: "u1", Issuer: iss.URL})
		_, err := v.Verify(t.Context(), tok)
		require.Error(t, err)
	})
}

func TestBearerToken(t *testing.T) {
	tok, err := auth.BearerToken("Bearer abc")
	require.NoError(t, err)
	assert.Equal(t, "abc", tok)

	tok, err = auth.BearerToken("bearer abc")
	require.NoError(t, err)
	assert.Equal(t, "abc", tok)

	for _, h := range []string{"", "Bearer", "Bearer ", "Basic abc", "abc"} {
		_, err := auth.BearerToken(h)
		assert.Error(t, err, h)
	}
}

func TestMustFromContextPanicsWithoutPrincipal(t *testing.T) {
	assert.Panics(t, func() { auth.MustFromContext(context.Background()) })
	ctx := auth.WithPrincipal(context.Background(), auth.Principal{Subject: "x"})
	assert.Equal(t, "x", auth.MustFromContext(ctx).Subject)
}
