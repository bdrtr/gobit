// Package service holds the business logic of the b2b module.
//
// # The assumption the module breaks
//
// The B2C assumption of the storefront flow is "buyer = individual". In B2B the
// buyer is an employee with a LIMITED SPENDING AUTHORITY: their identity is
// again a customer record (customer module), but how much they may spend is
// decided by the company they belong to. This module holds those two pieces of
// information — the company and the employee's authority; it does NOT hold the
// identity itself.
//
// # Why the customer bond is a link
//
// The bond between the employee and the customer lives in core/link, not in a
// column (Principle 2.2, ADR 0005). The uniqueness of the bond is enforced by
// the cardinality: a customer can own at most ONE employee record (see
// [Definitions]). The storefront's "my own company" question resolves to a
// single answer only thanks to that uniqueness.
//
// # The surface it opens to the outside
//
// The module imports no other module and calls no other module's service. That
// the customer REALLY exists is not verified in this module: had it verified,
// it would depend on the customer module and the bond would be exactly the
// dependency the link exists to remove. An employee record bound to a customer
// that does not exist is harmless, because it resolves to no request in the
// storefront.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "b2b_invalid_input"
	// CodeNotReady reports that the service has not been set up.
	CodeNotReady = "b2b_service_unconfigured"
	// CodeEmployeeNotFound reports that the requested employee could not be
	// found.
	CodeEmployeeNotFound = "b2b_employee_not_found"
	// CodeLinkFailed reports that the customer bond could not be established.
	CodeLinkFailed = "b2b_link_failed"
)

// Pagination limits. If no limit is given the default is applied; an
// excessively large limit is rejected, so a client cannot scan the database
// with a single request.
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be asked for in a single
	// request.
	MaxLimit int64 = 100
)

// Page is a paginated list result.
//
// Limit and Offset are not the raw values of the request but the APPLIED
// values; the API envelope writes these fields as they are, so the client is
// aware of a limit that fell back to the default.
type Page[T any] struct {
	// Items are the records on the current page.
	Items []T
	// Count is the TOTAL number of records matching the filter (not the page
	// size).
	Count int64
	// Limit is the applied page size.
	Limit int64
	// Offset is the applied skip count.
	Offset int64
}

// Repository is the data access surface the service needs.
//
// The interface is defined on the CONSUMING side (here); the concrete
// implementation is in the internal/modules/b2b/repository package. This is the
// IN-MODULE counterpart of ADR 0001's pattern and it lets the service be tested
// without a database.
type Repository interface {
	CreateCompany(ctx context.Context, c models.Company) (models.Company, error)
	GetCompany(ctx context.Context, id string) (models.Company, error)
	ListCompanies(ctx context.Context, filter models.CompanyFilter, limit, offset int64) ([]models.Company, int64, error)
	UpdateCompany(ctx context.Context, id string, patch models.CompanyPatch, now time.Time) (models.Company, error)
	// DeleteCompany deletes the company and its employees; the returned slice
	// holds the identifiers of the deleted employees (the service removes the
	// bonds).
	DeleteCompany(ctx context.Context, id string, now time.Time) ([]string, error)

	CreateEmployee(ctx context.Context, e models.CompanyEmployee) (models.CompanyEmployee, error)
	GetEmployee(ctx context.Context, id string) (models.CompanyEmployee, error)
	ListEmployees(ctx context.Context, filter models.EmployeeFilter, limit, offset int64) ([]models.CompanyEmployee, int64, error)
	UpdateEmployee(ctx context.Context, id string, patch models.EmployeePatch, now time.Time) (models.CompanyEmployee, error)
	DeleteEmployee(ctx context.Context, id string, now time.Time) error
}

// Linker is the NARROW surface the service needs from the cross-module bond
// layer.
//
// core/link's full interface also covers definition declaration and reverse
// direction reads; the methods here are the ones the module REALLY calls.
// Keeping it narrow serves two purposes: the dependency is bounded by the
// surface actually used, and a fake bond service can be written in a few lines
// in unit tests.
type Linker interface {
	// Create establishes a bond between fromID and toID; if the same pair is
	// bound a second time the call is a no-op, and a cardinality violation is
	// an errors.Conflict.
	Create(ctx context.Context, name, fromID, toID string) error
	// Delete removes the bond; if there is no bond the call is a no-op.
	Delete(ctx context.Context, name, fromID, toID string) error
	// ListMany returns the customer identifiers of several employees in a
	// SINGLE query.
	ListMany(ctx context.Context, name string, fromIDs []string) (map[string][]string, error)
	// ListManyByTo resolves the reverse direction: it returns the employee
	// identifiers of the given customers. The storefront's "my own employee
	// record" question is answered with this.
	ListManyByTo(ctx context.Context, name string, toIDs []string) (map[string][]string, error)
}

// Options are the setup settings of the service.
type Options struct {
	// Repo is the persistence surface; it is required.
	Repo Repository
	// Links is the cross-module bond service; it is required.
	Links Linker
	// Logger is the structured log target; if nil the logs are discarded.
	Logger *slog.Logger
	// Now is the time source; if nil time.Now is used. Tests fill this in
	// with a fixed clock to make the time-dependent branches deterministic.
	Now func() time.Time
}

// Service is the public service of the b2b module. It is safe for concurrent
// use.
type Service struct {
	repo  Repository
	links Linker
	log   *slog.Logger
	now   func() time.Time
}

// New produces a service with the given dependencies.
//
// A missing dependency returns an error at SETUP time. Without the link service
// the module would run "silently half-done": the employee rows would be
// written, but none of them bound to a customer, and the gap would only become
// visible in the storefront, when no customer can find their company.
func New(opts Options) (*Service, error) {
	if opts.Repo == nil {
		return nil, errors.Internal(CodeNotReady, "the b2b service cannot be set up without a repository")
	}
	if opts.Links == nil {
		return nil, errors.Internal(CodeNotReady, "the b2b service cannot be set up without a link service")
	}
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: opts.Repo, links: opts.Links, log: log, now: now}, nil
}

// clock returns the current moment as UTC.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}
