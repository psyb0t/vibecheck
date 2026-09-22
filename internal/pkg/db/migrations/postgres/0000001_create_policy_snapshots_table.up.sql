CREATE TABLE policy_snapshots (
    hash            TEXT        PRIMARY KEY,
    name            TEXT        NOT NULL,
    version         TEXT        NOT NULL,
    canonical_json  TEXT        NOT NULL,
    loaded_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_policy_snapshots_name_version
    ON policy_snapshots (name, version);
