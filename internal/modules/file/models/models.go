package models

import "time"

// MaxOriginalNameLen is the accepted maximum length (in characters) of the file
// name the client declares.
//
// 255 is the file name limit of the common file systems; a longer name could
// not have stood on the client's own disk either. The bound DOES NOT TRIM, IT
// REJECTS: trimming would be silently changing the data the client sent, and
// the field's only job is anyway "to show what the user saw".
const MaxOriginalNameLen = 255

// Upload is the record of a single file written into the store.
//
// # THE CLIENT'S FILE NAME IS NOT A PATH
//
// [Upload.OriginalName] is stored but it locates nothing in the store; the
// file's place is [Upload.StorageKey] and the PROVIDER produces it. The
// separation is structural: the "../" a name carries, or any encoding of it,
// cannot turn into a path component at any stage, because the name enters no
// path expression.
//
// The reason the name is stored nonetheless is the admin interface: the person
// reviewing the uploads in the panel cannot tell a generated key apart from
// "product-red-front.jpg". The name is therefore DISPLAY data — it appears in
// the JSON body of the response, and is written into NO HTTP HEADER (e.g.
// Content-Disposition): writing it into a header would be putting a string
// whose content is not trusted inside the grammar of a header.
type Upload struct {
	// ID is the record's identifier.
	ID string
	// StorageKey is the file's key in the store and it is PRODUCED by the
	// provider. Deleting and reading are done with this value.
	StorageKey string
	// ProviderID is the identifier of the provider that wrote the file.
	//
	// It is stored because the configuration changes: on the day the
	// installation moves to an object store the old records still sit on the
	// local disk, and the only thing able to read them is the provider used on
	// that day.
	ProviderID string
	// ContentType is the type detected FROM THE CONTENT of the file; the type
	// the client declared is never written in here.
	//
	// While the file is being served, the Content-Type header is written from
	// THIS field.
	ContentType string
	// Size is the size of the file in bytes.
	Size int64
	// Checksum is the SHA-256 digest of the content (lowercase hexadecimal).
	//
	// It is computed during the upload and it is for diagnosis: the question
	// "is the file on disk the same thing as what we recorded" has no other
	// answer. It IS NOT USED for idempotency — the digest is known only after
	// all the bytes have been read, so looking at it in order to prevent a
	// repeat would have meant giving up on processing the body as a stream (see
	// the FileProvider contract in the core).
	Checksum string
	// OriginalName is the file name the client declared; it may be empty.
	// It is NEVER used as a path (see the type's documentation).
	OriginalName string
	// URL is the reachable address of the file.
	//
	// On the local provider it is RELATIVE TO THE ROOT ("/files/…"): writing
	// the installation's domain name into the record would invalidate every row
	// on the day the domain name changed. A storefront served from a different
	// origin puts its own origin in front of the address.
	URL string
	// UploadedBy is the identifier of the caller who did the upload. It is free
	// text, NOT a foreign key (Principle 2.2): the user is owned by the auth
	// module.
	UploadedBy string
	// CreatedAt is the moment the record was opened.
	CreatedAt time.Time
	// UpdatedAt is the moment the record last changed.
	UpdatedAt time.Time
}

// UploadFilter holds the pagination parameters of the upload listing.
//
// There IS NO filter field and that is deliberate: the list is an admin
// inventory and there is no flow reading it filtered today. A filter with no
// consumer would be a field that enters the query, the documentation and the
// test alike while answering no question; and a field that has entered the
// contract can never be taken out again.
type UploadFilter struct {
	// Limit is the maximum number of rows to return.
	Limit int64
	// Offset is the number of rows to skip.
	Offset int64
}
