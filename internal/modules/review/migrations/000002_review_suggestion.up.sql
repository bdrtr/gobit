-- A machine's PROPOSAL about a review, stored beside the review.
--
-- # A suggestion is not a moderation, and the schema says so twice
--
-- The whole table is shaped around one sentence — a review is invisible until a
-- human approves it (000001) — and a column written by a model is the obvious
-- way to break that sentence without noticing. So the proposal is kept in
-- columns of its own and touches NEITHER of the two the human writes:
--
--   status        stays 'submitted' until an operator moves it
--   moderated_at  stays NULL until an operator moves it
--
-- which is what keeps reviews_moderation_mirror meaning exactly what it said
-- when it was written: `(status = 'submitted') = (moderated_at IS NULL)` is the
-- record of a HUMAN decision, and nothing here can set either side of it. The
-- storefront read is unchanged for the same reason — it filters on 'approved',
-- and no proposal produces that value.
--
-- # Why the proposal is stored at all
--
-- The alternative is to compute it when the queue is opened. That was refused:
-- a proposal computed on read is paid for on every page of the queue, by the
-- operator waiting for it, and it changes under them between two loads of the
-- same page — the row they were reading said reject, and after a refresh it
-- says approve, with nothing to point at. A stored proposal is computed once,
-- costs the queue nothing, and can be shown with the moment it was made.
--
-- # Four columns, because a proposal that cannot be weighed is worth nothing
--
-- An operator is being asked to trust a machine, and the two questions they
-- will ask are "why" and "which one". A proposal that answers neither is a word
-- on a screen with no way to check it, and the operator's only honest response
-- to that is to ignore it — at which point the column is cost with no benefit.
ALTER TABLE reviews
    -- suggested_status is what a model proposed: approve it, or reject it.
    --
    -- 'submitted' is NOT in the CHECK, and it is not an omission. Proposing
    -- that a review stay where it is proposes nothing; the row already says
    -- that, and a machine writing it would put a moment and a model name on a
    -- non-decision an operator then has to read past.
    ADD COLUMN suggested_status text
        CHECK (suggested_status IS NULL OR suggested_status IN ('approved', 'rejected')),
    -- suggested_at is when the proposal was made.
    --
    -- It is the proposal's own moment and not the row's updated_at, because the
    -- two answer different questions: a shop that changes its model wants to
    -- know which proposals predate the change, and updated_at moves for every
    -- other write to the row as well.
    ADD COLUMN suggested_at timestamptz,
    -- suggestion_note is the model's reason, in the model's words.
    --
    -- It is FREE TEXT about a stranger's free text and it is declared personal
    -- data for that reason (see module.go): a reason for rejecting a review
    -- quotes the review, and the review is the author's own words.
    ADD COLUMN suggestion_note text NOT NULL DEFAULT '',
    -- suggestion_model is which model said it, as the provider names it.
    --
    -- Without it a proposal is anonymous, and an anonymous proposal cannot be
    -- retired: a shop that stops trusting a model has no way to tell its
    -- proposals from the ones it still trusts, and would have to clear the
    -- column for every review or trust none of them.
    ADD COLUMN suggestion_model text NOT NULL DEFAULT '',
    -- The three mirrors are one statement said three ways: a proposal exists
    -- whole, or not at all.
    --
    -- They are written as equalities rather than as "IS NOT NULL implies", in
    -- the same shape as reviews_moderation_mirror, because each one also
    -- forbids the RESIDUE — a note or a model name left behind by a proposal
    -- that was cleared, which would otherwise sit in the row attributing a
    -- sentence to a model about a decision nobody can see.
    ADD CONSTRAINT reviews_suggestion_mirror
        CHECK ((suggested_status IS NULL) = (suggested_at IS NULL)),
    ADD CONSTRAINT reviews_suggestion_reasoned
        CHECK ((suggested_status IS NULL) = (suggestion_note = '')),
    ADD CONSTRAINT reviews_suggestion_attributed
        CHECK ((suggested_status IS NULL) = (suggestion_model = ''));
