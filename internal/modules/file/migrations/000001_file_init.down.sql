-- Rollback of 000001_file_init.
--
-- There is a single table and it is bound to no table; the indexes fall
-- together with the table, they are not DROPped separately.
--
-- CAUTION: This DOES NOT delete THE FILES IN THE STORE. The migration rolls the
-- database back; the uploaded bytes stay in the root directory (or in the
-- object store). That is deliberate — a schema rollback must not trigger a
-- data deletion that cannot be rolled back. The files must be cleaned up while
-- their records are still readable.

DROP TABLE IF EXISTS file_uploads;
