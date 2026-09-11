-- The relying party a credential belongs to.
--
-- A passkey is scoped to an RP ID by the authenticator that minted it: a
-- credential created for `shop.example` is not offered when the browser is asked
-- for `example.com`, and cannot be. So an installation that changes
-- `Options.RPID` — a domain move, or dropping a subdomain — turns every existing
-- row into something that is no longer a way into the account.
--
-- Until this column the module could not tell which rows those were, and the
-- guard that refuses to remove somebody's last way in COUNTED them. A person
-- holding one old key and one new one was told they had two, and removing the
-- new one was permitted (gap D68).
--
-- NULL means a row written before this column existed. It is read as "the
-- relying party this installation is configured for", which is exactly what
-- those rows were registered under unless the installation already moved — and
-- in that case the information was never recorded and no backfill can invent it.
ALTER TABLE passkey_credentials
    ADD COLUMN IF NOT EXISTS rp_id TEXT;

-- Blank is not a relying party id. NULL is a row from before the column.
ALTER TABLE passkey_credentials
    ADD CONSTRAINT passkey_credentials_rp_id_not_blank
    CHECK (rp_id IS NULL OR length(btrim(rp_id)) > 0);

-- Every read of this module is scoped by customer AND relying party, so the
-- index that serves them carries both. It replaces the customer-only index
-- rather than joining it: a two-column index with customer_id first answers a
-- customer-only question just as well.
CREATE INDEX IF NOT EXISTS passkey_credentials_customer_rp_idx
    ON passkey_credentials (customer_id, rp_id);
DROP INDEX IF EXISTS passkey_credentials_customer_idx;
