package service

import (
	"context"
	"strings"
	"time"

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

// ScheduleProduct replaces a product's schedule with the given moments, either
// of which may be nil (ADR 0177, ADR 0178, ADR 0179).
//
// The rules are the service's. The panel checks what it can see before it
// writes anything, so an edit is not half-saved, but this is where they are
// decided.
func (a *AdminSurface) ScheduleProduct(ctx context.Context, id string, publishAt, archiveAt *time.Time) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}
	_, err := a.svc.SetSchedule(ctx, id, Schedule{PublishAt: publishAt, ArchiveAt: archiveAt})

	return err
}

// UnscheduleProduct takes the whole schedule off a product; a product with none
// is left as it is.
func (a *AdminSurface) UnscheduleProduct(ctx context.Context, id string) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}
	_, err := a.svc.ClearSchedule(ctx, id)

	return err
}

// SetProductRelations replaces the given kinds of a product's relations, all of
// them or none (ADR 0181); a kind absent from the map is left as it is.
//
// Each list holds references in the storefront's order, and a reference is a
// product id or a handle: an operator knows the handle, and the panel should not
// have to look every one up before it can save. The kinds are the strings the
// API's paths use. The rules are the service's, and a refusal names what it
// refused as the operator typed it.
func (a *AdminSurface) SetProductRelations(ctx context.Context, id string, lists map[string][]string) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	// The references of every list are resolved in one read, then cut back into
	// their lists by length.
	kinds := make([]models.RelationType, 0, len(lists))
	var refs []string
	for kind, list := range lists {
		kinds = append(kinds, models.RelationType(kind))
		refs = append(refs, list...)
	}
	ids, err := a.svc.ResolveProductRefs(ctx, refs)
	if err != nil {
		return err
	}

	resolved := make(map[models.RelationType][]string, len(kinds))
	for _, kind := range kinds {
		n := len(lists[string(kind)])
		resolved[kind], ids = ids[:n:n], ids[n:]
	}
	_, err = a.svc.SetRelationLists(ctx, id, resolved)

	return err
}

// SetProductAddOns replaces a product's add-on list (ADR 0232).
//
// Each reference is a variant id or a SKU, in the storefront's order: an
// operator knows the SKU, and the panel should not have to look each one up
// before it can save. The rules are the service's (ADR 0228), and a refusal
// names what it refused as the operator typed it.
func (a *AdminSurface) SetProductAddOns(ctx context.Context, id string, refs []string) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}
	ids, err := a.svc.ResolveVariantRefs(ctx, refs)
	if err != nil {
		return err
	}
	_, err = a.svc.SetProductAddOns(ctx, id, ids)

	return err
}

// SetVariantBundle replaces what a variant is made of (ADR 0236): each part a
// variant id or a SKU with the units one bundle holds, in the operator's
// order, refused when the product is no longer at the version the form was read
// at.
//
// The quantities travel beside the references rather than inside them because
// the panel cannot name this module's types; the two lists are refused unless
// they are as long as each other. A quantity outside ADR 0234's bound is refused
// naming the reference as typed, before it is narrowed to the column's width.
// The rest are the service's rules.
func (a *AdminSurface) SetVariantBundle(
	ctx context.Context, variantID string, refs []string, quantities []int64, version int64,
) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}
	if len(refs) != len(quantities) {
		return invalid("%d parts were named with %d quantities", len(refs), len(quantities))
	}
	ids, err := a.svc.ResolveVariantRefs(ctx, refs)
	if err != nil {
		return err
	}
	components := make([]models.BundleComponent, 0, len(ids))
	for i, id := range ids {
		// The bound is checked where the value is narrowed, so the conversion
		// below is one the checker can see is safe.
		quantity := quantities[i]
		if quantity < 1 || quantity > MaxBundleComponentQuantity {
			return invalid("a part is held 1 to %d times (%s: %d)", MaxBundleComponentQuantity, refs[i], quantity)
		}
		components = append(components, models.BundleComponent{VariantID: id, Quantity: int32(quantity)})
	}
	_, err = a.svc.SetVariantBundle(ExpectVersion(ctx, version), variantID, components)

	return err
}

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
//
// # The version
//
// The version is the one the form was read at, and the write is refused with
// [CodeVersionMismatch] when the product was written since: two operators
// saving one product no longer overwrite each other (ADR 0222).
func (a *AdminSurface) UpdateProductBasics(ctx context.Context, id, title, handle, status string, version int64) error {
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

	_, err := a.svc.UpdateProduct(ExpectVersion(ctx, version), id, UpdateProductInput{
		Title:  &trimmedTitle,
		Handle: &trimmedHandle,
		Status: &parsed,
	})

	return err
}

// CreateProduct creates a DRAFT product with the title and the handle — the
// title's slug when the handle is empty — and returns its id (ADR 0307).
//
// A draft is the only status a new product takes here: the storefront does not
// show it until the operator publishes it on its edit form, by which time it
// has its variants, prices and stock.
func (a *AdminSurface) CreateProduct(ctx context.Context, title, handle string) (string, error) {
	if a == nil || a.svc == nil {
		return "", errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	// The service trims the title and the handle and derives the handle when
	// it is empty, as it does for the admin API.
	product, err := a.svc.CreateProduct(ctx, CreateProductInput{
		Title:  title,
		Handle: handle,
		Status: models.StatusDraft,
	})
	if err != nil {
		return "", err
	}

	return product.ID, nil
}

// AddVariant adds a variant with the title and the SKU — none when empty — to
// the product and returns its id (ADR 0307).
func (a *AdminSurface) AddVariant(ctx context.Context, productID, title, sku string) (string, error) {
	if a == nil || a.svc == nil {
		return "", errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	// The service trims the SKU and takes an empty one for none.
	variant, err := a.svc.CreateVariant(ctx, productID, CreateVariantInput{Title: title, SKU: &sku})
	if err != nil {
		return "", err
	}

	return variant.ID, nil
}

// PriceVariant sets the variant's base price at one unit in the currency,
// creating its price set and linking it first when the variant has none — the
// import's own act (ADR 0207, ADR 0309). Other prices on the set are left as
// they are; a base price already in the currency is replaced.
func (a *AdminSurface) PriceVariant(ctx context.Context, variantID, currencyCode string, amount int64) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}
	if _, err := a.svc.GetVariant(ctx, variantID); err != nil {
		return err
	}

	// Pricing trims and upper-cases the currency, as it does for an import.
	_, err := a.svc.importPrices(ctx, variantID, map[string]int64{currencyCode: amount})

	return err
}

// SetVariantCost writes the variant's unit cost in one currency from the cost
// the form was drawn with, read, nil for none; a nil amount clears it, and
// another currency's cost is not touched (ADR 0412). A cost that moved since
// is refused with [CodeVariantCostMoved].
func (a *AdminSurface) SetVariantCost(ctx context.Context, variantID, currencyCode string, read, amount *int64) error {
	if a == nil || a.svc == nil {
		return errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	return a.svc.SetVariantCost(ctx, variantID, currencyCode, read, amount)
}

// StockVariant gives the variant an inventory item, created by inventory and
// linked here, and returns its id; a variant that has one returns it (ADR
// 0310). Its levels are then set on the stock form, location by location.
func (a *AdminSurface) StockVariant(ctx context.Context, variantID string) (string, error) {
	if a == nil || a.svc == nil {
		return "", errors.Unavailable(codeNotReady, "the product service is not set up")
	}

	return a.svc.stockVariant(ctx, variantID)
}
