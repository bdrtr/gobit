package api

import (
	"net/http"
	"time"

	"github.com/go-chi/chi/v5"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// attachClaimEvidenceRequest binds a file to a claim.
type attachClaimEvidenceRequest struct {
	// UploadID is the id from POST /admin/v1/uploads; it is REQUIRED.
	UploadID string `json:"upload_id"`
	// Caption is what the operator says the picture shows; it may be left out.
	Caption string `json:"caption"`
}

// claimEvidenceDTO is the external representation of one piece of evidence.
//
// It carries the upload's ID and no address. The address is resolved through the
// file module when it is needed, because a signed one expires and a claim is
// opened long after it was filed (ADR 0106).
type claimEvidenceDTO struct {
	ID           string    `json:"id"`
	OrderClaimID string    `json:"order_claim_id"`
	UploadID     string    `json:"upload_id"`
	Caption      string    `json:"caption,omitempty"`
	CreatedAt    time.Time `json:"created_at"`
	UpdatedAt    time.Time `json:"updated_at"`
}

// toClaimEvidenceDTO converts the model to the external representation.
func toClaimEvidenceDTO(evidence models.ClaimEvidence) claimEvidenceDTO {
	return claimEvidenceDTO{
		ID:           evidence.ID,
		OrderClaimID: evidence.OrderClaimID,
		UploadID:     evidence.UploadID,
		Caption:      evidence.Caption,
		CreatedAt:    evidence.CreatedAt,
		UpdatedAt:    evidence.UpdatedAt,
	}
}

// adminAttachClaimEvidence binds a file to the claim.
func (h *Handler) adminAttachClaimEvidence(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body attachClaimEvidenceRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	evidence, err := h.svc.AttachClaimEvidence(ctx, chi.URLParam(r, paramClaimID), service.AttachClaimEvidenceInput{
		UploadID: body.UploadID,
		Caption:  body.Caption,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusCreated,
		singleEnvelope{Data: toClaimEvidenceDTO(evidence)})
}

// adminListClaimEvidence returns the claim's evidence, oldest first.
//
// The single envelope with an ARRAY in it: the evidence belongs to one claim and
// there is no page to ask for (D44).
func (h *Handler) adminListClaimEvidence(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	evidence, err := h.svc.ListClaimEvidence(ctx, chi.URLParam(r, paramClaimID))
	if err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}

	out := make([]claimEvidenceDTO, 0, len(evidence))
	for i := range evidence {
		out = append(out, toClaimEvidenceDTO(evidence[i]))
	}
	corehttp.WriteJSON(ctx, w, http.StatusOK, singleEnvelope{Data: out})
}

// adminDetachClaimEvidence removes a file from its claim.
//
// The FILE is untouched: it belongs to the file module and may be evidence of
// something else. What is deleted is the binding.
func (h *Handler) adminDetachClaimEvidence(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	id := chi.URLParam(r, paramEvidenceID)
	if err := h.svc.DetachClaimEvidence(ctx, id); err != nil {
		corehttp.WriteError(ctx, w, err)

		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
