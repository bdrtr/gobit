package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// codeCallbackContradiction fingerprints the one callback outcome that asks for
// a person.
//
// It is UNEXPORTED while every other code in this package is published, and the
// difference is which way the value travels. The three registration codes are
// RETURNED — a plugin author catches one at startup — so they are a Go
// contract. This one is never returned to anybody: it exists inside a log
// record, and what reads it is an error collector matching a string. Exporting
// it would publish a symbol no caller can receive (ADR 0026 keeps the surface a
// promise), and the string is held instead by the test that names it.
const codeCallbackContradiction = "callback_contradiction"

// guard runs one callback through every ring and then the handler.
//
// # The order, and why each step is where it is
//
// The quota is FIRST because a refused request must be almost free: after it
// come a body read and a signature hash, and the whole point of the quota is
// that an unauthenticated caller cannot make this endpoint do work.
//
// The signature check comes before anything is derived from the payload,
// because everything derived from an unverified payload is attacker-chosen —
// including the replay key, which is exactly the value an attacker would want
// to choose.
//
// The replay ring comes last, after the payload is known to be genuine, and it
// is the only ring that writes anything.
//
// # Every outcome leaves a line, refusals included
//
// The record of a callback is this log and not an audit row: a provider is not
// an actor, and the audit table has no column for the thing worth recording
// here, which is WHICH of the five answers went back (ADR 0056).
//
// It is not a ledger table either, and that is a decision rather than a
// postponement (ADR 0062). What the callback ASSERTED is durable in the table
// the receiving module already owns — a provider that reports back instead of
// being asked cannot answer "is the money held?" without one — and this log
// holds what no module can see, the requests a guard turned away before the
// handler ran. Of those, the ERROR lines are the ones that leave the process
// for a collector, so they carry an error value rather than only a sentence.
//
// What the log is evidence about is decided one step above, in
// [CallbackRegistry.lookup]: the population is every request that MATCHED a
// registered route, so a callback the quota threw away and one whose signature
// failed are both in it. Recording only what got past the guards would make the
// log evidence about the requests that passed — which is the one question a
// reader of it never has.
//
// [TestNoCallbackOutcomeIsSilent] holds the KNOWN outcomes — twelve of them,
// each driven to its end and required to leave a line naming the callback, and
// the four where the handler ran required to carry the status too.
//
// What it does NOT hold is a branch nobody has written yet. Measured
// 2026-09-08: a new terminal branch added at the top of this function, writing
// a status and returning without logging, leaves the whole package green. A
// census cannot enumerate an outcome that does not exist, so the twelve are a
// FLOOR and not a fence. Closing that would take a count of the terminal sites
// in this file, derived from the source rather than from the census, and it is
// not built.
func (g *CallbackRegistry) guard(
	w http.ResponseWriter, r *http.Request, rt *CallbackRoute, next http.Handler,
) {
	log := g.opts.Logger.With("callback", rt.Source, "path", rt.Path)

	if !g.allow(w, r, rt, log) {
		return
	}

	body, err := readAtMost(r, rt.MaxBodyBytes)
	if err != nil {
		log.WarnContext(r.Context(), "a callback body could not be read", "error", err)
		writeCallback(w, rt.Ack.Malformed)

		return
	}

	ctx, cancel := context.WithTimeout(r.Context(), rt.Timeout)
	defer cancel()
	r = r.WithContext(ctx)

	if err := rt.Verify(ctx, r, body); err != nil {
		// This is the message that says money moved or a parcel was delivered,
		// so it is the one worth forging. A genuine misconfiguration and an
		// attack look identical here; both are worth an ERROR line.
		log.ErrorContext(ctx, "a callback failed verification", "error", err)
		writeCallback(w, rt.Ack.Rejected)

		return
	}

	identity, content, err := rt.Key(r, body)
	if err != nil {
		log.ErrorContext(ctx, "a VERIFIED callback could not be keyed", "error", err)
		writeCallback(w, rt.Ack.Malformed)

		return
	}
	if len(identity) == 0 || g.opts.Store == nil {
		// Either this payload carries nothing to key on, or the installation has
		// no replay window. Both are stated conditions rather than faults, and
		// both mean the same thing: the handler runs without a record, which is
		// what every callback in this repository did before this ring existed.
		//
		// It is said out loud because it is the state in which the same event
		// can be applied twice, and the two ways into it — an unkeyable payload
		// and an installation with no store — are invisible from the outside:
		// the provider gets the ordinary answer either way.
		answered := &responseWriter{ResponseWriter: w, status: http.StatusOK}
		next.ServeHTTP(answered, r)
		log.InfoContext(ctx, "a callback was handled with no replay record",
			"status", answered.status)

		return
	}

	g.deduplicate(w, r, rt, next, callbackKeysOf(rt, identity, content), log)
}

// allow applies the quota, and reports whether the request may go on.
func (g *CallbackRegistry) allow(
	w http.ResponseWriter, r *http.Request, rt *CallbackRoute, log *slog.Logger,
) bool {
	if g.opts.Limiter == nil {
		return true
	}

	// The path is part of the key so one provider's flood cannot exhaust
	// another's budget. The SOURCE cannot be part of it: identity here is the
	// signature, and the signature is not checked yet.
	decision, err := g.opts.Limiter.Allow(r.Context(), rt.Path+"|"+g.opts.LimitKey(r))
	if err != nil {
		// Fail OPEN, the same direction [RateLimit] takes: dropping a provider's
		// callback because the limiter is unreachable loses the event, and the
		// event is the thing this endpoint exists for.
		log.WarnContext(r.Context(), "the callback quota could not be checked, letting it through",
			"error", err)

		return true
	}

	writeRateLimitHeaders(w, decision)
	if decision.Allowed {
		return true
	}

	// Answered as UNAVAILABLE rather than refused: a throttled callback has not
	// been processed, and the only answer that saves the event is the one that
	// makes the provider come back.
	log.WarnContext(r.Context(), "a callback was throttled", "retry_after", decision.RetryAfter)
	writeCallback(w, rt.Ack.Unavailable)

	return false
}

// callbackKeys is the store key and the fingerprint of one verified callback.
type callbackKeys struct {
	key         string
	fingerprint string
}

// deduplicate runs the handler at most once per event.
func (g *CallbackRegistry) deduplicate(
	w http.ResponseWriter, r *http.Request, rt *CallbackRoute, next http.Handler,
	keys callbackKeys, log *slog.Logger,
) {
	record, done, err := g.opts.Store.Begin(r.Context(), keys.key, keys.fingerprint)

	switch {
	case errors.Is(err, ErrIdempotencyKeyInFlight):
		// The same event is being processed right now. Answering "accepted"
		// would let the provider stop retrying while the first attempt may still
		// fail; answering "retry" costs one more call and loses nothing.
		log.InfoContext(r.Context(), "a callback arrived while the same event was in flight")
		writeCallback(w, rt.Ack.Unavailable)

		return
	case err != nil:
		// The replay window is unreachable. Processing anyway would risk
		// applying the same event twice, which on this surface means paying or
		// shipping twice; the provider retrying costs one call.
		log.ErrorContext(r.Context(), "the callback replay window is unreachable", "error", err)
		writeCallback(w, rt.Ack.Unavailable)

		return
	case done && record != nil && record.Fingerprint == keys.fingerprint:
		// A plain retry: the provider gets back the answer it missed.
		log.InfoContext(r.Context(), "a callback was replayed from the record")
		replayCallback(w, record)

		return
	case done:
		// The same event, asserting something DIFFERENT. This is a real signal,
		// not a client error, and it is acknowledged on purpose: refusing it
		// would make a provider that reads the body retry it forever.
		//
		// It is the one outcome on this surface whose own message says a person
		// has to act, so it is logged AS AN ERROR VALUE rather than as a
		// sentence: an error reporter fingerprints a record by the code of the
		// error it carries, and a record carrying none is filed under
		// "unclassified" — sharing one rate-limit bucket with every genuinely
		// unclassified failure in the process (ADR 0062).
		log.ErrorContext(r.Context(),
			"a callback contradicted an event already recorded; a human has to look",
			"error", coreerrors.Conflict(codeCallbackContradiction,
				"a callback asserted something other than the event already recorded"),
			"key", keys.key)
		writeCallback(w, rt.Ack.Duplicate)

		return
	}

	g.record(w, r, rt, next, keys, log)
}

// record runs the handler and stores what it answered.
func (g *CallbackRegistry) record(
	w http.ResponseWriter, r *http.Request, rt *CallbackRoute, next http.Handler,
	keys callbackKeys, log *slog.Logger,
) {
	recorder := &recordingWriter{ResponseWriter: w, status: http.StatusOK}

	// The store calls are detached from the request context on purpose: the
	// handler may have finished its work with the deadline already spent, and
	// dropping the record then would let the same event be processed again.
	settle := context.WithoutCancel(r.Context())

	release := func(ctx context.Context) {
		if err := g.opts.Store.Abort(ctx, keys.key); err != nil {
			log.ErrorContext(ctx, "the callback key could not be released", "error", err)
		}
	}

	defer func() {
		if recovered := recover(); recovered != nil {
			release(settle)
			panic(recovered)
		}
	}()

	next.ServeHTTP(recorder, r)

	// The one outcome this ring does not produce itself, and the reason it is
	// said at all: every OTHER outcome is a refusal, and a refusal writes a
	// line. Without this one the ring's log is evidence about the callbacks
	// that were turned away and about nothing else — a log with no line in it
	// would mean either "nothing arrived" or "everything succeeded", and no
	// reader could tell which. A failing handler is on this line too, at its
	// own status: "the ring refused it" and "the handler broke" are the two
	// answers a provider retry has to be told apart by.
	log.InfoContext(r.Context(), "a callback was handled", "status", recorder.status)

	if recorder.status >= http.StatusInternalServerError || recorder.overflowed {
		// A failure is not recorded, so the provider's retry gets a real attempt
		// rather than a replayed failure. An overflowing answer is not recorded
		// either: replaying half a body would hand the provider a broken ack.
		release(settle)

		return
	}

	if err := g.opts.Store.Complete(settle, keys.key,
		IdempotentResponse{
			Status:      recorder.status,
			Header:      recorder.Header().Clone(),
			Body:        recorder.buf.Bytes(),
			Fingerprint: keys.fingerprint,
		}); err != nil {
		log.ErrorContext(r.Context(),
			"the callback was processed but not recorded; a retry will process it AGAIN",
			"error", err)
		release(settle)
	}
}

// callbackKeysOf derives the store key and the fingerprint from the two tuples.
//
// Both are hashed, so neither depends on how long a provider's identifiers are,
// and both are built with a length-prefixed join: without it a provider whose
// identifier may contain the separator could make two different events produce
// one key, which is a way to make an event disappear.
func callbackKeysOf(rt *CallbackRoute, identity, content []string) callbackKeys {
	bucket := "callback:" + rt.Source

	return callbackKeys{
		key: storeKey(bucket, hashTuple(identity)),
		// The path and the source go into the fingerprint as well: the key is
		// already namespaced by them, and repeating them here means a record
		// written under a different route can never be mistaken for a match.
		fingerprint: hashTuple(append([]string{bucket, rt.Path}, content...)),
	}
}

// hashTuple joins the parts unambiguously and hashes the result.
func hashTuple(parts []string) string {
	sum := sha256.New()
	for _, part := range parts {
		_, _ = sum.Write([]byte(strconv.Itoa(len(part)) + ":" + part))
	}

	return hex.EncodeToString(sum.Sum(nil))
}
