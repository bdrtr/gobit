package api

// This file is English because ADR 0012 makes language a property of the FILE
// and every new file is English; the files beside it stay Turkish.

import (
	"net/http"
	"time"

	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/modules/tax/models"
	"github.com/bdrtr/gobit/internal/modules/tax/service"
)

// The paths of the tax class surface.
const (
	pathAdminClasses       = "/admin/v1/tax-classes"
	pathAdminClass         = "/admin/v1/tax-classes/{id}"
	pathAdminClassProducts = "/admin/v1/tax-classes/{id}/products"
	pathAdminClassProduct  = "/admin/v1/tax-classes/{id}/products/{productID}"
)

// createTaxClassRequest is the body of the class write.
type createTaxClassRequest struct {
	// Name is what the operator picks the class by; it is required and unique
	// among live classes.
	Name string `json:"name"`
	// Metadata is free extra data.
	Metadata map[string]any `json:"metadata"`
}

// taxClassMemberRequest is the body of the membership write.
type taxClassMemberRequest struct {
	// ProductID is the catalog product joining the class. It is NOT verified
	// against the catalog: this module knows no catalog record (Principle 2.2),
	// and a membership written for an id that does not exist never matches a
	// line.
	ProductID string `json:"product_id"`
}

// taxClassDTO is a class in a response.
type taxClassDTO struct {
	ID        string         `json:"id"`
	Name      string         `json:"name"`
	Metadata  map[string]any `json:"metadata,omitempty"`
	CreatedAt time.Time      `json:"created_at"`
	UpdatedAt time.Time      `json:"updated_at"`
}

// taxClassMemberDTO is one membership in a response.
type taxClassMemberDTO struct {
	TaxClassID string    `json:"tax_class_id"`
	ProductID  string    `json:"product_id"`
	CreatedAt  time.Time `json:"created_at"`
}

// toTaxClassDTO converts the model into its outward form.
func toTaxClassDTO(class models.TaxClass) taxClassDTO {
	return taxClassDTO{
		ID:        class.ID,
		Name:      class.Name,
		Metadata:  class.Metadata,
		CreatedAt: class.CreatedAt,
		UpdatedAt: class.UpdatedAt,
	}
}

// toTaxClassMemberDTO converts the model into its outward form.
func toTaxClassMemberDTO(member models.TaxClassMember) taxClassMemberDTO {
	return taxClassMemberDTO{
		TaxClassID: member.TaxClassID,
		ProductID:  member.ProductID,
		CreatedAt:  member.CreatedAt,
	}
}

// createClass is the POST /admin/v1/tax-classes handler.
func (a *API) createClass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body createTaxClassRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	class, err := a.svc.CreateTaxClass(ctx, service.CreateTaxClassInput{
		Name:     body.Name,
		Metadata: body.Metadata,
	})
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxClassDTO(class))
}

// listClasses is the GET /admin/v1/tax-classes handler.
func (a *API) listClasses(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	classes, err := a.svc.ListTaxClasses(ctx)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, classes, toTaxClassDTO)
}

// getClass is the GET /admin/v1/tax-classes/{id} handler.
func (a *API) getClass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	class, err := a.svc.GetTaxClass(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusOK, toTaxClassDTO(class))
}

// deleteClass is the DELETE /admin/v1/tax-classes/{id} handler.
func (a *API) deleteClass(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.DeleteTaxClass(ctx, pathParam(r, "id")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}

// listClassProducts is the GET /admin/v1/tax-classes/{id}/products handler.
func (a *API) listClassProducts(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	members, err := a.svc.ListTaxClassMembers(ctx, pathParam(r, "id"))
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeAll(w, r, members, toTaxClassMemberDTO)
}

// addClassProduct is the POST /admin/v1/tax-classes/{id}/products handler.
//
// A product already in another class is MOVED rather than refused:
// reclassifying is the ordinary operator action, and refusing it would make
// them delete the old membership first, leaving a window in which the product
// is in no class at all.
func (a *API) addClassProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	var body taxClassMemberRequest
	if err := decodeBody(w, r, &body); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}

	member, err := a.svc.SetProductTaxClass(ctx, pathParam(r, "id"), body.ProductID)
	if err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	writeItem(w, r, http.StatusCreated, toTaxClassMemberDTO(member))
}

// removeClassProduct is the DELETE /admin/v1/tax-classes/{id}/products/{productID}
// handler.
//
// The class in the path is not compared against the product's: a product is in
// at most one class, so "take it out of THIS class" and "take it out" cannot
// differ in outcome, and comparing them would invent a way to fail.
func (a *API) removeClassProduct(w http.ResponseWriter, r *http.Request) {
	ctx := r.Context()

	if err := a.svc.RemoveProductTaxClass(ctx, pathParam(r, "productID")); err != nil {
		corehttp.WriteError(ctx, w, err)
		return
	}
	corehttp.WriteJSON(ctx, w, http.StatusNoContent, nil)
}
