CREATE TABLE evaluations (
    id                    TEXT        PRIMARY KEY,
    policy_snapshot_hash  TEXT        NOT NULL,
    policy_name           TEXT        NOT NULL,
    policy_version        TEXT        NOT NULL,
    policy_kind           TEXT        NOT NULL,
    status                TEXT        NOT NULL,
    outcome               TEXT        NOT NULL DEFAULT '',
    matched_rule_id       TEXT        NOT NULL DEFAULT '',
    execution_path        TEXT        NOT NULL DEFAULT '',
    requested_model       TEXT        NOT NULL DEFAULT '',
    resolved_model        TEXT        NOT NULL DEFAULT '',
    answers_json          TEXT        NOT NULL DEFAULT '',
    input_tokens          INTEGER     NOT NULL DEFAULT 0,
    output_tokens         INTEGER     NOT NULL DEFAULT 0,
    provider_attempts     INTEGER     NOT NULL DEFAULT 0,
    provider_duration_ms  INTEGER      NOT NULL DEFAULT 0,
    total_duration_ms     INTEGER      NOT NULL DEFAULT 0,
    request_id            TEXT        NOT NULL DEFAULT '',
    metadata_json         TEXT        NOT NULL DEFAULT '',
    encrypted_input       BLOB ,
    failure_code          TEXT        NOT NULL DEFAULT '',
    created_at            DATETIME    NOT NULL
);

-- Listing is newest first with the ID as a stable tiebreaker, so the index
-- carries both columns in that exact order.
CREATE INDEX idx_evaluations_created_at_id ON evaluations (created_at DESC, id DESC);
CREATE INDEX idx_evaluations_policy ON evaluations (policy_name, policy_version);
CREATE INDEX idx_evaluations_outcome ON evaluations (outcome);
CREATE INDEX idx_evaluations_snapshot ON evaluations (policy_snapshot_hash);
