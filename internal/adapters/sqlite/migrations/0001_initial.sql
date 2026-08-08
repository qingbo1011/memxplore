CREATE TABLE schema_migrations (
    version INTEGER PRIMARY KEY,
    name TEXT NOT NULL,
    sha256 TEXT NOT NULL,
    applied_at TEXT NOT NULL
) STRICT;

CREATE TABLE observations (
    id TEXT PRIMARY KEY,
    namespace_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    context_id TEXT,
    visibility TEXT NOT NULL CHECK (visibility IN ('private', 'shared', 'public')),
    source_kind TEXT NOT NULL,
    source_reference TEXT,
    content_json TEXT NOT NULL CHECK (json_valid(content_json)),
    evidence_class TEXT NOT NULL CHECK (evidence_class IN ('untrusted', 'trusted', 'policy')),
    policy_authority TEXT,
    metadata_json TEXT NOT NULL CHECK (json_valid(metadata_json)),
    captured_at TEXT NOT NULL,
    stored_at TEXT NOT NULL
) STRICT;

CREATE INDEX observations_namespace_subject_time
    ON observations(namespace_id, subject_id, captured_at);

CREATE TABLE memories (
    id TEXT PRIMARY KEY,
    namespace_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    context_id TEXT,
    visibility TEXT NOT NULL CHECK (visibility IN ('private', 'shared', 'public')),
    function TEXT NOT NULL CHECK (function IN ('factual', 'experiential', 'working')),
    state TEXT NOT NULL CHECK (state IN ('active', 'archived', 'forgotten')),
    current_version INTEGER NOT NULL CHECK (current_version > 0),
    created_at TEXT NOT NULL
) STRICT;

CREATE INDEX memories_recall_scope
    ON memories(namespace_id, subject_id, function, state, visibility);

CREATE TABLE memory_versions (
    id TEXT PRIMARY KEY,
    memory_id TEXT NOT NULL REFERENCES memories(id) ON DELETE CASCADE,
    version_number INTEGER NOT NULL CHECK (version_number > 0),
    state TEXT NOT NULL CHECK (state IN ('current', 'superseded', 'archived', 'forgotten', 'stale')),
    taxonomy_json TEXT NOT NULL CHECK (json_valid(taxonomy_json)),
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    provenance_json TEXT NOT NULL CHECK (json_valid(provenance_json)),
    supersedes_json TEXT NOT NULL CHECK (json_valid(supersedes_json)),
    derived_from_json TEXT NOT NULL CHECK (json_valid(derived_from_json)),
    valid_from TEXT NOT NULL,
    valid_to TEXT,
    system_from TEXT NOT NULL,
    system_to TEXT,
    conflict_group_id TEXT,
    created_at TEXT NOT NULL,
    UNIQUE(memory_id, version_number)
) STRICT;

CREATE INDEX memory_versions_temporal
    ON memory_versions(memory_id, valid_from, valid_to, system_from, system_to, state);
CREATE INDEX memory_versions_conflicts
    ON memory_versions(conflict_group_id) WHERE conflict_group_id IS NOT NULL;

CREATE TABLE memory_dependencies (
    parent_version_id TEXT NOT NULL REFERENCES memory_versions(id) ON DELETE CASCADE,
    child_version_id TEXT NOT NULL REFERENCES memory_versions(id) ON DELETE CASCADE,
    PRIMARY KEY(parent_version_id, child_version_id),
    CHECK (parent_version_id <> child_version_id)
) STRICT, WITHOUT ROWID;

CREATE VIRTUAL TABLE memory_fts USING fts5(
    memory_version_id UNINDEXED,
    memory_id UNINDEXED,
    namespace_id UNINDEXED,
    text_content,
    tokenize = 'unicode61 remove_diacritics 2'
);

INSERT INTO memory_fts(memory_fts, rank) VALUES('secure-delete', 1);

CREATE TABLE episodes (
    id TEXT PRIMARY KEY,
    namespace_id TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    task_json TEXT NOT NULL CHECK (json_valid(task_json)),
    observation_ids_json TEXT NOT NULL CHECK (json_valid(observation_ids_json)),
    started_at TEXT NOT NULL,
    ended_at TEXT NOT NULL
) STRICT;

CREATE TABLE outcomes (
    id TEXT PRIMARY KEY,
    episode_id TEXT NOT NULL REFERENCES episodes(id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    value REAL NOT NULL,
    evidence_json TEXT NOT NULL CHECK (json_valid(evidence_json)),
    observed_at TEXT NOT NULL
) STRICT;

CREATE TABLE working_sets (
    id TEXT PRIMARY KEY,
    namespace_id TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    subject_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    context_id TEXT,
    visibility TEXT NOT NULL CHECK (visibility IN ('private', 'shared', 'public')),
    task_id TEXT NOT NULL,
    goal_json TEXT NOT NULL CHECK (json_valid(goal_json)),
    global_recall INTEGER NOT NULL DEFAULT 0 CHECK (global_recall IN (0, 1)),
    expires_at TEXT,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(namespace_id, task_id)
) STRICT;

CREATE TABLE operations (
    id TEXT PRIMARY KEY,
    phase TEXT NOT NULL CHECK (phase IN ('observe', 'propose', 'apply')),
    kind TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    target_id TEXT NOT NULL,
    proposal_id TEXT,
    strategy_id TEXT,
    occurred_at TEXT NOT NULL,
    result TEXT NOT NULL
) STRICT;

CREATE TABLE retrieval_traces (
    id TEXT PRIMARY KEY,
    namespace_id TEXT NOT NULL,
    scope_json TEXT NOT NULL CHECK (json_valid(scope_json)),
    query TEXT NOT NULL,
    strategy_id TEXT NOT NULL,
    fallback_reason TEXT,
    valid_at TEXT NOT NULL,
    system_at TEXT NOT NULL,
    token_budget INTEGER NOT NULL CHECK (token_budget >= 0),
    tokens_used INTEGER NOT NULL CHECK (tokens_used >= 0 AND tokens_used <= token_budget),
    started_at TEXT NOT NULL,
    completed_at TEXT NOT NULL
) STRICT;

CREATE TABLE retrieval_candidates (
    trace_id TEXT NOT NULL REFERENCES retrieval_traces(id) ON DELETE CASCADE,
    ordinal INTEGER NOT NULL CHECK (ordinal >= 0),
    candidate_json TEXT NOT NULL CHECK (json_valid(candidate_json)),
    PRIMARY KEY(trace_id, ordinal)
) STRICT, WITHOUT ROWID;

CREATE TABLE durable_jobs (
    id TEXT PRIMARY KEY,
    namespace_id TEXT NOT NULL,
    kind TEXT NOT NULL,
    state TEXT NOT NULL CHECK (state IN ('queued', 'running', 'succeeded', 'failed', 'canceled')),
    idempotency_key TEXT NOT NULL,
    payload_json TEXT NOT NULL CHECK (json_valid(payload_json)),
    result_json TEXT CHECK (result_json IS NULL OR json_valid(result_json)),
    error_code TEXT,
    error_message TEXT,
    attempts INTEGER NOT NULL DEFAULT 0 CHECK (attempts >= 0),
    lease_owner TEXT,
    lease_expires_at TEXT,
    available_at TEXT NOT NULL,
    created_at TEXT NOT NULL,
    updated_at TEXT NOT NULL,
    UNIQUE(namespace_id, kind, idempotency_key)
) STRICT;

CREATE INDEX durable_jobs_claim
    ON durable_jobs(state, available_at, lease_expires_at);

CREATE TABLE artifacts (
    digest TEXT PRIMARY KEY,
    media_type TEXT NOT NULL,
    size INTEGER NOT NULL CHECK (size >= 0),
    created_at TEXT NOT NULL
) STRICT;

CREATE TABLE artifact_references (
    artifact_digest TEXT NOT NULL REFERENCES artifacts(digest) ON DELETE RESTRICT,
    owner_type TEXT NOT NULL,
    owner_id TEXT NOT NULL,
    PRIMARY KEY(artifact_digest, owner_type, owner_id)
) STRICT, WITHOUT ROWID;

CREATE TABLE purge_receipts (
    id TEXT PRIMARY KEY,
    namespace_id TEXT NOT NULL,
    target_type TEXT NOT NULL,
    target_id TEXT NOT NULL,
    actor_id TEXT NOT NULL,
    versions_deleted INTEGER NOT NULL CHECK (versions_deleted >= 0),
    artifacts_detached INTEGER NOT NULL CHECK (artifacts_detached >= 0),
    purged_at TEXT NOT NULL
) STRICT;

CREATE INDEX purge_receipts_subjectless_lookup
    ON purge_receipts(namespace_id, target_type, target_id, purged_at);

