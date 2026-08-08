CREATE TABLE memory_embeddings (
    memory_version_id TEXT NOT NULL REFERENCES memory_versions(id) ON DELETE CASCADE,
    provider_id TEXT NOT NULL,
    model TEXT NOT NULL,
    dimensions INTEGER NOT NULL CHECK (dimensions > 0 AND dimensions <= 16384),
    vector_blob BLOB NOT NULL CHECK (length(vector_blob) = dimensions * 4),
    content_sha256 TEXT NOT NULL CHECK (length(content_sha256) = 64),
    created_at TEXT NOT NULL,
    PRIMARY KEY(memory_version_id, provider_id, model)
) STRICT, WITHOUT ROWID;

CREATE INDEX memory_embeddings_identity
    ON memory_embeddings(provider_id, model, memory_version_id);

ALTER TABLE retrieval_traces ADD COLUMN strategy_hash TEXT;
