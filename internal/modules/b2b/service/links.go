package service

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/internal/modules/b2b/models"
)

// The module and entity names on the link's sides.
//
// [link.LinkSide.Entity] is THE NAME under which Query LOOKS FOR A PROVIDER; it
// is not the module name (see core/query targetSide). The customer module
// registers its provider under the name "customer.query"; writing the module
// name instead of the entity name on the target side would mean errors.NotFound
// at run time. Here the two are the same ("customer"), but that is a
// coincidence, and the name is written out explicitly.
const (
	// ModuleName is this module's name; it is used on the link's sides to
	// state the owner.
	ModuleName = "b2b"
	// EntityEmployee is the entity name of the employee record.
	EntityEmployee = "b2b_employee"
	// EntityCustomer is the customer module's customer entity name.
	EntityCustomer = "customer"
)

// LinkEmployeeCustomer is the name of the link that binds an employee to a
// CUSTOMER record.
//
// The name is a CROSS-MODULE CONTRACT: it is written to the persistent
// definition ledger, and changing it produces errors.Conflict at startup (see
// link.LinkService.Define).
const LinkEmployeeCustomer = "b2b_employee_customer"

// Definitions are the link definitions the b2b module declares.
//
// # Why the cardinality is OneToOne
//
// Uniqueness is required on both sides, and both are intentional:
//
//   - The From (employee) side: an employee record belongs to a single
//     customer. Otherwise a single spending limit would be shared by several
//     people, and it could not be said on whose behalf the limit was used up.
//   - The To (customer) side: a customer is an employee of at most ONE
//     company. The storefront has to be able to resolve the "my own company"
//     question to a single answer; in a model with two answers it stays
//     unclear whose company's limit applies, and that unclarity means the
//     spending rule is not applied.
//
// The constraint lives not in the application but in the DATABASE (the link
// table's unique indexes): a "read first, then write" check would not hold
// between two concurrent requests adding the same customer to two companies.
//
// The price of this is that the bond MUST be removed too when an employee
// record is deleted; a remaining bond means the customer can never again be
// added to any company (see [Service.DeleteEmployee]).
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name:        LinkEmployeeCustomer,
			From:        link.LinkSide{Module: ModuleName, Entity: EntityEmployee, Field: "employee_id"},
			To:          link.LinkSide{Module: EntityCustomer, Entity: EntityCustomer, Field: "customer_id"},
			Cardinality: link.OneToOne,
		},
	}
}

// linkCustomer binds an employee to a customer.
func (s *Service) linkCustomer(ctx context.Context, employeeID, customerID string) error {
	if err := s.links.Create(ctx, LinkEmployeeCustomer, employeeID, customerID); err != nil {
		return wrapLink(err, "the %q bond could not be established (employee: %s -> customer: %s)",
			LinkEmployeeCustomer, employeeID, customerID)
	}
	return nil
}

// unlinkCustomers removes the customer bonds of the given employees.
//
// It returns NO error; failures are logged as warnings: this call is made
// either after a deletion has completed (cleanup) or as the compensation of a
// failed creation. In both cases returning an error to the caller would point
// at the wrong reason — in the first it gives the impression that "the
// employee was not deleted", in the second it shadows the original error.
//
// A bond that cannot be cleaned up is NOT HARMLESS, and that is the difference
// from the similar case in the cart module: since the bond is unique, a
// dangling row means that customer can never again be added as an employee of
// any company. The read path is still safe — because the employee record is
// soft-deleted, the dangling bond resolves to no request (see
// [Service.MembershipOfCustomer]) — but the state has to stay visible in the
// log.
func (s *Service) unlinkCustomers(ctx context.Context, employeeIDs []string) {
	if len(employeeIDs) == 0 {
		return
	}

	bonds, err := s.links.ListMany(ctx, LinkEmployeeCustomer, employeeIDs)
	if err != nil {
		s.log.WarnContext(ctx, "the employees' customer bonds could not be read",
			"link", LinkEmployeeCustomer, "employee_ids", employeeIDs, "error", err)
		return
	}

	for employeeID, customerIDs := range bonds {
		for _, customerID := range customerIDs {
			if err := s.links.Delete(ctx, LinkEmployeeCustomer, employeeID, customerID); err != nil {
				s.log.WarnContext(ctx, "the employee's customer bond could not be removed",
					"link", LinkEmployeeCustomer, "employee_id", employeeID,
					"customer_id", customerID, "error", err)
			}
		}
	}
}

// attachCustomerIDs fills in the customer ids of employee records in ONE
// query.
//
// The ids live not in a column but in the link table; a separate query per
// record would be N+1 (what ADR 0004 forbids structurally). The field of an
// employee without a bond stays EMPTY, and that is a visible fault: a record
// whose bond could not be established or was corrupted by hand shows itself
// with an empty customer id.
func (s *Service) attachCustomerIDs(ctx context.Context, employees []models.CompanyEmployee) error {
	if len(employees) == 0 {
		return nil
	}

	ids := make([]string, 0, len(employees))
	for _, e := range employees {
		ids = append(ids, e.ID)
	}

	bonds, err := s.links.ListMany(ctx, LinkEmployeeCustomer, ids)
	if err != nil {
		return wrapLink(err, "the employees' customer bonds could not be read")
	}

	for i := range employees {
		if customerIDs := bonds[employees[i].ID]; len(customerIDs) > 0 {
			// The bond is unique (OneToOne); a second id cannot come about
			// because of the database constraint, so the first is taken.
			employees[i].CustomerID = customerIDs[0]
		}
	}
	return nil
}

// employeeIDOfCustomer returns the id of the employee bound to the customer;
// an empty string if there is no bond.
func (s *Service) employeeIDOfCustomer(ctx context.Context, customerID string) (string, error) {
	bonds, err := s.links.ListManyByTo(ctx, LinkEmployeeCustomer, []string{customerID})
	if err != nil {
		return "", wrapLink(err, "the customer's employee bond could not be read: %s", customerID)
	}

	employeeIDs := bonds[customerID]
	if len(employeeIDs) == 0 {
		return "", nil
	}
	// The bond is unique (OneToOne); a second id cannot come about because of
	// the database constraint.
	return employeeIDs[0], nil
}

// wrapLink wraps an error from the link service, PRESERVING ITS CLASS.
//
// Preserving the class is mandatory: a cardinality violation has to stay a
// Conflict (409), an undefined link name a NotFound (404); turning them all
// into Internal would show the client an error it can fix as if it were a
// server error. If the customer is already bound to another company, this is
// exactly what the client sees: 409.
func wrapLink(err error, format string, a ...any) error {
	return errors.Wrap(err, errors.KindOf(err), CodeLinkFailed, format, a...)
}
