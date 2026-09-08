package adminui

import (
	"crypto/sha256"
	"embed"
	"encoding/hex"
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// assetFiles holds the panel's static assets and is EMBEDDED IN THE BINARY.
//
// Embedding is the same requirement the templates carry: the repository's
// delivery promise is "run the binary, it works", and a stylesheet read from
// disk is a second artifact that has to travel with it — plus a failure that
// only appears on the first page load, in front of an operator, when the
// working directory is not what someone assumed.
//
//go:embed assets/panel.css assets/reviews.js
var assetFiles embed.FS

// stylesheetFile is the embedded path of the panel's stylesheet.
const stylesheetFile = "assets/panel.css"

// stylesheetType is the content type the stylesheet is served with.
//
// It is written out rather than sniffed: [corehttp.WriteAsset] sends
// X-Content-Type-Options: nosniff, so a wrong or missing type here means the
// browser refuses the file instead of guessing — which is the correct trade for
// an admin surface and the reason the type is a constant.
const stylesheetType = "text/css; charset=utf-8"

// stylesheet is the file's bytes, read once at startup.
//
// Reading at init rather than per request is not an optimization: it means a
// missing asset fails the BUILD (the embed directive) rather than the request,
// and the ETag can be computed once from content that cannot change while the
// process lives.
var stylesheet = mustReadAsset(stylesheetFile)

// stylesheetETag is the content stamp the browser caches against.
//
// It is derived from the BYTES rather than from a version string: a release
// that changes the stylesheet gets a new stamp automatically, and one that does
// not keeps the old one, so an operator's browser refetches exactly when the
// file really changed. A hand-maintained version would drift the first time
// somebody edited the CSS without remembering to bump it.
var stylesheetETag = assetETag(stylesheet)

// serveStylesheet writes the panel's stylesheet.
//
// # Why this is the first call to WriteAsset
//
// [corehttp.WriteAsset] was written for the panel in ADR 0011 and had never
// been called — the panel had no CSS at all. A capability with no consumer is
// this repository's named second error class (ADR 0009); this is the consumer.
func (u *UI) serveStylesheet(w http.ResponseWriter, r *http.Request) {
	corehttp.WriteAsset(r.Context(), w, stylesheetType, stylesheetETag, stylesheet)
}

// reviewsScriptFile is the embedded path of the review screen's client.
const reviewsScriptFile = "assets/reviews.js"

// reviewsScriptType is the content type the script is served with.
//
// Written out for [stylesheetType]'s reason and with a sharper edge: WriteAsset
// sends X-Content-Type-Options: nosniff, so a wrong type here means the browser
// REFUSES to execute the file — and a review screen whose script did not run is
// an empty moderation queue, which is the one answer this feature must never
// give wrongly.
const reviewsScriptType = "text/javascript; charset=utf-8"

// The review screen's client and its content stamp; see [stylesheet].
var (
	reviewsScript     = mustReadAsset(reviewsScriptFile)
	reviewsScriptETag = assetETag(reviewsScript)
)

// serveReviewsScript writes the review screen's client.
func (u *UI) serveReviewsScript(w http.ResponseWriter, r *http.Request) {
	corehttp.WriteAsset(r.Context(), w, reviewsScriptType, reviewsScriptETag, reviewsScript)
}

// mustReadAsset reads an embedded asset or panics.
//
// It runs at package initialization, and its failure means the embed directive
// and the file have drifted apart — a build-time mistake, not a runtime
// condition.
func mustReadAsset(name string) []byte {
	body, err := assetFiles.ReadFile(name)
	if err != nil {
		panic(err)
	}

	return body
}

// assetETag stamps the content.
//
// The stamp is quoted because that is what the header's grammar requires; an
// unquoted value is silently ignored by some caches, which would turn the
// immutable cache header into a promise nothing acts on.
func assetETag(body []byte) string {
	sum := sha256.Sum256(body)

	return `"` + hex.EncodeToString(sum[:16]) + `"`
}
