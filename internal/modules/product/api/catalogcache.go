package api

import (
	"net/http"
	"strconv"
	"time"
)

// This file writes ONE header, and everything about it is a decision somebody
// else has to be able to reverse (ADR 0151).
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
// Because it is only true of three responses. A middleware would have to know
// which route it is on, which is the route table written a second time; and a
// middleware cannot see whether the handler is about to answer with a BODY or
// with an ERROR — and a 404 stored by a CDN for a minute is a product that stays
// missing after it is fixed.

// catalogCache is the freshness policy of the channel-scoped catalog reads.
//
// The zero value writes NO header, which is the default an installation that has
// never heard of this setting gets. The two fields are separate questions on
// purpose: how stale a body may be, and who may keep it.
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
// and `must-revalidate` are all defensible and none of them is decidable here: the
// first two say something about a CDN's behavior that only the operator who runs
// it can choose, and the third asks for a validator this endpoint does not have
// (there is no ETag on a catalog body, deliberately — see ADR 0151).
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

// allowCaching writes the header onto a response that is ABOUT to carry a body.
//
// It is called on the success path of a channel-scoped read and nowhere else. The
// position matters more than the value: called earlier, it would land on the
// refusals too — and a cached 404 is a product that stays missing for the length
// of the TTL after somebody fixes it.
func (h *Handler) allowCaching(w http.ResponseWriter) {
	if value := h.cache.header(); value != "" {
		w.Header().Set("Cache-Control", value)
	}
}
