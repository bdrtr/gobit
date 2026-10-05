package api

import (
	"net/http"
	"strconv"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// This file writes ONE header, and everything about it is a decision somebody
// else has to be able to reverse (ADR 0151); and it writes the validator that
// rides with it (ADR 0391).
//
// # What ADR 0044 left, and what decides it
//
// That record moved the sales channel into the catalog PATH so that two keys
// authorized for one channel receive byte-identical bodies — "which is what a
// shared cache can store" — and then deliberately did not turn a cache on. The
// fact it measured is what decides the policy: the catalog body can change
// WITHOUT a write, because a price list window opens against the clock. A purge
// on write is therefore never complete, and a TTL is the only instrument that
// can be.
//
// # Why the header is written per handler and not by a middleware
//
// Because it is only true of the channel-scoped responses. A middleware would
// have to know which route it is on, which is the route table written a second
// time; and a middleware cannot see whether the handler is about to answer with
// a BODY or with an ERROR — and a 404 stored by a CDN for a minute is a product
// that stays missing after it is fixed.
//
// # The validator rides with it (ADR 0391)
//
// Every response that carries the policy carries an ETag of its own bytes,
// written by the one helper below, so a read cannot have the policy without the
// tag. The converse does not hold: the tag is written at every TTL, zero
// included, and on the unscoped reads that never carry the policy.
// The tag is a hash and not a version because the body moves with the clock and
// with writes in pricing and inventory that bump nothing this module can see.

// catalogCache is the freshness policy of the channel-scoped catalog reads.
//
// The zero value writes no Cache-Control, which is the default an installation
// that has never heard of this setting gets. The two fields are separate
// questions on purpose: how stale a body may be, and who may keep it.
type catalogCache struct {
	// ttl is how long a stored answer may be reused. Zero switches the header off.
	ttl time.Duration
	// shared says whether a cache outside the shopper's own client may store it.
	//
	// False writes `private`, which no CDN or reverse proxy may store. True writes
	// `public`, and what that costs is stated where the setting lives: after
	// ADR 0044 the publishable key is a GATE with no influence on the body, so a
	// shared copy is served to callers that present no key at all.
	shared bool
}

// header returns the Cache-Control value, or "" when caching is off.
//
// The value carries max-age and nothing else. `s-maxage`, `stale-while-revalidate`
// and `must-revalidate` say what a cache may do with a copy that is shared or
// stale, which only the operator who runs it can choose; `no-cache` would void
// the TTL this value states. The validator is the body's own ETag (ADR 0391, see
// [Handler.writeCatalog]).
func (c catalogCache) header() string {
	if c.ttl <= 0 {
		return ""
	}

	scope := "private"
	if c.shared {
		scope = "public"
	}

	return scope + ", max-age=" + strconv.FormatInt(int64(c.ttl.Seconds()), 10)
}

// writeCatalog answers a channel-scoped read that succeeded: the freshness
// policy, then the body with its validator, or a 304 when the request names
// these bytes' tag or sends `*` (ADR 0151, ADR 0391).
//
// It is called after the read; called earlier, the policy would land on
// refusals and a 304 would be answered before the key's channel or the
// product's visibility was asked — and a cached 404 is a product that stays
// missing for the length of the TTL after somebody fixes it.
func (h *Handler) writeCatalog(w http.ResponseWriter, r *http.Request, body any) {
	if value := h.cache.header(); value != "" {
		w.Header().Set("Cache-Control", value)
	}

	corehttp.WriteJSONWithValidator(w, r, body)
}
