package sqlite

import (
	"context"
	"errors"
	"strings"
	"testing"
	"time"

	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/domain"
)

func TestAPITokensAreHashedScopedExpiringAndRevocable(t *testing.T) {
	ctx := context.Background()
	store := testStore(t)
	now := time.Date(2026, 8, 8, 10, 0, 0, 0, time.UTC)
	expires := now.Add(time.Hour)
	spec := auth.TokenSpec{
		ID: "token-test", PrincipalID: "principal-a", Namespace: "ns-test",
		PrivateOwners: []domain.ID{"owner-a"}, Scopes: []auth.Scope{auth.ScopeMemoryRead, auth.ScopeMemoryWrite},
		AllowShared: true, ExpiresAt: &expires, CreatedAt: now,
	}
	raw, err := store.CreateAPIToken(ctx, spec)
	if err != nil || !strings.HasPrefix(raw, "mx_") {
		t.Fatalf("raw=%q err=%v", raw, err)
	}
	var stored string
	if err := store.db.QueryRow("SELECT token_sha256 FROM api_tokens WHERE id = ?", spec.ID).Scan(&stored); err != nil {
		t.Fatal(err)
	}
	if strings.Contains(stored, raw) || len(stored) != 64 {
		t.Fatalf("token was not digest-only: %q", stored)
	}
	principal, err := store.AuthenticateToken(ctx, raw, now.Add(time.Minute))
	if err != nil || principal.Namespace != "ns-test" || !principal.HasScope(auth.ScopeMemoryRead) || principal.HasScope(auth.ScopeMemoryPurge) {
		t.Fatalf("principal=%+v err=%v", principal, err)
	}
	if _, err := store.AuthenticateToken(ctx, raw, expires); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("expired token err=%v", err)
	}
	if err := store.RevokeAPIToken(ctx, spec.ID, now.Add(30*time.Minute)); err != nil {
		t.Fatal(err)
	}
	if _, err := store.AuthenticateToken(ctx, raw, now.Add(31*time.Minute)); !errors.Is(err, ErrInvalidToken) {
		t.Fatalf("revoked token err=%v", err)
	}
}

func TestAdminScopeImpliesCapabilities(t *testing.T) {
	principal := auth.Principal{Scopes: []auth.Scope{auth.ScopeAdmin}}
	if !principal.HasScope(auth.ScopeMemoryPurge) || !principal.HasScope(auth.ScopeMemoryWrite) {
		t.Fatal("admin did not imply protocol scopes")
	}
}
