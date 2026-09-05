package service

import (
	"context"
	"strings"

	"github.com/bdrtr/gobit/core/errors"

	"github.com/bdrtr/gobit/internal/modules/product/models"
)

// This file is the product module's ADMIN WRITE surface (ADR 0013).
//
// It is separate from interop.go and the separation is the decision, not a
// filing convenience. interop.go is the surface OTHER MODULES, workflows and
// plugins read the catalog through, and its own godoc promises to stay narrow;
// a write method added there would let any plugin rewrite the catalog. This
// surface has one audience — the admin panel — and one job: let a human edit a
// product without the panel importing this module.
//
// Everything it does goes through [Service], never the repository. The service
// is where the handle uniqueness check lives and where the "product.updated"
// event is published; a surface that reached the repository directly would
// write a product that no subscriber ever hears about.
//
// The signature speaks only in PRIMITIVE types, for the same reason interop
// does: the consumer cannot import this package, so it cannot name
// UpdateProductInput or models.Status. The moment it names such a type, that
// type is a DIFFERENT type declared in the consumer's own package and the
// concrete surface stops satisfying the consumer's interface.

// CodeAdminInputInvalid reports input the admin surface refuses.
const CodeAdminInputInvalid = "product_admin_input_invalid"

// AdminSurface is the product module's admin write surface.
//
// It is registered in the container under [github.com/bdrtr/gobit/internal/modules/product.AdminName];
// who may resolve that name is not a matter of taste and is checked in
// internal/arch.
type AdminSurface struct{ svc *Service }

// NewAdminSurface builds the admin surface over the given service.
func NewAdminSurface(svc *Service) *AdminSurface { return &AdminSurface{svc: svc} }

// UpdateProductBasics updates a product's title, handle and status.
//
// # Why these three and not a patch document
//
// The three are what an edit form shows, they are always all submitted
// together, and each is a plain string. A JSON patch — the shape interop uses
// for its structured payloads — would buy the ability to omit a field, which
// this caller never needs, at the cost of a schema the consumer has to build
// and this surface has to validate.
//
// The price is that a fourth field means a new signature. That failure is NOT
// silent: the consumer resolves this surface through its own narrow interface,
// and a signature that no longer matches makes container.Resolve fail AT
// STARTUP with a message naming the missing method.
//
// # Everything is validated by the service
//
// The status is parsed here because the consumer sends a string and this is the
// only place that knows the valid values; everything else — an empty title, a
// handle already taken by another product — is the service's rule and is left
// to it. Repeating those checks here would create a second place to keep in
// step with the first.
func (a *AdminSurface) UpdateProductBasics(ctx context.Context, id, title, handle, status string) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	parsed := models.Status(strings.TrimSpace(status))
	if !parsed.Valid() {
		return errors.Invalid(CodeAdminInputInvalid,
			"%q is not a valid product status (expected: %s, %s or %s)",
			status, models.StatusDraft, models.StatusPublished, models.StatusArchived)
	}

	trimmedTitle := strings.TrimSpace(title)
	trimmedHandle := strings.TrimSpace(handle)

	_, err := a.svc.UpdateProduct(ctx, id, UpdateProductInput{
		Title:  &trimmedTitle,
		Handle: &trimmedHandle,
		Status: &parsed,
	})

	return err
}
