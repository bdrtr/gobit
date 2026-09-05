package outbox

import (
	"context"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// CodeDeadLetterFailed reports that a dead letter could not be read or changed.
const CodeDeadLetterFailed = "outbox_dead_letter_failed"

// DeadLetter is one event the relay stopped trying to deliver.
//
// It carries the WHY as well as the what. An id and a name would say which
// promise was broken; without the attempt count and the last error, the next
// question — "is the receiver down, or is this payload wrong?" — has no answer
// short of reading the application's logs from four hours ago.
//
// The payload is deliberately absent. A dead letter is read by a human during
// an incident and printed somewhere, and an event's Data can carry an address,
// a name or a phone number. The row still has it, so a redrive loses nothing;
// this type is the part that is safe to look at.
type DeadLetter struct {
	// ID and Name identify the event.
	ID   string
	Name string
	// Attempts is how many deliveries were tried before giving up.
	Attempts int64
	// LastError is what the final attempt reported.
	LastError string
	// CreatedAt is when the transaction that promised the event committed, and
	// DeadLetteredAt is when the relay gave up. The distance between them is
	// how long the promise went unkept.
	CreatedAt      time.Time
	DeadLetteredAt time.Time
}

// DeadLetterReport is the pile, and a sample of it.
//
// Count is the whole pile and Oldest is the part that fits. They are separate
// because a report that showed only the sample would answer "what broke?" and
// not "how much?", and during an outage the second question is the one that
// decides whether anybody is woken up.
type DeadLetterReport struct {
	// Count is how many dead letters the table holds, ignoring the limit.
	Count int64
	// Oldest are the earliest given up on, up to the limit asked for.
	Oldest []DeadLetter
}

// Empty reports whether there is nothing for a human to look at.
func (r DeadLetterReport) Empty() bool { return r.Count == 0 }

// selectDeadLettersSQL reads the pile and measures it in one pass.
//
// count(*) OVER () is evaluated before LIMIT, so it counts the whole filtered
// set rather than the page. Two round trips would have been the obvious
// alternative and they can DISAGREE: a relay dead-lettering rows between the
// count and the page would report a number no sample explains, once a minute,
// with nothing saying the two came from different instants.
//
// The scan it costs is bounded by the partial index on dead_lettered_at rather
// than by the table, which matters because this query runs on every relay pass
// forever — see the migration for why that index is a readability decision and
// not an optimization.
const selectDeadLettersSQL = `
SELECT id, name, attempts, last_error, created_at, dead_lettered_at, count(*) OVER () AS total
FROM event_outbox
WHERE dead_lettered_at IS NOT NULL
ORDER BY dead_lettered_at, id
LIMIT $1`

// redriveSQL puts dead letters back in the queue.
//
// attempts is reset rather than kept, and that is the whole meaning of the
// verb. An operator redrives after fixing the cause; a row that kept its count
// would be back on the pile after ONE more failure, which is a single retry
// wearing the name of a second chance.
//
// The dead_lettered_at IS NOT NULL predicate is not redundant with the id
// list. It is what stops this from being a way to reset the attempt count of a
// live row — a healthy event mid-backoff would jump the queue and lose its
// history to a typo in an id.
const redriveSQL = `
UPDATE event_outbox
SET dead_lettered_at = NULL, attempts = 0, next_attempt_at = now()
WHERE dead_lettered_at IS NOT NULL AND id = ANY($1)`

// discardSQL removes dead letters for good.
//
// A DELETE rather than a flag, because there is no honest flag to set. Marking
// the row published would say the event went out; a third timestamp would be a
// state nothing else in this schema reads. What discarding means is that a
// human looked at the event and decided nobody is owed it, and the only record
// that survives is the one the relay already wrote to the log when it gave up.
//
// It, too, refuses rows that are not dead. Discarding is the destructive verb
// in this file and it may only touch what the relay has already stopped
// trying; a pending event deleted by a mistyped id is a promise broken with no
// trace at all.
const discardSQL = `
DELETE FROM event_outbox
WHERE dead_lettered_at IS NOT NULL AND id = ANY($1)`

// DeadLetters reports the events the relay has given up on.
//
// # Why this exists at all
//
// A dead letter nothing can read is not a dead letter, it is a silent drop
// with extra columns. This repository has already made that mistake once and
// named it: audit_log is a table that is written and never read. So giving up
// on an event is only half a feature, and this is the other half — the relay
// calls it on every pass, and a non-empty report is carried into the job's
// recorded outcome, which is what `gobit jobs` prints.
//
// The limit is clamped to at least one. A limit of zero would return no rows,
// and Count is computed from the returned set, so the report would say the
// pile is empty while it is full — the exact lie this reader exists to
// prevent.
func (s *Store) DeadLetters(ctx context.Context, limit int32) (DeadLetterReport, error) {
	if limit < 1 {
		limit = 1
	}

	var report DeadLetterReport

	rows, err := s.pool.Query(ctx, selectDeadLettersSQL, limit)
	if err != nil {
		return report, errors.Wrap(err, errors.KindInternal, CodeDeadLetterFailed,
			"the outbox dead letters could not be read")
	}
	defer rows.Close()

	for rows.Next() {
		var letter DeadLetter
		if err := rows.Scan(&letter.ID, &letter.Name, &letter.Attempts, &letter.LastError,
			&letter.CreatedAt, &letter.DeadLetteredAt, &report.Count); err != nil {
			return DeadLetterReport{}, errors.Wrap(err, errors.KindInternal, CodeDeadLetterFailed,
				"an outbox dead letter could not be read")
		}
		report.Oldest = append(report.Oldest, letter)
	}
	if err := rows.Err(); err != nil {
		return DeadLetterReport{}, errors.Wrap(err, errors.KindInternal, CodeDeadLetterFailed,
			"the outbox dead letters could not be walked")
	}

	return report, nil
}

// Redrive returns dead letters to the queue and reports how many were taken.
//
// It is the operator's answer to "the receiver is back". The events keep their
// ids, so a subscriber that already saw one — the direct publish is still the
// fast path (ADR 0023) — is protected by the same idempotency the bus's
// at-least-once contract already requires.
//
// A count smaller than the number of ids given is not an error and is worth
// reading: the ids that did nothing were either never dead-lettered or already
// redriven by somebody else on the same call.
func (s *Store) Redrive(ctx context.Context, ids ...string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	tag, err := s.pool.Exec(ctx, redriveSQL, ids)
	if err != nil {
		return 0, errors.Wrap(err, errors.KindInternal, CodeDeadLetterFailed,
			"the outbox dead letters could not be redriven")
	}

	return tag.RowsAffected(), nil
}

// Discard deletes dead letters and reports how many were removed.
//
// This is the only method in the package that destroys a promise, and it exists
// because the alternative is worse: without it the relay's report stays
// non-empty forever, the job stays failed forever, and an operator learns to
// ignore the one line that was built to be impossible to ignore. An alarm with
// no off switch is an alarm that gets muted.
//
// Like [Store.Redrive] it refuses anything that is not already dead.
func (s *Store) Discard(ctx context.Context, ids ...string) (int64, error) {
	if len(ids) == 0 {
		return 0, nil
	}

	tag, err := s.pool.Exec(ctx, discardSQL, ids)
	if err != nil {
		return 0, errors.Wrap(err, errors.KindInternal, CodeDeadLetterFailed,
			"the outbox dead letters could not be discarded")
	}

	return tag.RowsAffected(), nil
}
