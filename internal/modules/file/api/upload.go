package api

import (
	"bytes"
	"context"
	"errors"
	"io"
	"mime"
	"mime/multipart"
	"net/http"

	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	corehttp "github.com/bdrtr/gobit/internal/core/http"
	"github.com/bdrtr/gobit/internal/modules/file/service"
)

// sniffSize is the number of bytes read for the content type detection.
//
// 512 is the maximum length [net/http.DetectContentType] looks at; more adds
// nothing, less would miss some signatures.
const sniffSize = 512

// envelopeAllowance is the share the multipart envelope (the boundaries and the
// part headers) adds on top of the body.
//
// The size limit is put on the FILE but [net/http.MaxBytesReader] counts the
// WHOLE request. Had no allowance been left, a file exactly at the limit would
// be rejected purely because of its envelope and the error would say "your file
// is too large" — while the file was exactly the limit itself. The allowance is
// not unbounded either: the real size of the file is separately and EXACTLY
// enforced by the counter in the service (see the size-limited reader there),
// so the slack here is granted to the envelope only.
const envelopeAllowance int64 = 8 << 10 // 8 KiB

// createUpload is the POST /admin/v1/uploads handler.
//
// # The body is NOT JSON but multipart/form-data
//
// This is the only path in the repository where ARBITRARY BYTES are accepted
// from the client, and that is why every step of the flow is written down
// explicitly:
//
//  1. The body is WRAPPED with [net/http.MaxBytesReader]. An unbounded body is
//     the cheapest way to fill the disk (and the memory) with a single request.
//  2. The parsing is done by STREAMING ([net/http.Request.MultipartReader]),
//     not with the form parser. r.ParseMultipartForm writes the parts it cannot
//     hold in memory into TEMPORARY FILES — that is, it lands bytes that have
//     passed no validation yet onto the disk. Exactly the thing we are trying
//     to avoid.
//  3. The first 512 bytes are read and the content type is detected FROM THE
//     CONTENT. The client's Content-Type header is a CLAIM, not a fact: an HTML
//     file sent as "image/png" passes an allow list that trusts it and runs in
//     the browser when it is served.
//  4. The leading bytes that were read are put back in front of the stream with
//     [io.MultiReader]; otherwise the first 512 bytes of the file would be lost.
//  5. The allow list is applied in the service layer, before A SINGLE BYTE is
//     written to the storage.
func (h *Handler) createUpload(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	// The limit is read from the SAME source as the service's limit; writing it
	// separately in two places would mean the two silently drifting apart.
	r.Body = http.MaxBytesReader(w, r.Body, h.svc.MaxUploadBytes()+envelopeAllowance)

	parts, err := r.MultipartReader()
	if err != nil {
		corehttp.WriteError(ctx, w, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the request body has to be multipart/form-data and has to carry the %q field", fieldFile))

		return
	}

	file, err := readFilePart(parts)
	if err != nil {
		corehttp.WriteError(ctx, w, sizeError(err))

		return
	}

	head, err := readHead(file)
	if err != nil {
		corehttp.WriteError(ctx, w, sizeError(err))

		return
	}

	record, err := h.svc.Upload(ctx, service.UploadInput{
		ContentType: contentTypeOf(head),
		Body:        io.MultiReader(bytes.NewReader(head), file),
		// [mime/multipart.Part.FileName] already runs the name through
		// [path/filepath.Base] as RFC 7578 §4.2 requires, so
		// "../../etc/passwd" arrives here as "passwd". That IS NOT OUR
		// PROTECTION and it is not relied upon: our protection is that the
		// name never enters any path expression at all. The distinction
		// matters — a design that rests on the stdlib's behavior collapses
		// silently on the first change that takes the client's name from
		// somewhere else (e.g. from a JSON field).
		OriginalName: file.FileName(),
		UploadedBy:   callerID(r),
	})
	if err != nil {
		corehttp.WriteError(ctx, w, sizeError(err))

		return
	}

	// An extra part ROLLS BACK the uploaded file.
	//
	// Ignoring it silently would mean that the second file the client thinks it
	// sent went nowhere, and that would only be noticed when somebody went
	// looking for "where is my second image". The check is done AFTER the
	// upload because in a multipart stream the existence of the next part is
	// only known once the previous one has been read in full.
	if err := h.rejectExtraPart(r, parts, record.ID); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	writeItem(w, r, http.StatusCreated, toUploadDTO(record))
}

// readFilePart finds the file part in the multipart stream.
//
// An unexpected field name is REJECTED, not skipped. Skipping it silently would
// give a client that misspelled the name an even more confusing error than
// "there is no file field"; on top of that this is the only field this module
// reads anyway, and accepting a field that is not read would be a promise that
// does not work.
func readFilePart(parts *multipart.Reader) (*multipart.Part, error) {
	part, err := parts.NextPart()
	if err != nil {
		if errors.Is(err, io.EOF) {
			return nil, coreerrors.Invalid(codeInvalidRequest,
				"the request body has no %q field", fieldFile)
		}

		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the multipart body could not be parsed")
	}

	if part.FormName() != fieldFile {
		return nil, coreerrors.Invalid(codeInvalidRequest,
			"unexpected field %q; only the %q field is read", part.FormName(), fieldFile)
	}

	return part, nil
}

// rejectExtraPart verifies that there is no further part after the file part;
// if there is, it deletes the uploaded record and returns an error.
func (h *Handler) rejectExtraPart(r *http.Request, parts *multipart.Reader, uploadID string) error {
	_, err := parts.NextPart()
	if errors.Is(err, io.EOF) {
		return nil
	}

	// The rollback has to be attempted even when the request's context has been
	// canceled; otherwise every rejected request would leave a file behind in
	// the storage.
	if delErr := h.svc.DeleteUpload(context.WithoutCancel(r.Context()), uploadID); delErr != nil {
		corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
			"the rejected upload could not be rolled back",
			"error", delErr,
			"upload_id", uploadID,
			"consequence", "a file whose record was not deleted stayed in the storage; it has to be cleaned up by hand")
	}

	if err != nil {
		return sizeError(coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the multipart body could not be parsed"))
	}

	return coreerrors.Invalid(codeInvalidRequest,
		"the request body has to carry a single %q field", fieldFile)
}

// readHead reads the head of the body for the content type detection.
//
// If the file is smaller than 512 bytes a short read IS NOT AN ERROR; the
// detection is done with the bytes at hand. Reading no bytes at all is an error
// though: a zero-byte upload has neither a detectable type nor any content to
// serve.
func readHead(r io.Reader) ([]byte, error) {
	head := make([]byte, sniffSize)

	n, err := io.ReadFull(r, head)
	if err != nil && !errors.Is(err, io.EOF) && !errors.Is(err, io.ErrUnexpectedEOF) {
		return nil, coreerrors.Wrap(err, coreerrors.KindInvalid, codeInvalidRequest,
			"the file could not be read")
	}

	if n == 0 {
		return nil, coreerrors.Invalid(codeInvalidRequest, "the file cannot be empty")
	}

	return head[:n], nil
}

// contentTypeOf detects the content type from the leading bytes.
//
// The parameters (e.g. "; charset=utf-8") are DROPPED:
// [net/http.DetectContentType] appends a character set to the text types and
// the raw string would never match the bare types in the allow list such as
// "image/png". If the parsing fails the raw value is returned as it is and it
// cannot pass the allow list — the right answer for an unrecognized type is to
// reject it anyway.
func contentTypeOf(head []byte) string {
	raw := http.DetectContentType(head)

	mediaType, _, err := mime.ParseMediaType(raw)
	if err != nil {
		return raw
	}

	return mediaType
}

// sizeError turns the errors of an exceeded body limit into a typed client
// error.
//
// The error of [net/http.MaxBytesReader] can be anywhere in the chain: the
// multipart parser or the provider returns it wrapped. That is why it is looked
// for by its type.
//
// # Why 422 and not 413
//
// The handler DOES NOT CHOOSE the status code (plan Section 2.7): the code is
// derived from the class of the error, and the core's set of classes has no
// counterpart for 413. Breaking that rule for a single endpoint would break the
// principle that the error classification stands in one place — and what the
// client is really going to branch on is not the status but the
// machine-readable code ([service.CodeTooLarge]).
func sizeError(err error) error {
	var tooLarge *http.MaxBytesError
	if errors.As(err, &tooLarge) {
		return coreerrors.Wrap(err, coreerrors.KindInvalid, service.CodeTooLarge,
			"the request body can be at most %d bytes", tooLarge.Limit)
	}

	return err
}

// callerID returns the identity of the caller making the request; the empty
// string when there is none.
//
// The identity is FREE TEXT in the ledger, not a foreign key (Principle 2.2):
// binding to the auth module's table would break the module isolation. It can
// also stay empty — since this endpoint is protected that does not happen in
// the normal flow, but in an embedded use the handler can be called directly
// and in that case saying "who uploaded it is unknown" is better than making
// something up.
func callerID(r *http.Request) string {
	principal, ok := corehttp.PrincipalFromContext(r.Context())
	if !ok {
		return ""
	}

	return principal.ID
}
