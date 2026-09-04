-- Rolling the job runner's schema back.
--
-- The index falls with the table; it is dropped explicitly anyway, because the
-- name space is shared with tables and the day it moves this file would
-- silently be incomplete.
--
-- The data loss is real but harmless, and it is the only rollback in this
-- repository of which that can be said: the table is a HISTORY, not a record
-- anything depends on. What is lost is the answer to "did it run last night".
-- Every job is safe to run again — that is a requirement of the Func contract,
-- not a hope — so the worst consequence is one extra run of each job.
DROP INDEX IF EXISTS job_run_recent_idx;
DROP TABLE IF EXISTS job_run;
