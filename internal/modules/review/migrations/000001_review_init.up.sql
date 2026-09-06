-- The review module's schema.
--
-- # A review is INVISIBLE until an operator approves it
--
-- That is the design constraint the whole table is shaped around, and it is not
-- a preference. Decision A15 in docs/gaps.md asks whether the storefront may
-- accept content from a party this framework cannot identify, and it carries
-- one discriminator: DOES A HUMAN STAND BETWEEN THE WRITE AND ITS EFFECT? The
-- repository's own precedent for a yes is the storefront return request — a
-- record anyone holding the order id can write, which moves no stock and no
-- money until an operator receives it. A review published on APPROVAL has that
-- exact shape and the return request's argument covers it unchanged. A review
-- published on SUBMISSION does not, and would need a decision this repository
-- has not made.
--
-- So status starts at 'submitted', the storefront read filters on 'approved',
-- and the only path between the two runs through an admin endpoint.
--
-- # What this table holds about its author, and what it deliberately does not
--
-- It holds ONE thing: a display name the author typed in order to have it
-- printed. It holds no email address, no phone number and no IP address.
--
-- The distinction is between data given TO BE PUBLISHED and data given for
-- something else. A display name is the byline; the author wrote it knowing it
-- appears under their words. An email address is a contact detail, and storing
-- one here would build a mailing list the shop never asked for and the author
-- never consented to, with no verification and no unsubscribe — which is the
-- precise property that makes the back-in-stock waitlist (C1) FAIL A15. The
-- repository already refuses that class of column elsewhere: the notification
-- module stores no recipient address at all, its delivery log has no column for
-- one, and the address never enters an event payload.
--
-- An IP address is the tempting third column, and it is refused twice over. It
-- would be the only network identifier of a shopper stored anywhere in this
-- repository, and it would buy nothing: the quota that would use it already
-- exists one layer up, on the whole /store/v1 prefix, keyed by the connection.
--
-- # What is NOT here: an order id
--
-- There is deliberately no order_id column, so "verified purchase" is not
-- expressible in this table. Rejected alternative, and why:
--
-- An order id would narrow spam — a writer would need one that really exists —
-- but it would NOT authenticate anybody. ADR 0008 settles customer identity on
-- the embedding application; the order id is exactly the credential the return
-- request runs on, and anyone who has ever seen a confirmation page holds one.
-- A column that cannot mean "the buyer wrote this" would still be read as a
-- verified-purchase badge the first day somebody renders it, and that badge
-- would be a false statement made by this schema.
--
-- Validating the id would cost more and buy the same nothing: the chain is
-- order_line_item (which offers variant_id, not product_id) then variant (which
-- offers product_id), two hops through the read layer, on the WRITE path of an
-- unauthenticated endpoint — and a shop that composes gobit without the order
-- module could then accept no review at all.

-- reviews holds the customer-written reviews of a product.
CREATE TABLE reviews (
    id text PRIMARY KEY,
    -- product_id is the SUBJECT: what the review is about.
    --
    -- It is a product and not a variant because the storefront can address a
    -- product and cannot address a variant: /store/v1/products/{id} exists and
    -- there is no store variant endpoint at all, so a variant-level review
    -- would have no page to appear on.
    --
    -- It is another module's identifier and it is NOT validated here, and there
    -- is no foreign key: Principle 2.2 forbids one across a module boundary,
    -- and the order module's line records its variant_id under exactly the same
    -- rule. What keeps a review of a product that does not exist off the
    -- storefront is the same thing that keeps every other unwanted review off
    -- it — an operator who never approves it.
    product_id      text        NOT NULL,
    -- rating is the star count, 1 to 5.
    --
    -- The bound is in the DATABASE and not only in the service, because the
    -- average computed over this column is a number a shop prints: a single row
    -- carrying 50 would move a product's rating and no read would notice.
    rating          smallint    NOT NULL CHECK (rating BETWEEN 1 AND 5),
    -- title is the headline; it may be empty, because a rating with a sentence
    -- under it is a complete review and demanding a headline would only produce
    -- headlines that repeat the first line of the body.
    title           text        NOT NULL DEFAULT '',
    body            text        NOT NULL,
    -- author_name is the byline the author typed. See the header for why this
    -- is the only thing stored about them.
    author_name     text        NOT NULL,
    -- status is where the review stands. The CHECK is the schema's half of the
    -- transition table in the models package: the service refuses a move the
    -- table does not allow, and this refuses a VALUE the module does not know,
    -- which is the failure a hand-written UPDATE or a future migration produces.
    status          text        NOT NULL
        CHECK (status IN ('submitted', 'approved', 'rejected')),
    -- moderated_at is the moment a human decided.
    --
    -- The mirror is FULL rather than one-directional: nothing returns a review
    -- to 'submitted', so "has been moderated" and "is not submitted" are the
    -- same statement and the constraint can say so in both directions. The
    -- fulfillment module's returned_at carries the same kind of check, and for
    -- the same reason — the state it mirrors is one nothing moves back out of.
    moderated_at    timestamptz,
    -- moderation_note is why. It is required for a rejection by the service,
    -- because a rejection is the decision somebody later has to account for.
    moderation_note text        NOT NULL DEFAULT '',
    created_at      timestamptz NOT NULL DEFAULT now(),
    updated_at      timestamptz NOT NULL DEFAULT now(),
    CONSTRAINT reviews_moderation_mirror
        CHECK ((status = 'submitted') = (moderated_at IS NULL))
);

-- reviews_approved_idx serves BOTH storefront reads: the page of approved
-- reviews of a product, and the count-and-average shown next to it.
--
-- It is PARTIAL on 'approved' because that is the only status the storefront
-- may see, and it carries rating in INCLUDE so the aggregate can be answered
-- without the heap when the visibility map allows it.
--
-- Measured rather than asserted, against PostgreSQL 16 on 505,000 reviews over
-- 20,001 products, with the aggregate the summary endpoint runs:
--
--   without this index          33-38 ms, a full parallel sequential scan, and
--                               the cost is the same whether the product has 19
--                               reviews or 5,000
--   with it, 19 approved        0.17-0.21 ms
--   with it, 5,000 approved     1.3-2.0 ms
--   with it, 50,000 approved    9.3 ms
--   first page of 20            0.03-0.04 ms at every one of those sizes,
--                               because the LIMIT stops the index scan
--
-- The index is 40 MB against a 348 MB table.
--
-- Those numbers are why the average is COMPUTED ON READ and no aggregate is
-- stored; the argument is in the queries file next to the query itself.
CREATE INDEX reviews_approved_idx
    ON reviews (product_id, created_at DESC, id DESC)
    INCLUDE (rating)
    WHERE status = 'approved';

-- reviews_moderation_idx is the moderation queue's own order: oldest first is
-- deliberate and it is the queue's whole point, but the index is declared DESC
-- to match the cursor the listing walks (see the queries file); PostgreSQL reads
-- a b-tree in either direction, so one index serves both.
CREATE INDEX reviews_moderation_idx ON reviews (status, created_at DESC, id DESC);
