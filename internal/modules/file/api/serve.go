package api

import (
	"net/http"
	"strings"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// cacheControl is the Cache-Control value of the served files.
//
// # Why NOT "immutable"
//
// The key is never reused (80 bits of randomness), so the content of an address
// DOES NOT CHANGE — up to here "immutable" looks right. But even though the
// content does not change it CAN BE DELETED, and that is exactly where the
// difference shows up: this endpoint is identity-less, therefore SHARED caches
// (a CDN, a reverse proxy) legitimately store the response. "immutable" tells
// them to stop revalidating; the result is that the same address keeps being
// served for a YEAR after DELETE /admin/v1/uploads/{id} was called. The delete
// works at the origin but does not take the access back.
//
// One hour balances the two demands: image traffic is still served from the
// cache and a delete decision reaches everywhere within an hour at the latest.
// An installation that wants a longer period has to pair the delete with a
// cache purge — then extending the period is safe too.
const cacheControl = "public, max-age=3600"

// serveFile is the GET /files/{key} handler.
//
// # Two rules hold on every response
//
//  1. The Content-Type is written from the STORED type — NOT from the type the
//     client declared during the upload. The stored type was detected from the
//     CONTENT of the file at upload time; the client's claim is stored nowhere,
//     so that it cannot leak in here.
//  2. X-Content-Type-Options: nosniff is present on EVERY response — the error
//     responses included, which is why the header is written on the very first
//     line. Without this header the browser looks at the content in spite of
//     the type we sent and makes its own guess: a file stored as "image/png"
//     that looks like HTML could be executed as HTML. That is, even if the
//     detection and the allow list work correctly, the serving stage would
//     void them.
//
// # Content-Disposition IS NOT WRITTEN
//
// The file name the client declared stands in the record but is NOT PUT INTO A
// HEADER: putting a string whose content is not trusted inside the header
// grammar opens a separate class of hole. The name comes back in the JSON body
// and its encoding is safe there.
//
// # The body of the response is written by net/http
//
// [net/http.ServeContent] serves conditional requests (If-Modified-Since) and
// range (Range) requests. Writing io.Copy by hand would be losing both of them
// — reloading a large image or downloading it partially is ordinary browser
// behavior. The file name is passed EMPTY: ServeContent uses the name only to
// GUESS the Content-Type and we have already written it from the record; had
// the name been given, the guessing path would have stayed open.
func (h *Handler) serveFile(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	w.Header().Set(headerContentTypeOptions, nosniff)

	opened, err := h.svc.OpenByKey(ctx, chi.URLParam(r, paramKey))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	defer func() { _ = opened.Content.Close() }()

	w.Header().Set("Content-Type", opened.Upload.ContentType)
	w.Header().Set("Cache-Control", cacheControl)

	// A MULTI-RANGE Range is refused; the header is deleted and we fall back to
	// the full body.
	//
	// [net/http.ServeContent] serves a multi-range request with
	// multipart/byteranges and only prevents the TOTAL BYTES of the ranges from
	// exceeding the file size — it does not bound the NUMBER of ranges. Since
	// every range carries its own boundary string and header block, a client
	// asking for hundreds of one-byte ranges makes the response many times
	// larger than the body. This endpoint is IDENTITY-LESS (the <img> on the
	// storefront cannot send a header), so the amplification turns directly
	// into a bandwidth attack.
	//
	// A single-range Range IS PRESERVED: that is the form browsers and video
	// players really use. Deleting the header instead of returning 416 is
	// deliberate — the client continues without an error and with the full
	// content, it only loses the range optimisation.
	if strings.Contains(r.Header.Get("Range"), ",") {
		r.Header.Del("Range")
	}

	http.ServeContent(w, r, "", opened.ModTime, opened.Content)
}
