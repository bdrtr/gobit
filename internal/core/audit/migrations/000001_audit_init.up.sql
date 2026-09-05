-- The audit log: one row per admin WRITE that reached the server.
--
-- # What a row is, and what it deliberately is not
--
-- It records the REQUEST — who, what they called, what came back — and not the
-- change. A diff would mean every module producing a before-and-after for every
-- write, which is a contract in fifteen places and a cost on every request; a
-- bare "a product was updated" would be cheaper and worth nothing.
--
-- What this answers is the question an incident actually starts with: who
-- touched this surface, when, and did it succeed. The WHAT is then read from
-- the record itself, which already carries updated_at.
--
-- # Only writes, and only the admin surface
--
-- Reads are volume without an answer: knowing that somebody listed the orders
-- does not help anyone. The storefront is unauthenticated by decision (ADR
-- 0008), so a row there would record "somebody" and mean nothing.
CREATE TABLE IF NOT EXISTS audit_log (
    id           text        PRIMARY KEY,
    -- actor_id and actor_kind are the caller: a user or an api key.
    --
    -- They are stored as TEXT with no foreign key: the identities belong to the
    -- auth module (Principle 2.2), and a deleted user must not take their audit
    -- trail with them — which is exactly when the trail matters most.
    actor_id     text        NOT NULL,
    actor_kind   text        NOT NULL,
    method       text        NOT NULL,
    path         text        NOT NULL,
    status       integer     NOT NULL,
    -- request_id ties the row to the log lines of the same request.
    request_id   text        NOT NULL DEFAULT '',
    created_at   timestamptz NOT NULL DEFAULT now(),

    CONSTRAINT audit_log_method_not_empty CHECK (method <> ''),
    CONSTRAINT audit_log_path_not_empty   CHECK (path <> ''),
    CONSTRAINT audit_log_status_range     CHECK (status BETWEEN 100 AND 599)
);

-- The two questions an operator asks: "what did this person do" and "what
-- happened to this endpoint". Both are answered newest-first.
CREATE INDEX IF NOT EXISTS audit_log_actor_idx
    ON audit_log (actor_id, created_at DESC, id DESC);

CREATE INDEX IF NOT EXISTS audit_log_path_idx
    ON audit_log (path, created_at DESC, id DESC);
