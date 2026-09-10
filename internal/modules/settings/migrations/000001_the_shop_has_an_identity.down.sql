-- The table goes and the shop stops having an identity of its own.
--
-- What comes back is the state before it: whoever issues a document has to send
-- the seller with every request, and two documents from one shop can name two
-- different sellers.
DROP TABLE IF EXISTS store_profile;
