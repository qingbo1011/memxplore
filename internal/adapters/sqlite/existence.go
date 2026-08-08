package sqlite

import (
	"context"
	"fmt"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// EpisodeExists supports idempotent durable formation retries.
func (s *Store) EpisodeExists(ctx context.Context, id domain.ID) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM episodes WHERE id = ?)", id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check episode: %w", err)
	}
	return exists != 0, nil
}

// WorkingSetExists supports idempotent durable formation retries.
func (s *Store) WorkingSetExists(ctx context.Context, id domain.ID) (bool, error) {
	var exists int
	if err := s.db.QueryRowContext(ctx, "SELECT EXISTS(SELECT 1 FROM working_sets WHERE id = ?)", id).Scan(&exists); err != nil {
		return false, fmt.Errorf("check working set: %w", err)
	}
	return exists != 0, nil
}
