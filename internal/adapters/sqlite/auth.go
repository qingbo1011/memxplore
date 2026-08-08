package sqlite

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/qingbo1011/memxplore/internal/auth"
	"github.com/qingbo1011/memxplore/internal/domain"
)

var ErrInvalidToken = errors.New("invalid API token")

// CreateAPIToken returns raw token material exactly once and persists only its digest.
func (s *Store) CreateAPIToken(ctx context.Context, spec auth.TokenSpec) (string, error) {
	if err := spec.Validate(); err != nil {
		return "", err
	}
	var secret [32]byte
	if _, err := rand.Read(secret[:]); err != nil {
		return "", fmt.Errorf("generate API token: %w", err)
	}
	raw := "mx_" + hex.EncodeToString(secret[:])
	digest := sha256.Sum256([]byte(raw))
	owners, _ := json.Marshal(spec.PrivateOwners)
	scopes, _ := json.Marshal(spec.Scopes)
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO api_tokens(
            id, token_sha256, principal_id, namespace_id, private_owners_json, scopes_json,
            allow_shared, allow_public, expires_at, created_at
        ) VALUES(?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
		spec.ID, hex.EncodeToString(digest[:]), spec.PrincipalID, spec.Namespace,
		string(owners), string(scopes), spec.AllowShared, spec.AllowPublic,
		nullableTime(spec.ExpiresAt), formatTime(spec.CreatedAt))
	if err != nil {
		return "", fmt.Errorf("store API token: %w", err)
	}
	return raw, nil
}

// AuthenticateToken verifies a digest, expiry, revocation, and typed scopes.
func (s *Store) AuthenticateToken(ctx context.Context, raw string, at time.Time) (auth.Principal, error) {
	if raw == "" || at.IsZero() {
		return auth.Principal{}, ErrInvalidToken
	}
	digest := sha256.Sum256([]byte(raw))
	var principal auth.Principal
	var ownersJSON, scopesJSON string
	var expiresAt, revokedAt sql.NullString
	err := s.db.QueryRowContext(ctx, `
        SELECT id, principal_id, namespace_id, private_owners_json, scopes_json,
               allow_shared, allow_public, expires_at, revoked_at
        FROM api_tokens WHERE token_sha256 = ?`, hex.EncodeToString(digest[:])).Scan(
		&principal.TokenID, &principal.PrincipalID, &principal.Namespace, &ownersJSON, &scopesJSON,
		&principal.AllowShared, &principal.AllowPublic, &expiresAt, &revokedAt)
	if errors.Is(err, sql.ErrNoRows) {
		return auth.Principal{}, ErrInvalidToken
	}
	if err != nil {
		return auth.Principal{}, fmt.Errorf("authenticate API token: %w", err)
	}
	if revokedAt.Valid {
		return auth.Principal{}, ErrInvalidToken
	}
	if expiresAt.Valid {
		expires, err := parseStoredTime(expiresAt.String)
		if err != nil || !expires.After(at) {
			return auth.Principal{}, ErrInvalidToken
		}
	}
	if err := json.Unmarshal([]byte(ownersJSON), &principal.PrivateOwners); err != nil {
		return auth.Principal{}, fmt.Errorf("decode token owners: %w", err)
	}
	if err := json.Unmarshal([]byte(scopesJSON), &principal.Scopes); err != nil {
		return auth.Principal{}, fmt.Errorf("decode token scopes: %w", err)
	}
	if _, err := s.db.ExecContext(ctx, "UPDATE api_tokens SET last_used_at = ? WHERE id = ?", formatTime(at), principal.TokenID); err != nil {
		return auth.Principal{}, fmt.Errorf("update token use: %w", err)
	}
	return principal, nil
}

// RevokeAPIToken invalidates a token by non-secret ID.
func (s *Store) RevokeAPIToken(ctx context.Context, id domain.ID, at time.Time) error {
	if id == "" || at.IsZero() {
		return fmt.Errorf("token id and revocation time are required")
	}
	result, err := s.db.ExecContext(ctx,
		"UPDATE api_tokens SET revoked_at = ? WHERE id = ? AND revoked_at IS NULL", formatTime(at), id)
	if err != nil {
		return fmt.Errorf("revoke API token: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil || count != 1 {
		return fmt.Errorf("active API token not found")
	}
	return nil
}

// APITokenCount reports configured credentials for non-loopback startup validation.
func (s *Store) APITokenCount(ctx context.Context, at time.Time) (int, error) {
	var count int
	if err := s.db.QueryRowContext(ctx, `
        SELECT count(*) FROM api_tokens
        WHERE revoked_at IS NULL AND (expires_at IS NULL OR expires_at > ?)`, formatTime(at)).Scan(&count); err != nil {
		return 0, fmt.Errorf("count API tokens: %w", err)
	}
	return count, nil
}
