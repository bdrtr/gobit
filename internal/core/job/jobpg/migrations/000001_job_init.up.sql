-- The job runner's schema: one row per OCCURRENCE.
--
-- The row is the frequency half of the election. It answers "has this
-- occurrence already run?" and nothing else; whether a process is running the
-- job RIGHT NOW is answered by an advisory lock, which needs no storage and no
-- expiry (see internal/core/job's package documentation).
--
-- The two halves cannot drift because neither is ever consulted for the other's
-- question.
CREATE TABLE IF NOT EXISTS job_run (
    -- name and due together identify one occurrence, and the PRIMARY KEY is the
    -- election itself: the first instance to insert wins, every other one gets
    -- a conflict and does nothing. No leader, no coordinator, no vote.
    name text NOT NULL,
    -- due is the occurrence instant, anchored to the epoch rather than to
    -- process start. That anchoring is what makes two instances that booted
    -- minutes apart compute the SAME instant and collide on this key.
    due timestamptz NOT NULL,

    started_at timestamptz NOT NULL DEFAULT now(),
    -- ended_at is NULL while the run is in progress, and STAYS NULL if the
    -- process died before recording an end. That is deliberate: an unfinished
    -- row is the visible trace of a process that disappeared, and it is the
    -- only trace there would otherwise be.
    ended_at timestamptz,

    -- failure is empty on success. A failed run is still a run that HAPPENED
    -- and is recorded as such — hiding it would make the listing claim the job
    -- has not run since its last success.
    failure text NOT NULL DEFAULT '',
    -- detail is a one-line note the job may leave for the operator, such as a
    -- count. It carries no personal data: it is printed by `gobit jobs`.
    detail text NOT NULL DEFAULT '',

    PRIMARY KEY (name, due)
);

-- The operator's question is always "when did this last run", so the index is
-- the reverse of the primary key's order.
CREATE INDEX IF NOT EXISTS job_run_recent_idx ON job_run (name, due DESC);
