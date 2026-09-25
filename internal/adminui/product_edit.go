package adminui

import (
	"context"
	"net/http"
	"slices"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// ServiceProductAdmin is the container name of the product module's admin write
// surface (ADR 0013).
//
// It is spelled by hand, like the catalog's entity and link names, and pinned
// against the module's own constant at compile time in internal/arch.
const ServiceProductAdmin = "product.admin"

// ProductEditPath is the edit form of one product.
const ProductEditPath = ProductPath + "/edit"

// productStatuses are the values the edit form offers.
//
// The panel repeats them because the module's Status type cannot be imported.
// A value the module no longer accepts does not fail silently: the write
// surface rejects an unknown status with errors.Invalid and the form comes back
// with that message. The list is pinned against the module's constants in
// internal/arch all the same — a status the module ADDED would otherwise never
// appear in the form, which no error would report.
var productStatuses = []string{"draft", "published", "archived"}

// ProductStatuses returns the statuses the edit form offers.
//
// It is exported so internal/arch can compare the list against the module's own
// constants; the slice is cloned so a caller cannot reorder the form's options
// from outside.
func ProductStatuses() []string { return slices.Clone(productStatuses) }

// ProductWriter is the narrow write surface the panel needs, declared on the
// CONSUMER side (ADR 0001).
//
// The panel edits a product's basics and a draft's schedule, and nothing else.
// Each method is a decision someone made: an interface that offered more would
// let a future screen delete a product without that decision being made
// anywhere. The schedule joined in ADR 0178.
//
// The signatures speak only in primitives and stdlib types because this package
// cannot import the product module; see the module's admin surface for the full
// reason.
type ProductWriter interface {
	// UpdateProductBasics updates a product's title, handle and status.
	UpdateProductBasics(ctx context.Context, id, title, handle, status string) error
	// ScheduleProduct sets the moment a draft is published (ADR 0177).
	ScheduleProduct(ctx context.Context, id string, at time.Time) error
	// UnscheduleProduct takes a schedule off; a product with none is left as
	// it is.
	UnscheduleProduct(ctx context.Context, id string) error
}

// publishAtLayout is how the form's moment is written and read: the value of
// an HTML datetime-local input, which carries no zone. The panel reads it as
// UTC and says so on the label, because a moment in the operator's unknown zone
// would publish at an hour nobody chose.
const publishAtLayout = "2006-01-02T15:04"

// statusDraft is the one status a schedule belongs to.
const statusDraft = "draft"

// editProduct renders the edit form.
func (u *UI) editProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	product, ok := u.loadProduct(w, r, id)
	if !ok {
		return
	}

	u.renderEditForm(w, r, http.StatusOK, product, "")
}

// submitProductEdit applies the edit and returns to the product page.
//
// # Why a redirect and not a rendered page
//
// After a successful POST the browser is sent to the product page with a 303.
// Rendering the result here would leave the form's POST in the history: a
// refresh would re-submit it, and the operator would apply the same edit twice
// without meaning to. The redirect is the standard answer and the reason
// [corehttp.WriteRedirect] pins 303 rather than leaving the code to the caller.
//
// # Why the form comes back on a rejection
//
// A rejected edit re-renders the form with what the operator typed and the
// service's message. Redirecting on failure would throw the typed values away
// and show a message with no field to fix.
func (u *UI) submitProductEdit(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")

	if u.products == nil {
		u.errorPage(w, r, http.StatusServiceUnavailable, "Editing unavailable",
			"The product module's admin surface is not registered in this installation.")
		return
	}

	if err := r.ParseForm(); err != nil {
		u.errorPage(w, r, http.StatusBadRequest, "Bad request", "The form could not be read.")
		return
	}

	title := r.PostFormValue("title")
	handle := r.PostFormValue("handle")
	status := r.PostFormValue("status")
	publishAt := strings.TrimSpace(r.PostFormValue("publish_at"))

	// The schedule is checked BEFORE anything is written, so a moment that
	// cannot be kept does not leave the basics saved and the form saying it
	// failed. The product module still decides; this only spares a half-edit.
	moment, problem := readPublishAt(publishAt, status, time.Now())
	if problem != "" {
		u.rerenderEdit(w, r, id, title, handle, status, publishAt, problem)
		return
	}

	err := u.products.UpdateProductBasics(r.Context(), id, title, handle, status)
	if err == nil && status == statusDraft {
		if moment.IsZero() {
			err = u.products.UnscheduleProduct(r.Context(), id)
		} else {
			err = u.products.ScheduleProduct(r.Context(), id, moment)
		}
	}
	if err == nil {
		corehttp.WriteRedirect(r.Context(), w, ProductsPath+"/"+id)
		return
	}

	// Only a rejection the operator can act on is shown on the form. Anything
	// else — the database unreachable, the surface misconfigured — becomes the
	// panel's error page and the real cause goes to the log.
	if !errors.IsInvalid(err) && !errors.IsConflict(err) {
		u.unexpectedFailure(w, r, err, "The product could not be saved")
		return
	}

	u.rerenderEdit(w, r, id, title, handle, status, publishAt, messageFor(err))
}

// rerenderEdit shows the form again with what the operator typed and why it
// was refused.
func (u *UI) rerenderEdit(
	w http.ResponseWriter, r *http.Request, id, title, handle, status, publishAt, message string,
) {
	product, ok := u.loadProduct(w, r, id)
	if !ok {
		return
	}
	// What the operator typed is shown back, not what is stored: the form must
	// return the values that were rejected so the mistake is visible.
	product.Title, product.Handle, product.Status = title, handle, status
	product.PublishAtTyped = &publishAt

	u.renderEditForm(w, r, http.StatusUnprocessableEntity, product, message)
}

// readPublishAt reads the form's moment and says, in the form's own words, why
// it cannot be kept. An empty field is no schedule.
func readPublishAt(value, status string, now time.Time) (moment time.Time, problem string) {
	if value == "" {
		return time.Time{}, ""
	}
	moment, err := time.ParseInLocation(publishAtLayout, value, time.UTC)
	if err != nil {
		return time.Time{}, "The publication moment could not be read; use the date and time picker."
	}
	if status != statusDraft {
		return time.Time{}, "Only a draft can be scheduled. Set the status to draft, or clear the moment."
	}
	if !moment.After(now) {
		return time.Time{}, "The publication moment has to be in the future. To publish now, set the status."
	}

	return moment, ""
}

// loadProduct reads one product for the form, answering on the way when it
// cannot.
func (u *UI) loadProduct(w http.ResponseWriter, r *http.Request, id string) (productRow, bool) {
	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No product was named.")
		return productRow{}, false
	}

	products, err := u.catalog.Graph(r.Context(), productByID(id))
	if err != nil {
		u.catalogFailure(w, r, err, "The product could not be read.")
		return productRow{}, false
	}
	if len(products) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "There is no product with that id.")
		return productRow{}, false
	}

	return productRowOf(products[0]), true
}

// renderEditForm writes the form with the given status code and message.
func (u *UI) renderEditForm(
	w http.ResponseWriter, r *http.Request, status int, product productRow, message string,
) {
	u.templates.render(w, r, status, "product_edit.gohtml", map[string]any{
		titleKey:     "Edit " + product.Title,
		"Product":    product,
		"Statuses":   productStatuses,
		errorKey:     message,
		"ActionPath": ProductsPath + "/" + product.ID + "/edit",
		"CancelPath": ProductsPath + "/" + product.ID,
	})
}

// messageFor returns the part of a rejection that is safe to show.
//
// The message of an Invalid or Conflict error is written by the service author
// and is client-safe by the framework's own rule — the same rule
// [corehttp.WriteError] applies when it passes those classes through untouched.
func messageFor(err error) string {
	var typed *errors.Error
	if errors.As(err, &typed) && typed.Message != "" {
		return typed.Message
	}

	return "The product could not be saved."
}
