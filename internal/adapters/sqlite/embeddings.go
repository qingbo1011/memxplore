package sqlite

import (
	"context"
	"crypto/sha256"
	"encoding/binary"
	"encoding/hex"
	"fmt"
	"math"
	"time"

	"github.com/qingbo1011/memxplore/internal/domain"
)

// PutEmbedding stores an exact float32 vector for one immutable memory version.
func (s *Store) PutEmbedding(ctx context.Context, versionID domain.ID, providerID, model, content string, vector []float32, createdAt time.Time) error {
	if versionID == "" || providerID == "" || model == "" || content == "" || len(vector) == 0 || len(vector) > 16384 || createdAt.IsZero() {
		return fmt.Errorf("embedding version, identity, content, vector, and timestamp are required")
	}
	var indexedContent string
	if err := s.db.QueryRowContext(ctx,
		"SELECT text_content FROM memory_fts WHERE memory_version_id = ?", versionID).Scan(&indexedContent); err != nil {
		return fmt.Errorf("read indexed memory content: %w", err)
	}
	if indexedContent != content {
		return fmt.Errorf("embedding content does not match immutable memory version")
	}
	encoded := make([]byte, len(vector)*4)
	var squared float64
	for index, value := range vector {
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return fmt.Errorf("embedding contains a non-finite value at dimension %d", index)
		}
		squared += float64(value) * float64(value)
		binary.LittleEndian.PutUint32(encoded[index*4:], math.Float32bits(value))
	}
	if squared == 0 {
		return fmt.Errorf("embedding vector has zero norm")
	}
	digest := sha256.Sum256([]byte(content))
	_, err := s.db.ExecContext(ctx, `
        INSERT INTO memory_embeddings(
            memory_version_id, provider_id, model, dimensions, vector_blob, content_sha256, created_at
        ) VALUES(?, ?, ?, ?, ?, ?, ?)
        ON CONFLICT(memory_version_id, provider_id, model) DO UPDATE SET
            dimensions = excluded.dimensions,
            vector_blob = excluded.vector_blob,
            content_sha256 = excluded.content_sha256,
            created_at = excluded.created_at`,
		versionID, providerID, model, len(vector), encoded, hex.EncodeToString(digest[:]), formatTime(createdAt))
	if err != nil {
		return fmt.Errorf("put memory embedding: %w", err)
	}
	return nil
}

func decodeVector(encoded []byte, dimensions int) ([]float32, error) {
	if dimensions < 1 || dimensions > 16384 || len(encoded) != dimensions*4 {
		return nil, fmt.Errorf("stored embedding dimensions are invalid")
	}
	vector := make([]float32, dimensions)
	for index := range vector {
		value := math.Float32frombits(binary.LittleEndian.Uint32(encoded[index*4:]))
		if math.IsNaN(float64(value)) || math.IsInf(float64(value), 0) {
			return nil, fmt.Errorf("stored embedding contains a non-finite value")
		}
		vector[index] = value
	}
	return vector, nil
}
