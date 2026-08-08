CREATE TABLE api_tokens (
    id TEXT PRIMARY KEY,
    token_sha256 TEXT NOT NULL UNIQUE CHECK (length(token_sha256) = 64),
    principal_id TEXT NOT NULL,
    namespace_id TEXT NOT NULL,
    private_owners_json TEXT NOT NULL CHECK (json_valid(private_owners_json)),
    scopes_json TEXT NOT NULL CHECK (json_valid(scopes_json)),
    allow_shared INTEGER NOT NULL DEFAULT 0 CHECK (allow_shared IN (0, 1)),
    allow_public INTEGER NOT NULL DEFAULT 0 CHECK (allow_public IN (0, 1)),
    expires_at TEXT,
    revoked_at TEXT,
    created_at TEXT NOT NULL,
    last_used_at TEXT
) STRICT;

CREATE INDEX api_tokens_principal
    ON api_tokens(namespace_id, principal_id, revoked_at, expires_at);

CREATE TABLE agent_event_receipts (
    event_id TEXT NOT NULL,
    schema_version TEXT NOT NULL,
    source TEXT NOT NULL,
    observation_id TEXT NOT NULL REFERENCES observations(id) ON DELETE RESTRICT,
    job_id TEXT NOT NULL REFERENCES durable_jobs(id) ON DELETE RESTRICT,
    received_at TEXT NOT NULL,
    PRIMARY KEY (source, event_id)
) STRICT;
