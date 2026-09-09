package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// AttachClaimEvidenceInput binds a file to a claim.
type AttachClaimEvidenceInput struct {
	// UploadID is the file module's upload id; it is REQUIRED.
	//
	// It is the id and not the address. An operator opens a claim days or months
	// after it was filed, and an object store's address is signed and expires; a
	// stored address would be right about the file and wrong about where to get
	// it. The id resolves through the file module whenever it is asked.
	UploadID string
	// Caption is what the operator says the picture shows; it may be empty.
	Caption string
}

// AttachClaimEvidence binds a file to the claim.
//
// # The claim is read first, and the reason is the message
//
// The foreign key would refuse an unknown claim on its own, with a constraint
// name. Reading it first turns that into "the claim was not found", which is the
// answer an operator can act on.
//
// # What it does NOT do
//
// It does not verify that the upload EXISTS. The id belongs to the file module
// and this module cannot see it (Principle 2.2); asking would mean a
// cross-module read on every attach, and the answer would be stale the moment
// after it was given. What is refused is an EMPTY id, which is the one value
// that claims a file while naming none.
func (s *Service) AttachClaimEvidence(
	ctx context.Context, claimID string, in AttachClaimEvidenceInput,
) (models.ClaimEvidence, error) {
	if err := requireID("claim_id", claimID); err != nil {
		return models.ClaimEvidence{}, err
	}
	if err := requireID("upload_id", in.UploadID); err != nil {
		return models.ClaimEvidence{}, err
	}

	caption := strings.TrimSpace(in.Caption)
	if err := checkTextLen("caption", caption); err != nil {
		return models.ClaimEvidence{}, err
	}

	if _, err := s.store.GetClaim(ctx, claimID); err != nil {
		return models.ClaimEvidence{}, err
	}

	return s.store.CreateClaimEvidence(ctx, models.ClaimEvidence{
		ID:           models.NewClaimEvidenceID(),
		OrderClaimID: claimID,
		UploadID:     in.UploadID,
		Caption:      caption,
	})
}

// ListClaimEvidence returns the claim's evidence, oldest first.
func (s *Service) ListClaimEvidence(
	ctx context.Context, claimID string,
) ([]models.ClaimEvidence, error) {
	if err := requireID("claim_id", claimID); err != nil {
		return nil, err
	}

	return s.store.ListClaimEvidence(ctx, claimID)
}

// DetachClaimEvidence removes a file from its claim.
//
// The FILE is untouched. It belongs to the file module and may be evidence of
// something else, or the operator may simply have attached it to the wrong
// claim; deleting the upload from here would be this module reaching into
// another one's data on a guess.
func (s *Service) DetachClaimEvidence(ctx context.Context, evidenceID string) error {
	if err := requireID("evidence_id", evidenceID); err != nil {
		return err
	}

	return s.store.DeleteClaimEvidence(ctx, evidenceID)
}
