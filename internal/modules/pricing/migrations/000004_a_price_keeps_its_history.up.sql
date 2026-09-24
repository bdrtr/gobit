-- A price keeps its history (ADR 0167).
--
-- ADR 0047 deleted a replaced price and kept nothing, and said why: the rows the
-- old code left behind had no reader, and a history nobody reads is a cost. The
-- reader now exists. A shop that announces a reduction has to be able to say
-- what the price was — the lowest price it applied in the thirty days before the
-- reduction began — and a catalog that forgets every replaced amount cannot.
--
-- # What is kept is the INPUT of the ladder, not its answer
--
-- The price a shopper is charged is computed: the ladder in the service picks
-- among a set's live prices, and a price list's status and window decide which
-- of them compete at a given moment. That answer changes without any write — a
-- sale list's window opens at its starts_at whether or not anybody touches the
-- catalog — so storing the answer at write time would miss exactly the changes a
-- reduction is made of. What is stored instead is everything the ladder reads,
-- as it stood after every write: a set's live prices with their rules, and a
-- list's type, status and window. The price at any past moment is the ladder run
-- over the last snapshots before it, and the service does that.
--
-- # A snapshot per write, and never an update
--
-- Each table is appended to by the transaction that changed what it records,
-- in the repository, and by nothing else; there is no UPDATE and no DELETE
-- against either anywhere in this module's queries, and an arch gate reads the
-- query files to hold that. A snapshot is the whole state rather than a
-- difference, so reading the state at a moment needs one row per set, not a
-- replay.
--
-- seq orders two snapshots of one set taken at the same instant: recorded_at is
-- the application's clock (ADR 0053), which the calling write already stamps its
-- own rows with, and two writes in one millisecond are not ordered by it.
CREATE TABLE IF NOT EXISTS price_set_history (
    id           TEXT        PRIMARY KEY,
    seq          BIGINT      NOT NULL GENERATED ALWAYS AS IDENTITY,
    price_set_id TEXT        NOT NULL,
    recorded_at  TIMESTAMPTZ NOT NULL,
    -- The set's live prices after the write, each with its rules, in the shape
    -- the repository writes and reads (repository/history.go). An empty array is
    -- a set with no price, which is also what a deleted set leaves.
    prices       JSONB       NOT NULL,
    CONSTRAINT price_set_history_prices_is_array CHECK (jsonb_typeof(prices) = 'array'),
    CONSTRAINT price_set_history_set_not_blank CHECK (btrim(price_set_id) <> '')
);

CREATE UNIQUE INDEX IF NOT EXISTS price_set_history_seq_key
    ON price_set_history (seq);
CREATE INDEX IF NOT EXISTS price_set_history_set_idx
    ON price_set_history (price_set_id, recorded_at, seq);

CREATE TABLE IF NOT EXISTS price_list_history (
    id            TEXT        PRIMARY KEY,
    seq           BIGINT      NOT NULL GENERATED ALWAYS AS IDENTITY,
    price_list_id TEXT        NOT NULL,
    recorded_at   TIMESTAMPTZ NOT NULL,
    type          TEXT        NOT NULL,
    status        TEXT        NOT NULL,
    starts_at     TIMESTAMPTZ NULL,
    ends_at       TIMESTAMPTZ NULL,
    -- A deleted list offers no price, which the candidate query already says by
    -- joining only live lists; the snapshot says it the same way.
    deleted       BOOLEAN     NOT NULL,
    CONSTRAINT price_list_history_list_not_blank CHECK (btrim(price_list_id) <> '')
);

CREATE UNIQUE INDEX IF NOT EXISTS price_list_history_seq_key
    ON price_list_history (seq);
CREATE INDEX IF NOT EXISTS price_list_history_list_idx
    ON price_list_history (price_list_id, recorded_at, seq);

-- # The history starts here
--
-- Every live set and every list is recorded once, as it stands, stamped with the
-- database's clock at migration time. Nothing before that moment is known — ADR
-- 0047 deleted it — and the service reports a window that reaches further back
-- as not covered rather than inventing a price for it.
INSERT INTO price_set_history (id, price_set_id, recorded_at, prices)
SELECT 'psethist_seed_' || s.id,
       s.id,
       now(),
       COALESCE((
           SELECT jsonb_agg(jsonb_build_object(
                      'id', p.id,
                      'price_list_id', p.price_list_id,
                      'currency_code', p.currency_code,
                      'amount', p.amount,
                      'min_quantity', p.min_quantity,
                      'max_quantity', p.max_quantity,
                      'rules', COALESCE((
                          SELECT jsonb_agg(jsonb_build_object(
                                     'attribute', r.attribute,
                                     'operator', r.operator,
                                     'values', to_jsonb(r.rule_values))
                                 ORDER BY r.id)
                          FROM price_rule r
                          WHERE r.price_id = p.id AND r.deleted_at IS NULL), '[]'::jsonb))
                  ORDER BY p.id)
           FROM price p
           WHERE p.price_set_id = s.id AND p.deleted_at IS NULL), '[]'::jsonb)
FROM price_set s
WHERE s.deleted_at IS NULL;

INSERT INTO price_list_history
    (id, price_list_id, recorded_at, type, status, starts_at, ends_at, deleted)
SELECT 'plisthist_seed_' || l.id,
       l.id,
       now(),
       l.type,
       l.status,
       l.starts_at,
       l.ends_at,
       l.deleted_at IS NOT NULL
FROM price_list l;
