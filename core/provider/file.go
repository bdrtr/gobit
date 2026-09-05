package provider

import (
	"context"
	"io"
)

// The content types an upload accepts by default; the allow-list consists of
// these and nothing else, and [UploadInput.ContentType] carries one of them.
//
// The constants are in the core because the value is written both by the side
// validating the upload and by the side serving the file, and the two CANNOT
// import each other (Principle 2.4).
//
// # Why an allow-list and not a deny-list
//
// A deny-list ACCEPTS by default EVERY type it does not list: a single shape
// nobody thought of today (a document, an archive, a script) enters the store
// silently, and whoever writes the list has to predict every new shape in
// advance. In an allow-list an unknown type is REJECTED; accepting a new type
// is a deliberate decision, not an omission that can be forgotten.
//
// # Why there is NO SVG
//
// An SVG looks like an image but it is a DOCUMENT: it can carry <script> and,
// served from the same origin, becomes stored XSS — the user who uploads the
// file runs code in the session of everyone who views it. Its absence from the
// list also happens on its own: [net/http.DetectContentType] returns "text/xml"
// or "text/plain" for an SVG, NOT "image/svg+xml". So the type detected from
// the content never falls into this list anyway; the constant's absence merely
// writes that fact down.
const (
	// ContentTypeJPEG is a JPEG image.
	ContentTypeJPEG = "image/jpeg"
	// ContentTypePNG is a PNG image.
	ContentTypePNG = "image/png"
	// ContentTypeGIF is a GIF image.
	ContentTypeGIF = "image/gif"
	// ContentTypeWebP is a WebP image.
	ContentTypeWebP = "image/webp"
)

// UploadInput is the input of a single file to be written to the store.
//
// # There is NO client file name
//
// The absence of a file name among the fields is deliberate. The store key is
// PRODUCED by the provider (an identity plus an extension derived from the
// detected type); a name coming from the client never becomes a path component
// at any stage, which makes writing outside the store with "../" STRUCTURALLY
// impossible.
//
// The alternative — taking the name and "sanitizing" it — would have meant
// making the same decision again for every new encoding trick (%2e%2e, a
// backslash, an embedded NUL, Unicode normalization), and its correctness would
// depend on how many tricks the sanitizer remembered that day. A field that
// does not exist has no trick to miss.
type UploadInput struct {
	// ContentType is the type detected from the file's CONTENT
	// ([net/http.DetectContentType]); the client's Content-Type header is NEVER
	// written here.
	//
	// The type the client declares is a CLAIM, not a fact: an HTML file sent as
	// "image/png" passes an allow-list that trusts it and, when served, runs as
	// HTML in the browser. A list that looks at the claim filters nothing.
	//
	// Leaving detection to the caller is deliberate too: the allow-list must be
	// applied before a SINGLE BYTE is written to the store. Had the provider
	// detected it, the check could only happen after the write had started, a
	// rejected file would need a delete call, and when that delete failed the
	// file would stay in the store.
	ContentType string
	// Body is the file's body and is read as a STREAM.
	//
	// As a []byte a 50 MB upload would mean 50 MB of memory; a few concurrent
	// uploads would bring the process down. The size limit is therefore
	// enforced with a [net/http.MaxBytesReader] wrapping the body, which also
	// makes it configurable: an unbounded body is the cheapest way to fill a
	// disk with a single request.
	//
	// The provider does NOT close Body; closing is the opener's job.
	Body io.Reader
}

// File is a file written to the store.
type File struct {
	// Key is the file's key in the store and is PRODUCED by the provider.
	// [FileProvider.Delete] takes this value.
	Key string
	// URL is the file's reachable address.
	//
	// A key and an address are DIFFERENT things: on S3 the key may be
	// "product/x.jpg" while the address is a signed URL, and the key cannot be
	// recovered from the signature. That is why deletion takes the key and not
	// the address; with a single field the delete path would have to parse the
	// address and would be rewritten for every provider.
	//
	// The caller stores the address DURABLY (it is written into the product
	// image record), so a provider returning a short-lived signature leaves a
	// field that rots silently in the database.
	URL string
	// ContentType is the stored type and is the same as
	// [UploadInput.ContentType]. When the file is served, the Content-Type
	// header is written FROM THIS.
	ContentType string
	// Size is the number of bytes written.
	Size int64
}

// FileProvider is the contract a file store offers the core (plan Section 5.6).
//
// # No idempotency is EXPECTED
//
// Unlike [PaymentProvider] there is no IdempotencyKey here. A repeated upload
// leaves A SECOND object; the cost is disk space, not a duplicate charge.
// Preventing the repeat with a key would require hashing the body to recognize
// the same content, and a hash is only known after ALL the bytes have been read
// — which would mean taking the body into memory or a temporary file, reversing
// the streaming decision.
//
// # Serving is NOT the provider's job, but the rule binds
//
// Whichever layer serves the file (the local disk provider may serve it itself,
// on S3 a CDN does), two rules hold: Content-Type is written from the STORED
// type, never from the one the client sent; and EVERY response carries
// X-Content-Type-Options: nosniff. Without the second the browser looks at the
// content and makes its own guess, so a file stored as "image/png" that looks
// like HTML may be executed as HTML — that is, the serving stage would undo
// detection and the allow-list even when both worked correctly.
type FileProvider interface {
	Provider

	// Upload writes the body to the store and returns the reachable address.
	//
	// The body is read IN FULL. The call BLOCKS and may go to an external
	// service; the caller must put a deadline on ctx.
	//
	// If the read fails midway — which is exactly how
	// [net/http.MaxBytesReader] cuts off a body exceeding the limit — the
	// provider MUST CLEAN UP the half-written object and return an error. A
	// half object is a file no record points at and no delete path knows the
	// key of: requests exceeding the limit could still fill the disk.
	Upload(ctx context.Context, in UploadInput) (File, error)

	// Delete removes the file from the store. It must be IDEMPOTENT: a key
	// that does not exist is NOT an error.
	//
	// Deletion is the cleanup step of the flow that removes the record
	// pointing at the file, and that flow can be retried. A second call
	// blowing up would make a file whose record is already gone impossible to
	// clean up — that is, it would make permanent exactly the garbage it is
	// supposed to remove.
	Delete(ctx context.Context, key string) error
}
