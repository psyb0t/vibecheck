CREATE TABLE idempotency_records (
    key_hash               TEXT        PRIMARY KEY,
    canonical_request_hash TEXT        NOT NULL,
    evaluation_id          TEXT        NOT NULL,
    expires_at             DATETIME    NOT NULL,
    created_at             DATETIME    NOT NULL
);

CREATE INDEX idx_idempotency_records_expires_at ON idempotency_records (expires_at);
