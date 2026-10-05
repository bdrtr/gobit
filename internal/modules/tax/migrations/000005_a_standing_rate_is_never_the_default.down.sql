-- The rollback takes the CHECK out. The flags the forward step cleared are not
-- put back: the older schema would hold them, but no row carried one the
-- calculation ever read.
ALTER TABLE tax_rate DROP CONSTRAINT IF EXISTS tax_rate_standing_default_check;
