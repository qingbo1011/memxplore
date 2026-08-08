CREATE UNIQUE INDEX operations_apply_proposal
    ON operations(proposal_id)
    WHERE phase = 'apply' AND proposal_id IS NOT NULL;

CREATE TABLE experiential_feedback (
    memory_version_id TEXT NOT NULL REFERENCES memory_versions(id) ON DELETE CASCADE,
    trace_id TEXT NOT NULL REFERENCES retrieval_traces(id) ON DELETE CASCADE,
    source_id TEXT NOT NULL,
    value REAL NOT NULL CHECK (value >= -1 AND value <= 1),
    recorded_at TEXT NOT NULL,
    PRIMARY KEY(memory_version_id, trace_id, source_id)
) STRICT, WITHOUT ROWID;

ALTER TABLE working_sets ADD COLUMN state TEXT NOT NULL DEFAULT 'active'
    CHECK (state IN ('active', 'expired', 'archived'));

CREATE INDEX working_sets_recall
    ON working_sets(namespace_id, task_id, state, global_recall, expires_at);

ALTER TABLE retrieval_traces ADD COLUMN filter_json TEXT NOT NULL DEFAULT '{}'
    CHECK (json_valid(filter_json));
