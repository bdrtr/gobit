package service

import "context"

// This file is customer's CROSS-MODULE surface (ADR 0001).
//
// The signatures here use ONLY primitive and stdlib types. The reason is Go's
// structural conformance: the consuming module (cart, order, or a workflow)
// CANNOT import customer, and therefore cannot name a type such as
// models.Customer in its own interface — the moment it names one it becomes a
// different type in its own package and the concrete service does not satisfy
// it. Signatures written with primitive types, on the other hand, can be
// repeated verbatim in the consumer's own package:
//
//	// in the cart module, WITHOUT importing customer:
//	type CustomerReader interface {
//	    CustomerGroupIDs(ctx context.Context, customerID string) ([]string, error)
//	}
//	customers, err := container.Resolve[CustomerReader](c, "customer.service")
//
// The surface is deliberately kept NARROW: every method added here is a
// contract customer can never change again (the mismatch is caught not at
// compile time but at the moment of resolution from the container). If the
// whole of a field set is needed, the right path is not a new primitive method
// but the Query layer: the "customer" provider gives all of a record's fields
// and its group ids in a single call (ADR 0004).

// CustomerEmail returns the customer's e-mail address; errors.NotFound if the
// customer does not exist.
//
// Cart and order flows need a contact address even for a guest customer; this
// surface, which gives the e-mail on its own, makes it unnecessary for the
// consumer to bind to the whole of the customer model.
func (s *Service) CustomerEmail(ctx context.Context, customerID string) (string, error) {
	customer, err := s.GetCustomer(ctx, customerID)
	if err != nil {
		return "", err
	}
	return customer.Email, nil
}

// CustomerGroupIDs returns the ids of the groups the customer is a member of;
// errors.NotFound if the customer does not exist.
//
// The real consumer of this surface is the PRICE COMPUTATION: pricing's rule
// context looks at the "customer_group_id" attribute, and the customer's
// segments are placed into that context while a cart total is computed. The
// groups' names, their metadata or their creation time are not inputs to the
// computation; only the ids are carried.
//
// For a customer with no groups an empty (non-nil) slice is returned.
func (s *Service) CustomerGroupIDs(ctx context.Context, customerID string) ([]string, error) {
	groups, err := s.ListGroupsOf(ctx, customerID)
	if err != nil {
		return nil, err
	}

	ids := make([]string, 0, len(groups))
	for _, g := range groups {
		ids = append(ids, g.ID)
	}
	return ids, nil
}

// RegisterGuestCustomer opens a guest customer record and returns its ID.
//
// It exists so that a cart coming from the storefront can be bound to a
// customer without an account. It does the same work as
// [Service.RegisterGuest]; the difference is that its signature is primitive
// enough to be used across modules.
//
// A guest record already existing with the same e-mail is NOT an obstacle; for
// the rationale see internal/modules/customer/models, Customer.
func (s *Service) RegisterGuestCustomer(ctx context.Context, email, firstName, lastName, phone string) (string, error) {
	customer, err := s.RegisterGuest(ctx, CustomerInput{
		Email:     email,
		FirstName: firstName,
		LastName:  lastName,
		Phone:     phone,
	})
	if err != nil {
		return "", err
	}
	return customer.ID, nil
}
