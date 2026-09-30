-- Rolling back forgets which sessions were closed one by one: a token that
-- names a closed row is accepted again, judged by the anchor alone, until it
-- expires.
DROP TABLE IF EXISTS auth_session;
