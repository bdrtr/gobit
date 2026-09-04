-- The rollback of 000001_notification_init.
--
-- There is a single table and it is bound to no table; the indexes fall
-- together with the table, they are not DROPped separately.

DROP TABLE IF EXISTS notification_deliveries;
