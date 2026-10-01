package service

import (
	"context"
	"encoding/json"
	"time"

	"github.com/bdrtr/gobit/core/errors"
)

// codeAdminEncodeFailed reports a listing the panel's surface could not
// encode.
const codeAdminEncodeFailed = "product_admin_encode_failed"

// adminRevision is one revision as the panel lists it (ADR 0316); the json
// tags are the contract with the panel, which cannot import this package.
type adminRevision struct {
	Version    int64     `json:"version"`
	RecordedAt time.Time `json:"recorded_at"`
	Changed    []string  `json:"changed"`
	RequestID  *string   `json:"request_id"`
}

// RevisionsJSON lists the product's revisions, newest first, a page at a time,
// with their total (ADR 0316). A revision's snapshot is left out, as the admin
// API's listing leaves it.
func (a *AdminSurface) RevisionsJSON(ctx context.Context, productID string, limit, offset int32) (json.RawMessage, int64, error) {
	if a == nil || a.svc == nil {
		return nil, 0, errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	page, err := a.svc.ListRevisions(ctx, productID, int(limit), int(offset))
	if err != nil {
		return nil, 0, err
	}

	out := make([]adminRevision, 0, len(page.Items))
	for i := range page.Items {
		rev := &page.Items[i]
		out = append(out, adminRevision{
			Version: rev.Version, RecordedAt: rev.RecordedAt, Changed: rev.Changed, RequestID: rev.RequestID,
		})
	}
	body, err := json.Marshal(out)
	if err != nil {
		return nil, 0, errors.Wrap(err, errors.KindInternal, codeAdminEncodeFailed,
			"the revisions of product %s could not be encoded", productID)
	}

	var total int64
	if page.Count != nil {
		total = int64(*page.Count)
	}

	return body, total, nil
}

// RestoreRevision writes the revision's content back to the product as a new
// revision and returns what the revision named that was removed since, each
// as kind:id (ADR 0316). The version is the one the operator's page was read
// at, and the restore is refused with [CodeVersionMismatch] when the product
// was written since, as an edit is (ADR 0222).
func (a *AdminSurface) RestoreRevision(ctx context.Context, productID string, revision, version int64) ([]string, error) {
	if a == nil || a.svc == nil {
		return nil, errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	result, err := a.svc.RestoreRevision(ExpectVersion(ctx, version), productID, revision)
	if err != nil {
		return nil, err
	}

	return result.Dropped, nil
}
