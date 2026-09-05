package http

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"errors"
	"log/slog"
	"net/http"
	"strconv"
)

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
		next.ServeHTTP(w, r)

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
		log.ErrorContext(r.Context(),
			"a callback contradicted an event already recorded; a human has to look",
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
