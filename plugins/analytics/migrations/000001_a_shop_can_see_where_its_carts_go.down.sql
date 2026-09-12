-- Rolling back drops the funnel's history, and that is the honest loss: no other
-- table holds these rows, and the events they came from are long gone from the
-- bus. A shop that rolls this back starts counting again from zero.
DROP TABLE IF EXISTS analytics_events;
