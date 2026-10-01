package service

import (
	"context"
	"io"
)

// AdminSurface is the file module's panel surface (ADR 0325): an upload from
// the panel's form, the address of one, and the removal of one nothing came
// to name. Only primitives and stdlib types cross it, as across every surface
// the panel resolves (ADR 0001), and every rule stays on [Service].
type AdminSurface struct {
	svc *Service
}

// NewAdminSurface builds the panel's surface over the service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// MaxUploadBytes is the size an upload is held to, so the panel bounds the
// body it reads by the module's own figure.
func (a *AdminSurface) MaxUploadBytes() int64 { return a.svc.MaxUploadBytes() }

// UploadFile stores the body, whose type the caller detected from its first
// bytes, and returns the upload's id and the address it is served at; the
// module checks the type against its allow list and the size against its
// bound.
func (a *AdminSurface) UploadFile(
	ctx context.Context, contentType, originalName, uploadedBy string, body io.Reader,
) (id, url string, err error) {
	record, err := a.svc.Upload(ctx, UploadInput{
		ContentType: contentType, Body: body, OriginalName: originalName, UploadedBy: uploadedBy,
	})
	if err != nil {
		return "", "", err
	}

	return record.ID, record.URL, nil
}

// UploadURL is the address an upload is served at.
func (a *AdminSurface) UploadURL(ctx context.Context, id string) (string, error) {
	record, err := a.svc.GetUpload(ctx, id)
	if err != nil {
		return "", err
	}

	return record.URL, nil
}

// DeleteUpload removes an upload, the panel's undoing of one it stored and
// then could not attach.
func (a *AdminSurface) DeleteUpload(ctx context.Context, id string) error {
	return a.svc.DeleteUpload(ctx, id)
}
