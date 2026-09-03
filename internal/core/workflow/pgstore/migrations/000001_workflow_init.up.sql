-- The workflow engine's execution state (plan Section 5.5).
--
-- Owner: the "workflow" core component. The only foreign key here is between
-- two tables of the SAME owner; the cross-module FK ban (Principle 2.2) is not
-- violated.

CREATE TABLE workflow_executions (
    -- id is a time-ordered id with the "wfx_" prefix; the application produces it.
    id              TEXT        PRIMARY KEY,
    workflow        TEXT        NOT NULL,
    -- idempotency_key is optional: keyless executions carry NULL. The empty
    -- string is not stored; NULL is the ONLY representation of "no key".
    idempotency_key TEXT,
    status          TEXT        NOT NULL,
    -- input/output are business data, stored as JSONB. NULL means "no value"
    -- and differs from JSON's own null value ('null').
    input           JSONB,
    output          JSONB,
    failure         TEXT        NOT NULL DEFAULT '',
    created_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    updated_at      TIMESTAMPTZ NOT NULL DEFAULT now(),
    CONSTRAINT workflow_executions_workflow_not_blank
        CHECK (workflow <> ''),
    CONSTRAINT workflow_executions_status_not_blank
        CHECK (status <> ''),
    -- A CHECK is not evaluated on NULL; this constraint only rules out the
    -- empty string.
    CONSTRAINT workflow_executions_idempotency_key_not_blank
        CHECK (idempotency_key <> '')
);

-- Idempotency is unique only for NON-NULL keys. The partial index
-- (WHERE ... IS NOT NULL) says so explicitly, and since NULLs do not clash with
-- each other, keyless executions can be opened freely.
--
-- Resistance to the race rests on this index: if two processes insert at the
-- same instant one gets 23505 and the application turns it into a Conflict.
-- "SELECT first, then INSERT" could not give that guarantee.
CREATE UNIQUE INDEX workflow_executions_idempotency_key_uniq
    ON workflow_executions (workflow, idempotency_key)
    WHERE idempotency_key IS NOT NULL;

-- Listing a workflow's most recent executions is a common access shape.
CREATE INDEX workflow_executions_workflow_created_at_idx
    ON workflow_executions (workflow, created_at DESC);

CREATE TABLE workflow_execution_steps (
    execution_id TEXT        NOT NULL
                 REFERENCES workflow_executions (id) ON DELETE CASCADE,
    -- The column is named step_index: "index" is a keyword in PostgreSQL and
    -- cannot be used unquoted, and a quoted name is a trap in every query.
    step_index   INTEGER     NOT NULL,
    name         TEXT        NOT NULL,
    status       TEXT        NOT NULL,
    output       JSONB,
    failure      TEXT        NOT NULL DEFAULT '',
    -- attempts is the retry counter; it grows when the same step is retried.
    attempts     INTEGER     NOT NULL DEFAULT 0,
    -- The times are NULL when they were not measured (the counterpart of a
    -- zero time.Time).
    started_at   TIMESTAMPTZ,
    ended_at     TIMESTAMPTZ,
    -- (execution_id, step_index) is the primary key: writing the same step a
    -- second time does not OPEN a new row, it updates the existing one (with
    -- ON CONFLICT).
    PRIMARY KEY (execution_id, step_index),
    CONSTRAINT workflow_execution_steps_step_index_non_negative
        CHECK (step_index >= 0),
    CONSTRAINT workflow_execution_steps_attempts_non_negative
        CHECK (attempts >= 0),
    CONSTRAINT workflow_execution_steps_name_not_blank
        CHECK (name <> ''),
    CONSTRAINT workflow_execution_steps_status_not_blank
        CHECK (status <> '')
);
