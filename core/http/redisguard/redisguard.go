// Package redisguard provides the SHARED (Redis-backed) implementations of the
// rate limiter and the idempotency store.
//
// Their in-memory counterparts in core/http (MemoryLimiter,
// MemoryIdempotencyStore) are for single-instance installations and tests. In a
// horizontally scaled deployment both break, but NOT IN THE SAME WAY:
//
//   - The rate limit is MULTIPLIED by the instance count: because every
//     instance keeps its own counter, "60 per minute" is 180 across three
//     instances. That is a SPEED problem; the limit loosens but no request is
//   - The idempotency protection does not work between instances AT ALL: two
//     requests with the same key landing on different instances are handled
//     twice — two orders, two captures. That is not a speed problem but a
//     CORRECTNESS one, and it removes the very reason idempotency exists.
//
// The cure for both is a shared counter/store; this package builds it on Redis.
// The contracts do not change: [Limiter] satisfies corehttp.RateLimiter and
// [IdempotencyStore] satisfies corehttp.IdempotencyStore exactly, and the
// middlewares do not know which implementation is plugged in.
//
// # Failure behavior
//
// When Redis is unreachable the two types behave in OPPOSITE ways, and that is
// deliberate. The limiter returns an error and the middleware LETS THE REQUEST
// THROUGH (fail-open): the limit is not there for the product's correctness but
// against abuse, and Redis going down must not cut all traffic. The store also
// returns an error but the middleware WRITES it to the client (fail-closed):
// letting a request through while the record cannot be written means a repeat is
// handled a second time — passing at the moment the protection is off is the
// same as never installing it. The asymmetry is already coded on the corehttp
//
// # The key namespace
//
// A key starts with the namespace prefix given to the constructors:
//
//	<prefix>:rl:<limit key>          — the rate limit counter
//	<prefix>:idem:<idempotency key>  — the idempotency record
//
// The prefix is a CONSTRUCTOR PARAMETER, not a constant. There are two reasons
// and the second weighs far more than the first:
//
//   - It keeps the keys from mixing with other data sharing the same Redis
//     (a cache, a queue, sessions); operations can scan "<prefix>:idem:*".
//   - It separates two gobit INSTALLATIONS sharing the SAME Redis (staging and
//     production, or two stores in one cluster). With a fixed prefix those two
//     installations spend each other's rate limit quota — a speed problem — and
//     READ each other's idempotency records: one installation's response goes to
//     the other's client. The second is a correctness problem; requiring a
//     separate DB or instance would have handed the cure to the infrastructure,
//     and Redis Cluster supports no numbered DBs while a separate instance costs
//
// The prefix is checked by [validatePrefix]; a prefix that is empty or carries
// the separator is NOT accepted silently.
package redisguard

import (
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// The key sections; joined with the namespace prefix they form the full key.
const (
	// separator is the character that separates the key sections.
	//
	// In Redis it is a CONVENTION rather than a language rule: redis-cli and most
	// monitoring tools split keys on it to show them as a tree, and group their
	// memory reports by that tree.
	separator = ":"
	// rateLimitSection is the section name of the rate limit counters.
	rateLimitSection = "rl"
	// idempotencySection is the section name of the idempotency records.
	idempotencySection = "idem"
)

// validatePrefix checks the shape of the namespace prefix.
//
// Accepted: at least one character, and only ASCII letters, digits, '-', '_'
// and '.'. The list is this narrow not because these are the characters
// "thought to be safe" but because every refused one has a CONCRETE failure:
//
//   - An empty prefix makes the key ":rl:client". That means no namespace at
//     all, while the caller asked for one by passing the prefix PARAMETER.
//     Turning empty into a default would be worse still: two installations
//     opened with a missing configuration would land in the same namespace
//     again — the very failure being fixed would come back silently.
//   - The separator (':') opens a real CLASH. The prefix "a" writes
//     "a:idem:<K>"; the prefix "a:idem:x" writes "a:idem:x:idem:<K2>". A client
//     inventing K = "x:idem:<K2>" lands both on the SAME key — that is, one
//     installation's client can deliberately read the other's record.
//   - Glob characters ('*', '?', '[') break the "<prefix>:idem:*" scan the
//     package godoc describes: the operator's pattern either deletes too much or
//     finds nothing. Both are deciding with the wrong key in production.
//   - Whitespace and control characters are INVISIBLE. A single trailing space
//     leaking out of an environment file moves the installation into ANOTHER
//     namespace in a way nobody notices: every counter resets and the
//     idempotency records in flight are ignored in an instant.
//
// NON-ASCII letters are refused too. Redis keys are binary safe, so there is no
// technical obstacle; but the prefix is a string humans read and match in
// redis-cli output, and visually indistinguishable characters (Cyrillic 'а' next
// to Latin 'a') would make two namespaces look like ONE.
func validatePrefix(keyPrefix string) error {
	if keyPrefix == "" {
		return coreerrors.Invalid(CodeInvalidConfig, "the key namespace prefix cannot be empty")
	}

	if strings.ContainsFunc(keyPrefix, func(r rune) bool { return !isValidPrefixChar(r) }) {
		return coreerrors.Invalid(CodeInvalidConfig,
			"the key namespace prefix %q carries an invalid character; "+
				"only ASCII letters, digits, '-', '_' and '.' are accepted", keyPrefix)
	}

	return nil
}

// isValidPrefixChar reports whether the character may be used in a namespace prefix.
func isValidPrefixChar(r rune) bool {
	switch {
	case r >= 'a' && r <= 'z', r >= 'A' && r <= 'Z', r >= '0' && r <= '9':
		return true
	default:
		return r == '-' || r == '_' || r == '.'
	}
}

// The machine-readable error codes this package produces.
//
// They are SEPARATE from corehttp.CodeRateLimited: that code says "you went over
// the limit" and the client's right response is to wait; these say "I could not
// measure the limit" and there is nothing the client can do.
const (
	// CodeRateLimiterFailed reports that the rate limit counter could not be updated.
	CodeRateLimiterFailed = "rate_limiter_unavailable"
	// CodeIdempotencyStoreFailed reports that the idempotency store is unreachable.
	CodeIdempotencyStoreFailed = "idempotency_store_unavailable"
	// CodeInvalidConfig reports that the constructor was given an invalid setting.
	CodeInvalidConfig = "redisguard_invalid_config"
)
