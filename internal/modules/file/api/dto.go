package api

import (
	"time"

	"github.com/bdrtr/gobit/internal/modules/file/models"
)

// The DTOs are kept SEPARATE from the domain models: JSON field names are the
// outside contract and a rename made in the model must not break the client.

// uploadDTO is the response body of an upload record.
//
// # THERE IS NO STORAGE KEY FIELD
//
// The record has one but it is not published. The only thing the client needs
// in order to reach the file is [uploadDTO.URL], and publishing the key on top
// of that would be promising the same thing with two different contracts: today
// the address is derived from the key, but in an object store the address is
// signed and has nothing to do with the key at all. An endpoint publishing both
// would quietly start lying on that day.
//
// # THERE IS NO UPDATE TIME FIELD
//
// An upload record is never updated — there is no modification endpoint, and
// the file does not change either. Publishing updated_at would be promising
// that it can change.
type uploadDTO struct {
	// ID is the record's identifier; the delete endpoint takes it.
	ID string `json:"id"`
	// URL is the reachable address of the file.
	//
	// On the local provider it is RELATIVE TO THE ROOT ("/files/…"); a
	// storefront served from a different origin puts its own origin in front of
	// it.
	URL string `json:"url"`
	// ContentType is the type detected FROM THE CONTENT of the file.
	//
	// It IS NOT the type the client declared while uploading and it may differ
	// from it; that is exactly why it comes back in the response — the client
	// must see not what it sent, but what the system STORED.
	ContentType string `json:"content_type"`
	// Size is the size of the file in bytes.
	Size int64 `json:"size"`
	// Checksum is the SHA-256 digest of the content (lowercase hexadecimal).
	Checksum string `json:"checksum"`
	// ProviderID is the identifier of the provider storing the file.
	ProviderID string `json:"provider_id"`
	// OriginalName is the file name the client declared; if it is empty the
	// field does not appear at all.
	//
	// It is DISPLAY data: in the admin panel it answers the "which file was it
	// that I uploaded" question. It enters no path expression and no HTTP
	// header.
	OriginalName string `json:"original_name,omitempty"`
	// UploadedBy is the identifier of the caller who did the upload; if it is
	// empty the field does not appear.
	UploadedBy string `json:"uploaded_by,omitempty"`
	// CreatedAt is the moment the upload was made (RFC3339, UTC).
	CreatedAt time.Time `json:"created_at"`
}

// toUploadDTO converts the domain record into the response body.
func toUploadDTO(u models.Upload) uploadDTO {
	return uploadDTO{
		ID:           u.ID,
		URL:          u.URL,
		ContentType:  u.ContentType,
		Size:         u.Size,
		Checksum:     u.Checksum,
		ProviderID:   u.ProviderID,
		OriginalName: u.OriginalName,
		UploadedBy:   u.UploadedBy,
		CreatedAt:    u.CreatedAt,
	}
}
