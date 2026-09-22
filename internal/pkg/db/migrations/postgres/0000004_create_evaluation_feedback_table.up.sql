CREATE TABLE evaluation_feedback (
    id               TEXT        PRIMARY KEY,
    evaluation_id    TEXT        NOT NULL,
    expected_outcome TEXT        NOT NULL,
    note             TEXT        NOT NULL DEFAULT '',
    created_at       TIMESTAMPTZ NOT NULL
);

CREATE INDEX idx_evaluation_feedback_evaluation_id
    ON evaluation_feedback (evaluation_id);
