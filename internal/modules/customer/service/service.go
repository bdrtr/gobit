// Package service holds the business logic of the customer module.
//
// # Cross-module surface (ADR 0001)
//
// customer IMPORTS no module and READS data from no module; there is therefore
// no consumer-side interface in this package. The reverse direction exists:
// cart (Phase 5) and order (Phase 6) need the customer. So that side can define
// a narrow interface in its own package, customer's surface is split IN TWO:
//
//   - The rich in-module surface — it uses the [models] types
//     ([Service.CreateCustomer], [Service.ListAddresses] …). These methods are
//     called only by customer's own API layer and its query provider.
//   - The cross-module surface — it uses ONLY primitive and stdlib types
//     (see interop.go).
//
// The split is mandatory: structural conformance in Go demands signature
// EQUALITY. Because the consuming module cannot import customer, it cannot name
// a type such as [models.Customer] in its signature; the moment it names one it
// becomes a different type in its own package and the concrete service does not
// satisfy it.
//
// # Guest and account
//
// The module's most important decision is that e-mail uniqueness is NOT
// ENFORCED for guests; its rationale is written in the [models.Customer] godoc.
// The service does not repeat this rule — the rule is in the partial unique
// index in the database, and had it been repeated the race between two
// concurrent records would still be resolved by the index. The service's job is
// to turn the conflict the index produces into an UNDERSTANDABLE error.
package service

import (
	"context"
	"log/slog"
	"time"

	"github.com/bdrtr/gobit/core/errors"
	corepage "github.com/bdrtr/gobit/internal/core/page"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
)

// Error codes; the calling side can look at these with errors.CodeOf.
const (
	// CodeInvalidInput reports that the input did not pass validation.
	CodeInvalidInput = "customer_invalid_input"
	// CodeCustomerNotFound reports that the requested customer was not found.
	CodeCustomerNotFound = "customer_not_found"
)

// Pagination limits. If no limit is given the default is applied; an
// excessively large limit is rejected, so the client cannot scan the database
// in a single request.
const (
	// DefaultLimit is the page size applied when no limit is given.
	DefaultLimit int64 = 50
	// MaxLimit is the largest page size that can be requested in a single
	// request.
	MaxLimit int64 = 100
)

// Page is a paginated list result.
//
// Limit and Offset are not the request's raw values but the APPLIED values; the
// API envelope writes these fields as they are, so the client is aware of a
// limit that fell back to the default.
type Page[T any] struct {
	// Items are the records on the current page.
	Items []T
	// Count is the TOTAL number of records matching the filter (not the page
	// size).
	Count int64
	// Limit is the applied page size.
	Limit int64
	// Offset is the applied number of skipped records.
	Offset int64
	// NextCursor is the opaque position the NEXT page starts below; empty means
	// this page is the last one.
	//
	// The emptiness is the end-of-listing signal: a cursor that always came back
	// would make a client walk one extra request into an empty page before it
	// could tell it was done.
	NextCursor string
}

// Repository is the data access surface the service needs.
//
// The interface is defined on the CONSUMING side (here); the concrete
// implementation is in the internal/modules/customer/repository package. This
// is the IN-MODULE counterpart of ADR 0001's pattern and it lets the service be
// tested without a database.
type Repository interface {
	CreateCustomer(ctx context.Context, c models.Customer) (models.Customer, error)
	GetCustomer(ctx context.Context, id string) (models.Customer, error)
	GetAccountByEmail(ctx context.Context, email string) (models.Customer, error)
	ListCustomers(ctx context.Context, filter models.CustomerFilter, limit, offset int64) ([]models.Customer, int64, error)
	GetCustomersByIDs(ctx context.Context, ids []string) ([]models.Customer, error)
	UpdateCustomer(ctx context.Context, id string, patch models.CustomerPatch, now time.Time) (models.Customer, error)
	PromoteGuest(ctx context.Context, id string, now time.Time) (models.Customer, error)
	DeleteCustomer(ctx context.Context, id string, now time.Time) error

	CreateGroup(ctx context.Context, g models.CustomerGroup) (models.CustomerGroup, error)
	GetGroup(ctx context.Context, id string) (models.CustomerGroup, error)
	ListGroups(ctx context.Context, limit, offset int64) ([]models.CustomerGroup, int64, error)
	UpdateGroup(ctx context.Context, id string, patch models.CustomerGroupPatch, now time.Time) (models.CustomerGroup, error)
	DeleteGroup(ctx context.Context, id string, now time.Time) error
	AddToGroup(ctx context.Context, customerID, groupID string, now time.Time) error
	RemoveFromGroup(ctx context.Context, customerID, groupID string) error
	ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error)
	GroupIDsOfCustomers(ctx context.Context, customerIDs []string) (map[string][]string, error)

	CreateAddress(ctx context.Context, a models.CustomerAddress) (models.CustomerAddress, error)
	GetAddress(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)
	ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error)
	UpdateAddress(ctx context.Context, customerID, addressID string, patch models.AddressPatch, now time.Time) (models.CustomerAddress, error)
	DeleteAddress(ctx context.Context, customerID, addressID string, now time.Time) error
	SetDefaultAddress(ctx context.Context, customerID, addressID string, kind models.DefaultKind, now time.Time) (models.CustomerAddress, error)
}

// Options are the service's setup settings.
type Options struct {
	// Logger is the structural log target; if nil the logs are discarded.
	Logger *slog.Logger
	// Now is the time source; if nil time.Now is used. Tests fill this in with a
	// fixed clock to make the time-dependent branches deterministic.
	Now func() time.Time
}

// Service is the customer module's public service. It is safe for concurrent
// use.
type Service struct {
	repo Repository
	log  *slog.Logger
	now  func() time.Time
}

// New produces a service working over the given repository.
//
// If repo is nil this is reported as a typed error not at setup but on the
// first call; the setup path produces no panic.
func New(repo Repository, opts Options) *Service {
	log := opts.Logger
	if log == nil {
		log = slog.New(slog.DiscardHandler)
	}
	now := opts.Now
	if now == nil {
		now = time.Now
	}
	return &Service{repo: repo, log: log, now: now}
}

// ready verifies that the repository is set up.
func (s *Service) ready() error {
	if s == nil || s.repo == nil {
		return errors.Unavailable("customer_service_unconfigured", "the customer service is not configured")
	}
	return nil
}

// clock returns the current moment as UTC.
func (s *Service) clock() time.Time {
	return s.now().UTC()
}

// CustomerInput is the write input of a customer.
//
// It is used both in account creation ([Service.CreateCustomer]) and in guest
// registration ([Service.RegisterGuest]); what separates the two is not the
// input but the METHOD CALLED. Not carrying the distinction in a boolean field
// is deliberate: such a field would let a request arriving at the admin
// endpoint silently open a guest record.
type CustomerInput struct {
	// Email is the customer's e-mail address; it is required and stored folded
	// to lower case.
	Email string
	// FirstName is the customer's first name; it can be left empty.
	FirstName string
	// LastName is the customer's last name; it can be left empty.
	LastName string
	// Phone is the customer's phone; it can be left empty.
	Phone string
	// Metadata is free structural context; it can be left empty.
	Metadata map[string]any
}

// CreateCustomer creates a REGISTERED customer account.
//
// If the e-mail already belongs to an account errors.Conflict is returned. For
// a guest record [Service.RegisterGuest] is used; the difference between the
// two is the [models.Customer.HasAccount] field, and that also determines the
// uniqueness rule.
func (s *Service) CreateCustomer(ctx context.Context, in CustomerInput) (models.Customer, error) {
	return s.createCustomer(ctx, in, true)
}

// RegisterGuest creates a GUEST customer record.
//
// A guest record or a registered account already existing with the same e-mail
// is NOT an obstacle: a guest record is not an identity but the contact
// information of a one-off purchase (for the rationale see [models.Customer]).
// The storefront therefore cannot turn the customer away with "this e-mail is
// in use".
func (s *Service) RegisterGuest(ctx context.Context, in CustomerInput) (models.Customer, error) {
	return s.createCustomer(ctx, in, false)
}

// createCustomer is the common body of the two record paths.
func (s *Service) createCustomer(ctx context.Context, in CustomerInput, hasAccount bool) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}

	email, err := normalizeEmail(in.Email)
	if err != nil {
		return models.Customer{}, err
	}
	if err := validatePerson(in.FirstName, in.LastName, in.Phone); err != nil {
		return models.Customer{}, err
	}

	now := s.clock()
	created, err := s.repo.CreateCustomer(ctx, models.Customer{
		ID:         models.NewCustomerID(now),
		Email:      email,
		FirstName:  in.FirstName,
		LastName:   in.LastName,
		Phone:      in.Phone,
		HasAccount: hasAccount,
		Metadata:   in.Metadata,
		CreatedAt:  now,
	})
	if err != nil {
		return models.Customer{}, err
	}

	// The e-mail is sensitive data and is not logged (plan Section 8); the id and
	// the record kind are enough to trace a call.
	s.log.DebugContext(ctx, "customer created",
		slog.String("customer_id", created.ID),
		slog.Bool("has_account", created.HasAccount),
	)
	return created, nil
}

// GetCustomer returns the customer by id; errors.NotFound if it does not exist.
func (s *Service) GetCustomer(ctx context.Context, id string) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}
	if err := requireID(id, models.CustomerIDPrefix, "customer id"); err != nil {
		return models.Customer{}, err
	}
	return s.repo.GetCustomer(ctx, id)
}

// GetCustomerByEmail returns the REGISTERED account by e-mail; errors.NotFound
// if it does not exist.
//
// Guest records are deliberately left out: because there can be several guests
// with the same e-mail, the question has no single right answer among the
// guests. A caller who wants to see the guest records uses
// [Service.ListCustomers] with the e-mail filter.
func (s *Service) GetCustomerByEmail(ctx context.Context, email string) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}
	normalized, err := normalizeEmail(email)
	if err != nil {
		return models.Customer{}, err
	}
	return s.repo.GetAccountByEmail(ctx, normalized)
}

// ListCustomersInput is the input of the customer listing.
type ListCustomersInput struct {
	// Email, when given, returns only the customers holding this e-mail address
	// (guests included). The value is applied normalized.
	Email *string
	// HasAccount, when given, filters by the guest/registered distinction.
	HasAccount *bool
	// GroupID, when given, returns only the members of this group.
	GroupID *string
	// Limit is the page size; if 0 [DefaultLimit] is applied.
	Limit int64
	// Offset is the number of records to skip.
	Offset int64
	// After is the opaque position from a previous page's NextCursor; the zero
	// value is the first page.
	//
	// It is what makes a deep page cheap: offset asks the database to walk and
	// DISCARD every row it skips, so its cost grows with depth, while a cursor
	// goes into the index condition and stays flat.
	After corepage.Cursor
}

// ListCustomers returns the filtered and paginated customer list.
func (s *Service) ListCustomers(ctx context.Context, in ListCustomersInput) (Page[models.Customer], error) {
	if err := s.ready(); err != nil {
		return Page[models.Customer]{}, err
	}

	limit, offset, err := normalizePaging(in.Limit, in.Offset)
	if err != nil {
		return Page[models.Customer]{}, err
	}

	filter := models.CustomerFilter{HasAccount: in.HasAccount}
	if in.Email != nil {
		normalized, emailErr := normalizeEmail(*in.Email)
		if emailErr != nil {
			return Page[models.Customer]{}, emailErr
		}
		filter.Email = &normalized
	}
	if in.GroupID != nil {
		if idErr := requireID(*in.GroupID, models.CustomerGroupIDPrefix, "group id"); idErr != nil {
			return Page[models.Customer]{}, idErr
		}
		filter.GroupID = in.GroupID
	}

	filter.After = in.After

	// One row MORE than asked for is fetched and the extra one is dropped below:
	// that is how "is there a next page" is answered without a second query.
	items, total, err := s.repo.ListCustomers(ctx, filter, limit+1, offset)
	if err != nil {
		return Page[models.Customer]{}, err
	}

	result := Page[models.Customer]{Items: items, Count: total, Limit: limit, Offset: offset}
	if int64(len(items)) > limit {
		result.Items = items[:limit]
		last := result.Items[len(result.Items)-1]
		result.NextCursor = corepage.Encode(CustomerListing,
			corepage.Cursor{Time: last.CreatedAt, ID: last.ID})
	}

	return result, nil
}

// CustomerListing names the customer listing inside a cursor.
//
// A cursor carries the name of the listing it belongs to so that one handed to
// a different listing is REFUSED rather than silently selecting the wrong rows.
const CustomerListing = "customers"

// UpdateCustomerInput is the partial update input of a customer.
//
// A nil field means "do not touch", a non-nil field means "write this value".
type UpdateCustomerInput struct {
	// Email is the new e-mail; it is written normalized.
	Email *string
	// FirstName is the new first name.
	FirstName *string
	// LastName is the new last name.
	LastName *string
	// Phone is the new phone.
	Phone *string
	// Metadata is the new metadata map; it replaces the whole column.
	Metadata map[string]any
}

// UpdateCustomer updates the given fields of the customer.
//
// If a registered account's e-mail is in use by another account
// errors.Conflict is returned. [models.Customer.HasAccount] CANNOT BE CHANGED
// here; for the guest-to-account transition [Service.ConvertGuestToAccount] is
// used.
func (s *Service) UpdateCustomer(ctx context.Context, id string, in UpdateCustomerInput) (models.Customer, error) {
	if err := s.ready(); err != nil {
		return models.Customer{}, err
	}
	if err := requireID(id, models.CustomerIDPrefix, "customer id"); err != nil {
		return models.Customer{}, err
	}

	patch := models.CustomerPatch{
		FirstName: in.FirstName,
		LastName:  in.LastName,
		Phone:     in.Phone,
		Metadata:  in.Metadata,
	}
	if in.Email != nil {
		normalized, err := normalizeEmail(*in.Email)
		if err != nil {
			return models.Customer{}, err
		}
		patch.Email = &normalized
	}
	if err := validatePatchPerson(patch); err != nil {
		return models.Customer{}, err
	}

	return s.repo.UpdateCustomer(ctx, id, patch, s.clock())
}

// DeleteCustomer deletes the customer and its addresses with a soft delete.
func (s *Service) DeleteCustomer(ctx context.Context, id string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(id, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}
	return s.repo.DeleteCustomer(ctx, id, s.clock())
}

// ConvertGuestToAccount converts a guest record into a registered account.
//
// If the record's e-mail already belongs to a registered account
// errors.Conflict is returned: two accounts sharing the same e-mail would have
// meant the "log in with e-mail" arriving in Phase 8 cannot know which record
// to pick. If the record is already an account errors.Conflict is returned as
// well; a silent no-op would tell the caller the conversion had happened.
//
// The decision is made WHILE the customer row is LOCKED and the partial unique
// index remains the last gate (see repository.Repo.PromoteGuest).
func (s *Service) ConvertGuestToAccount(ctx context.Context, customerID string) error {
	if err := s.ready(); err != nil {
		return err
	}
	if err := requireID(customerID, models.CustomerIDPrefix, "customer id"); err != nil {
		return err
	}

	converted, err := s.repo.PromoteGuest(ctx, customerID, s.clock())
	if err != nil {
		return err
	}

	s.log.InfoContext(ctx, "guest converted to account",
		slog.String("customer_id", converted.ID),
	)
	return nil
}
