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

// stylesheetStamp is the content stamp, and it goes in the ADDRESS as well as in
// the header.
//
// It is derived from the BYTES rather than from a version string: a release that
// changes the stylesheet gets a new stamp automatically, and one that does not
// keeps the old one. That sentence used to end "so an operator's browser
// refetches exactly when the file really changed", and the sentence was FALSE —
// the stamp went only into the ETag, the address never changed, and the cache
// header says `immutable`, which tells the browser not to ask. A stamped address
// is what makes the claim true (D94).
var stylesheetStamp = assetStamp(stylesheet)

// serveStylesheet writes the panel's stylesheet.
//
// # Why this is the first call to WriteAsset
//
// [corehttp.WriteAsset] was written for the panel in ADR 0011 and had never
// been called — the panel had no CSS at all. A capability with no consumer is
// this repository's named second error class (ADR 0009); this is the consumer.
func (u *UI) serveStylesheet(w http.ResponseWriter, r *http.Request) {
	u.writeAsset(r, w, StylesheetPath, stylesheetType, stylesheetStamp, stylesheet)
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
	reviewsScript      = mustReadAsset(reviewsScriptFile)
	reviewsScriptStamp = assetStamp(reviewsScript)
)

// serveReviewsScript writes the review screen's client.
func (u *UI) serveReviewsScript(w http.ResponseWriter, r *http.Request) {
	u.writeAsset(r, w, ReviewsScriptPath, reviewsScriptType, reviewsScriptStamp, reviewsScript)
}

// writeAsset serves one of the panel's assets, and decides from the SCOPE TABLE
// whether a shared cache may keep a copy.
//
// Derived rather than chosen per call site, and that is the whole reason this
// function exists: since ADR 0156 the review screen's script and every
// registered screen's script sit behind a privilege, and they were still being
// served `Cache-Control: public` — an invitation to a proxy to hand them to a
// caller this panel had just refused (D95). Reading the answer out of the same
// table that installs the refusal means the two cannot disagree, and a path that
// gains a privilege stops being publicly cacheable in the same edit.
func (u *UI) writeAsset(
	r *http.Request, w http.ResponseWriter, path, contentType, stamp string, body []byte,
) {
	etag := etagOf(stamp)
	if u.scopes[path] == "" {
		corehttp.WriteAsset(r.Context(), w, contentType, etag, body)

		return
	}

	corehttp.WritePrivateAsset(r.Context(), w, contentType, etag, body)
}

// assetURL is the address an asset is requested at: its path plus its stamp.
//
// The stamp travels in the query rather than in the path so the route stays one
// pattern — chi never sees the query — while the ADDRESS changes whenever the
// bytes do, which is what the `immutable` cache header needs to be honest.
func assetURL(path, stamp string) string { return path + "?v=" + stamp }

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

// assetStamp stamps the content.
//
// It returns the BARE hex because the stamp has two readers with different
// grammars: an address takes it as it is, and the ETag header requires it
// quoted. One derivation, two spellings — see [etagOf].
func assetStamp(body []byte) string {
	sum := sha256.Sum256(body)

	return hex.EncodeToString(sum[:16])
}

// etagOf quotes a stamp for the header.
//
// The quoting is required by the header's grammar; an unquoted value is silently
// ignored by some caches, which would turn the cache header into a promise
// nothing acts on.
func etagOf(stamp string) string { return `"` + stamp + `"` }
