package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// This file is the file module's CROSS-MODULE surface (ADR 0001, ADR 0006).
//
// Other modules, the workflows and the plugins CANNOT import this module. The
// solution is the same as the interop.go of every other module: publish a
// surface that speaks only in PRIMITIVE and stdlib types. The consumer declares
// its own narrow interface, this type satisfies it STRUCTURALLY, and it is
// resolved from the container under the name "file.interop".
//
// # Why it exists NOW, when the module said it deliberately did not have one
//
// The module's package doc used to argue that no other module reads the upload,
// and that the only thing a reader could want — the ADDRESS — already sits in
// the product image record. The first half was true and has stopped being true;
// the second half was wrong, and the product image binding is what exposed it.
// An address SHOWS a file and states nothing else about it: not the content type
// the system detected (which is not the one the client declared), not the size,
// not the checksum, not the provider holding the bytes. The record is the only
// place those live, and until this surface existed nothing outside this module
// could reach it.
//
// The surface is DELIBERATELY narrow: one read, by id. Uploading, listing and
// deleting stay on the module's own admin endpoints — a plugin that could delete
// an upload through this name would be able to break a product page without ever
// touching the catalog. Every method added here raises the cost of extracting
// file into a separate service.

// CodeInteropEncodeFailed reports that the upload record could not be encoded.
const CodeInteropEncodeFailed = "file_interop_encode_failed"

// uploadRecord is what a caller holding an upload id needs to know about the
// file without asking this module for the bytes.
//
// # THERE IS NO STORAGE KEY FIELD
//
// The record has one and it is not published here, for the reason the HTTP DTO
// gives: the address is the one contract for reaching the file, and a key
// alongside it would promise the same thing twice — with a signing object store
// the two have nothing to do with each other. A key is also this module's
// internal handle on the provider; a caller that held one could ask a provider
// for a file whose record it never read.
//
// # THERE IS NO uploaded_by FIELD
//
// It is the auth module's identity, kept here as free text (Principle 2.2), and
// no reader of "which file is behind this image" needs to know who uploaded it.
// Copying an identity into a second cross-module contract is how an identifier
// ends up load-bearing in a place that cannot validate it; the admin listing,
// which does have a reason to show it, reads it from this module's own endpoint.
type uploadRecord struct {
	// ID is the record's identifier — the value the caller already held; it is
	// echoed so that a decoded record is self-describing.
	ID string `json:"id"`
	// URL is the reachable address of the file. On the local provider it is
	// RELATIVE TO THE ROOT ("/files/…"); a consumer served from another origin
	// puts its own origin in front of it.
	URL string `json:"url"`
	// ContentType is the type detected FROM THE CONTENT, not the one the client
	// declared while uploading. A consumer deciding whether it can process the
	// file must branch on THIS value.
	ContentType string `json:"content_type"`
	// Size is the size of the file in bytes.
	Size int64 `json:"size"`
	// Checksum is the SHA-256 digest of the content (lowercase hexadecimal).
	Checksum string `json:"checksum"`
	// ProviderID is the identifier of the provider storing the file.
	ProviderID string `json:"provider_id"`
	// OriginalName is the file name the client declared; absent when empty.
	// It is DISPLAY data and locates nothing (see [models.Upload]).
	OriginalName string `json:"original_name,omitempty"`
	// CreatedAt is the moment the upload was made (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
}

// Interop turns the file service into the PRIMITIVE cross-module surface.
//
// It makes no decisions: it only translates the signature and the JSON schema.
// Every rule stays on [Service]; a rule added here would be a second copy that
// drifts from the first.
//
// It is registered in the container under the name "file.interop".
type Interop struct {
	svc *Service
}

// NewInterop builds the cross-module surface over the given service.
func NewInterop(svc *Service) *Interop { return &Interop{svc: svc} }

// UploadJSON returns the upload record of the given id.
//
// The schema is written out in the [uploadRecord] doc. An id that belongs to no
// record returns errors.NotFound and an empty id errors.Invalid; the two are
// kept apart because the caller can fix one and only report the other.
//
// The counterpart on the consumer side:
//
//	type UploadReader interface {
//	    UploadJSON(ctx context.Context, uploadID string) (json.RawMessage, error)
//	}
//
// The consumer CANNOT import this package, so nothing but an integration test
// can prove the two signatures agree — the compiler never sees them together.
func (i *Interop) UploadJSON(ctx context.Context, uploadID string) (json.RawMessage, error) {
	record, err := i.svc.GetUpload(ctx, uploadID)
	if err != nil {
		return nil, err
	}

	body, err := json.Marshal(toUploadRecord(record))
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeInteropEncodeFailed,
			"the upload record could not be encoded: %s", record.ID)
	}

	return body, nil
}

// toUploadRecord converts the domain record into the cross-module schema.
func toUploadRecord(u models.Upload) uploadRecord {
	return uploadRecord{
		ID:           u.ID,
		URL:          u.URL,
		ContentType:  u.ContentType,
		Size:         u.Size,
		Checksum:     u.Checksum,
		ProviderID:   u.ProviderID,
		OriginalName: u.OriginalName,
		CreatedAt:    u.CreatedAt,
	}
}
