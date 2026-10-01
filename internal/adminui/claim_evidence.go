package adminui

import (
	"bytes"
	"context"
	"encoding/json"
	stderrors "errors"
	"io"
	"mime"
	"net/http"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// A claim's evidence on the order's page (ADR 0325): the files an operator
// attaches to a damage or a shortage, stored by the file module under
// file:write and bound to the claim by the order module under order:write.

// ServiceFileAdmin is the file module's panel surface, spelled by hand and
// pinned against the module's constant in internal/arch.
const ServiceFileAdmin = "file.admin"

// OrderClaimEvidencePath attaches a file to a claim of the order, and
// OrderClaimEvidenceDetachPath removes one piece of a claim's evidence.
const (
	OrderClaimEvidencePath       = OrderPath + "/claims/{claim}/evidence"
	OrderClaimEvidenceDetachPath = OrderClaimEvidencePath + "/{evidence}/detach"
)

// The file module's privileges, as its admin API names them.
const (
	scopeFileRead  = "file:read"
	scopeFileWrite = "file:write"
)

// The evidence form's fields.
const (
	formEvidenceFile    = "file"
	formEvidenceCaption = "caption"
)

// evidenceEnvelope is what the form carries beside the file: the caption and
// the multipart framing, allowed on top of the file module's bound.
const evidenceEnvelope = 64 << 10

// captionBytes is as much of the caption as is read.
const captionBytes = 2048

// sniffBytes is what the content type is detected from, the size
// [net/http.DetectContentType] reads.
const sniffBytes = 512

// EvidenceKeeper is the narrow surface a claim's evidence is read and bound
// through: the order module's.
type EvidenceKeeper interface {
	// ClaimEvidenceJSON lists the claim's evidence, oldest first.
	ClaimEvidenceJSON(ctx context.Context, claimID string) (json.RawMessage, error)
	// AttachClaimEvidence binds an upload to the claim and returns the
	// evidence's id.
	AttachClaimEvidence(ctx context.Context, claimID, uploadID, caption string) (string, error)
	// DetachClaimEvidence removes a piece of evidence from its claim.
	DetachClaimEvidence(ctx context.Context, evidenceID string) error
}

// FileUploader is the narrow surface a file is stored through: the file
// module's.
type FileUploader interface {
	// MaxUploadBytes is the size an upload is held to.
	MaxUploadBytes() int64
	// UploadFile stores the body and returns its id and address.
	UploadFile(ctx context.Context, contentType, originalName, uploadedBy string, body io.Reader) (id, url string, err error)
	// UploadURL is the address an upload is served at.
	UploadURL(ctx context.Context, id string) (string, error)
	// DeleteUpload removes an upload.
	DeleteUpload(ctx context.Context, id string) error
}

// evidenceRow is one piece of evidence as the order module's surface sends
// it; the json tags are the contract with that surface, exercised end to end.
type evidenceRow struct {
	ID        string    `json:"id"`
	UploadID  string    `json:"upload_id"`
	Caption   string    `json:"caption"`
	CreatedAt time.Time `json:"created_at"`
	// URL is where the file is served, read for an operator who may read the
	// files; empty otherwise.
	URL string `json:"-"`
}

// claimEvidence reads one claim's evidence, with each file's address for an
// operator who may read the files; unread says the read failed, which leaves
// the claim on the page.
func (u *UI) claimEvidence(ctx context.Context, keeper EvidenceKeeper, claimID string, withURLs bool) ([]evidenceRow, bool) {
	raw, err := keeper.ClaimEvidenceJSON(ctx, claimID)
	var rows []evidenceRow
	if err == nil {
		err = json.Unmarshal(raw, &rows)
	}
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read a claim's evidence", "error", err, "claim_id", claimID)

		return nil, true
	}
	if withURLs && u.files != nil {
		for i := range rows {
			if url, urlErr := u.files.UploadURL(ctx, rows[i].UploadID); urlErr == nil {
				rows[i].URL = url
			}
		}
	}

	return rows, false
}

// withClaimEvidence reads the evidence of the claims among the records, for
// an operator who may read the order; the files' addresses only for one who
// may read the files too.
func (u *UI) withClaimEvidence(r *http.Request, records []orderAfterSale) {
	keeper, ok := u.afterSales.(EvidenceKeeper)
	if !ok {
		return
	}
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	withURLs := principal.HasScope(scopeFileRead)
	for i := range records {
		if records[i].Kind == kindClaim {
			records[i].Evidence, records[i].EvidenceUnread = u.claimEvidence(r.Context(), keeper, records[i].ID, withURLs)
		}
	}
}

// canAttachEvidence reports whether the operator may attach a file here: the
// order's write binds it and the files' write stores it.
func (u *UI) canAttachEvidence(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.afterSales.(EvidenceKeeper)

	return ok && u.files != nil && principal.HasScope(scopeOrderWrite) && principal.HasScope(scopeFileWrite)
}

// canDetachEvidence reports whether the operator may remove evidence here.
func (u *UI) canDetachEvidence(r *http.Request) bool {
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	_, ok := u.afterSales.(EvidenceKeeper)

	return ok && principal.HasScope(scopeOrderWrite)
}

// attachEvidence stores the form's file through the file module and binds it
// to the claim in the path through the order module, then draws the order
// again (ADR 0325). The body is read as a stream and held to the file
// module's bound; a file that could not be bound is removed again, since
// nothing would ever name it.
func (u *UI) attachEvidence(w http.ResponseWriter, r *http.Request) {
	keeper, ok := u.afterSales.(EvidenceKeeper)
	if !ok || u.files == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Evidence unavailable",
			"The order or the file module's panel surface is not registered in this installation.")
		return
	}
	principal, _ := corehttp.PrincipalFromContext(r.Context())
	if !principal.HasScope(scopeFileWrite) {
		u.errorPage(w, r, http.StatusForbidden, "Not allowed",
			"Attaching evidence stores a file, which needs the "+scopeFileWrite+" privilege as well.")
		return
	}

	ctx := r.Context()
	orderID, claimID := chi.URLParam(r, "id"), chi.URLParam(r, "claim")
	r.Body = http.MaxBytesReader(w, r.Body, u.files.MaxUploadBytes()+evidenceEnvelope)
	uploadID, caption, err := u.readEvidenceForm(ctx, r, principal.ID)
	if err == nil {
		_, err = keeper.AttachClaimEvidence(ctx, claimID, uploadID, caption)
		if err != nil {
			if undoErr := u.files.DeleteUpload(ctx, uploadID); undoErr != nil {
				corehttp.LoggerFromContext(ctx).WarnContext(ctx,
					"the panel could not remove a file it stored and could not attach",
					"error", undoErr, "upload_id", uploadID)
			}
		}
	}

	switch {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: "The file was attached to the claim."})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The evidence could not be attached")
	}
}

// readEvidenceForm reads the caption and stores the one file the form
// carries, its type detected from its first bytes as the file module's own
// endpoint detects it; a file stored before the form turned out to be wrong
// is removed again.
func (u *UI) readEvidenceForm(ctx context.Context, r *http.Request, uploadedBy string) (uploadID, caption string, err error) {
	parts, err := r.MultipartReader()
	if err != nil {
		return "", "", errors.Invalid(CodeEvidenceInvalid, "The form could not be read; send it again with a file.")
	}
	defer func() {
		if err != nil && uploadID != "" {
			_ = u.files.DeleteUpload(ctx, uploadID)
			uploadID = ""
		}
	}()

	for {
		part, nextErr := parts.NextPart()
		if stderrors.Is(nextErr, io.EOF) {
			break
		}
		if nextErr != nil {
			return uploadID, "", tooLargeOr(nextErr, u.files.MaxUploadBytes())
		}
		switch part.FormName() {
		case formEvidenceCaption:
			text, readErr := io.ReadAll(io.LimitReader(part, captionBytes))
			if readErr != nil {
				return uploadID, "", tooLargeOr(readErr, u.files.MaxUploadBytes())
			}
			caption = strings.TrimSpace(string(text))
		case formEvidenceFile:
			if uploadID != "" {
				return uploadID, "", errors.Invalid(CodeEvidenceInvalid, "Attach one file at a time.")
			}
			uploadID, err = u.storeEvidence(ctx, part, uploadedBy)
			if err != nil {
				return "", "", err
			}
		default:
			return uploadID, "", errors.Invalid(CodeEvidenceInvalid, "The form carried a field it does not have.")
		}
	}
	if uploadID == "" {
		return "", "", errors.Invalid(CodeEvidenceInvalid, "Choose a file to attach.")
	}

	return uploadID, caption, nil
}

// storeEvidence stores one file part through the file module.
func (u *UI) storeEvidence(ctx context.Context, part io.Reader, uploadedBy string) (string, error) {
	head := make([]byte, sniffBytes)
	n, err := io.ReadFull(part, head)
	if err != nil && !stderrors.Is(err, io.EOF) && !stderrors.Is(err, io.ErrUnexpectedEOF) {
		return "", tooLargeOr(err, u.files.MaxUploadBytes())
	}
	if n == 0 {
		return "", errors.Invalid(CodeEvidenceInvalid, "Choose a file to attach; the one sent is empty.")
	}
	head = head[:n]
	contentType := http.DetectContentType(head)
	if mediaType, _, parseErr := mime.ParseMediaType(contentType); parseErr == nil {
		contentType = mediaType
	}

	name := ""
	if named, ok := part.(interface{ FileName() string }); ok {
		name = named.FileName()
	}
	id, _, err := u.files.UploadFile(ctx, contentType, name, uploadedBy, io.MultiReader(bytes.NewReader(head), part))
	if err != nil {
		return "", tooLargeOr(err, u.files.MaxUploadBytes())
	}

	return id, nil
}

// tooLargeOr turns a body cut short by the bound into the operator's sentence
// and leaves any other error as it is.
func tooLargeOr(err error, bound int64) error {
	var tooLarge *http.MaxBytesError
	if stderrors.As(err, &tooLarge) {
		return errors.Invalid(CodeEvidenceInvalid,
			"The file is larger than the shop accepts; the bound is %d bytes.", bound)
	}

	return err
}

// detachEvidence removes the piece of evidence in the path from its claim and
// draws the order again; the file stays with the file module (ADR 0325).
func (u *UI) detachEvidence(w http.ResponseWriter, r *http.Request) {
	keeper, ok := u.afterSales.(EvidenceKeeper)
	if !ok {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Evidence unavailable",
			"The order module's panel surface cannot remove evidence in this installation.")
		return
	}

	orderID := chi.URLParam(r, "id")
	switch err := keeper.DetachClaimEvidence(r.Context(), chi.URLParam(r, "evidence")); {
	case err == nil:
		u.renderOrder(w, r, http.StatusOK, orderID, &afterSaleOutcome{Done: "The evidence was removed from the claim; the file is kept."})
	case errors.IsInvalid(err) || errors.IsConflict(err) || errors.IsNotFound(err):
		u.renderOrder(w, r, http.StatusUnprocessableEntity, orderID, &afterSaleOutcome{Refused: refusalOf(err)})
	default:
		u.unexpectedFailure(w, r, err, "The evidence could not be removed")
	}
}
