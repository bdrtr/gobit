package api_test

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/customer/api"
	"github.com/bdrtr/gobit/internal/modules/customer/models"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
)

// stubCustomer is a scriptable implementation of [api.Customer] for the tests.
//
// The HTTP layer's tests exercise the transport, not the service's BUSINESS
// LOGIC: routing, body decoding, envelope shape and the translation of an error
// kind into a status code. That is why a scriptable fake stands in for the
// service; the typed error the test wants returned is handed over directly and
// the expected status code is measured.
//
// Calling a method that was not scripted returns a typed error: a silent zero
// value would let the test pass for the wrong reason.
type stubCustomer struct {
	createCustomerFn  func(ctx context.Context, in service.CustomerInput) (models.Customer, error)
	registerGuestFn   func(ctx context.Context, in service.CustomerInput) (models.Customer, error)
	getCustomerFn     func(ctx context.Context, id string) (models.Customer, error)
	listCustomersFn   func(ctx context.Context, in service.ListCustomersInput) (service.Page[models.Customer], error)
	updateCustomerFn  func(ctx context.Context, id string, in service.UpdateCustomerInput) (models.Customer, error)
	deleteCustomerFn  func(ctx context.Context, id string) error
	convertGuestFn    func(ctx context.Context, customerID string) error
	createGroupFn     func(ctx context.Context, in service.GroupInput) (models.CustomerGroup, error)
	getGroupFn        func(ctx context.Context, id string) (models.CustomerGroup, error)
	listGroupsFn      func(ctx context.Context, limit, offset int64) (service.Page[models.CustomerGroup], error)
	updateGroupFn     func(ctx context.Context, id string, in service.UpdateGroupInput) (models.CustomerGroup, error)
	deleteGroupFn     func(ctx context.Context, id string) error
	addToGroupFn      func(ctx context.Context, customerID, groupID string) error
	removeFromGroupFn func(ctx context.Context, customerID, groupID string) error
	listGroupsOfFn    func(ctx context.Context, customerID string) ([]models.CustomerGroup, error)
	setSegmentFn      func(ctx context.Context, groupID string, rule models.SegmentRule) (models.CustomerGroup, error)
	clearSegmentFn    func(ctx context.Context, groupID string) (models.CustomerGroup, error)
	createAddressFn   func(ctx context.Context, customerID string, in service.AddressInput) (models.CustomerAddress, error)
	listAddressesFn   func(ctx context.Context, customerID string) ([]models.CustomerAddress, error)
	updateAddressFn   func(ctx context.Context, customerID, addressID string, in service.UpdateAddressInput) (models.CustomerAddress, error)
	deleteAddressFn   func(ctx context.Context, customerID, addressID string) error
	setDefaultShipFn  func(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)
	setDefaultBillFn  func(ctx context.Context, customerID, addressID string) (models.CustomerAddress, error)

	// The wishlist's methods are in wishlist_test.go.
	saveToWishlistFn     func(ctx context.Context, customerID, variantID string) (models.WishlistItem, error)
	listWishlistFn       func(ctx context.Context, customerID string) ([]models.WishlistItem, error)
	removeFromWishlistFn func(ctx context.Context, customerID, variantID string) error
	markStockAlertFn     func(ctx context.Context, customerID, variantID string, channels []string) (models.WishlistItem, error)
	unmarkStockAlertFn   func(ctx context.Context, customerID, variantID string) error
	markPriceAlertFn     func(ctx context.Context, customerID, variantID, regionID string, channels []string) (models.WishlistItem, error)
	unmarkPriceAlertFn   func(ctx context.Context, customerID, variantID string) error

	// The arguments of the last call; they prove the handler passed on the
	// right values.
	lastCustomerID string
	lastGroupID    string
	lastAddressID  string
	lastInput      service.CustomerInput
	lastListInput  service.ListCustomersInput
}

var _ api.Customer = (*stubCustomer)(nil)

// unset is the error returned when a method that was not scripted is called.
func unset(name string) error {
	return errors.Internal("stub_unset", "%s was not scripted in the test", name)
}

func (s *stubCustomer) CreateCustomer(ctx context.Context, in service.CustomerInput) (models.Customer, error) {
	s.lastInput = in
	if s.createCustomerFn == nil {
		return models.Customer{}, unset("CreateCustomer")
	}
	return s.createCustomerFn(ctx, in)
}

func (s *stubCustomer) RegisterGuest(ctx context.Context, in service.CustomerInput) (models.Customer, error) {
	s.lastInput = in
	if s.registerGuestFn == nil {
		return models.Customer{}, unset("RegisterGuest")
	}
	return s.registerGuestFn(ctx, in)
}

func (s *stubCustomer) GetCustomer(ctx context.Context, id string) (models.Customer, error) {
	s.lastCustomerID = id
	if s.getCustomerFn == nil {
		return models.Customer{}, unset("GetCustomer")
	}
	return s.getCustomerFn(ctx, id)
}

func (s *stubCustomer) ListCustomers(
	ctx context.Context,
	in service.ListCustomersInput,
) (service.Page[models.Customer], error) {
	s.lastListInput = in
	if s.listCustomersFn == nil {
		return service.Page[models.Customer]{}, unset("ListCustomers")
	}
	return s.listCustomersFn(ctx, in)
}

func (s *stubCustomer) UpdateCustomer(
	ctx context.Context,
	id string,
	in service.UpdateCustomerInput,
) (models.Customer, error) {
	s.lastCustomerID = id
	if s.updateCustomerFn == nil {
		return models.Customer{}, unset("UpdateCustomer")
	}
	return s.updateCustomerFn(ctx, id, in)
}

func (s *stubCustomer) DeleteCustomer(ctx context.Context, id string) error {
	s.lastCustomerID = id
	if s.deleteCustomerFn == nil {
		return unset("DeleteCustomer")
	}
	return s.deleteCustomerFn(ctx, id)
}

func (s *stubCustomer) ConvertGuestToAccount(ctx context.Context, customerID string) error {
	s.lastCustomerID = customerID
	if s.convertGuestFn == nil {
		return unset("ConvertGuestToAccount")
	}
	return s.convertGuestFn(ctx, customerID)
}

func (s *stubCustomer) CreateGroup(ctx context.Context, in service.GroupInput) (models.CustomerGroup, error) {
	if s.createGroupFn == nil {
		return models.CustomerGroup{}, unset("CreateGroup")
	}
	return s.createGroupFn(ctx, in)
}

func (s *stubCustomer) GetGroup(ctx context.Context, id string) (models.CustomerGroup, error) {
	s.lastGroupID = id
	if s.getGroupFn == nil {
		return models.CustomerGroup{}, unset("GetGroup")
	}
	return s.getGroupFn(ctx, id)
}

func (s *stubCustomer) ListGroups(
	ctx context.Context,
	limit, offset int64,
) (service.Page[models.CustomerGroup], error) {
	if s.listGroupsFn == nil {
		return service.Page[models.CustomerGroup]{}, unset("ListGroups")
	}
	return s.listGroupsFn(ctx, limit, offset)
}

func (s *stubCustomer) UpdateGroup(
	ctx context.Context,
	id string,
	in service.UpdateGroupInput,
) (models.CustomerGroup, error) {
	s.lastGroupID = id
	if s.updateGroupFn == nil {
		return models.CustomerGroup{}, unset("UpdateGroup")
	}
	return s.updateGroupFn(ctx, id, in)
}

func (s *stubCustomer) DeleteGroup(ctx context.Context, id string) error {
	s.lastGroupID = id
	if s.deleteGroupFn == nil {
		return unset("DeleteGroup")
	}
	return s.deleteGroupFn(ctx, id)
}

func (s *stubCustomer) AddToGroup(ctx context.Context, customerID, groupID string) error {
	s.lastCustomerID, s.lastGroupID = customerID, groupID
	if s.addToGroupFn == nil {
		return unset("AddToGroup")
	}
	return s.addToGroupFn(ctx, customerID, groupID)
}

func (s *stubCustomer) RemoveFromGroup(ctx context.Context, customerID, groupID string) error {
	s.lastCustomerID, s.lastGroupID = customerID, groupID
	if s.removeFromGroupFn == nil {
		return unset("RemoveFromGroup")
	}
	return s.removeFromGroupFn(ctx, customerID, groupID)
}

func (s *stubCustomer) ListGroupsOf(ctx context.Context, customerID string) ([]models.CustomerGroup, error) {
	s.lastCustomerID = customerID
	if s.listGroupsOfFn == nil {
		return nil, unset("ListGroupsOf")
	}
	return s.listGroupsOfFn(ctx, customerID)
}

func (s *stubCustomer) SetGroupSegment(
	ctx context.Context, groupID string, rule models.SegmentRule,
) (models.CustomerGroup, error) {
	s.lastGroupID = groupID
	if s.setSegmentFn == nil {
		return models.CustomerGroup{}, unset("SetGroupSegment")
	}
	return s.setSegmentFn(ctx, groupID, rule)
}

func (s *stubCustomer) ClearGroupSegment(ctx context.Context, groupID string) (models.CustomerGroup, error) {
	s.lastGroupID = groupID
	if s.clearSegmentFn == nil {
		return models.CustomerGroup{}, unset("ClearGroupSegment")
	}
	return s.clearSegmentFn(ctx, groupID)
}

func (s *stubCustomer) CreateAddress(
	ctx context.Context,
	customerID string,
	in service.AddressInput,
) (models.CustomerAddress, error) {
	s.lastCustomerID = customerID
	if s.createAddressFn == nil {
		return models.CustomerAddress{}, unset("CreateAddress")
	}
	return s.createAddressFn(ctx, customerID, in)
}

func (s *stubCustomer) ListAddresses(ctx context.Context, customerID string) ([]models.CustomerAddress, error) {
	s.lastCustomerID = customerID
	if s.listAddressesFn == nil {
		return nil, unset("ListAddresses")
	}
	return s.listAddressesFn(ctx, customerID)
}

func (s *stubCustomer) UpdateAddress(
	ctx context.Context,
	customerID, addressID string,
	in service.UpdateAddressInput,
) (models.CustomerAddress, error) {
	s.lastCustomerID, s.lastAddressID = customerID, addressID
	if s.updateAddressFn == nil {
		return models.CustomerAddress{}, unset("UpdateAddress")
	}
	return s.updateAddressFn(ctx, customerID, addressID, in)
}

func (s *stubCustomer) DeleteAddress(ctx context.Context, customerID, addressID string) error {
	s.lastCustomerID, s.lastAddressID = customerID, addressID
	if s.deleteAddressFn == nil {
		return unset("DeleteAddress")
	}
	return s.deleteAddressFn(ctx, customerID, addressID)
}

func (s *stubCustomer) SetDefaultShippingAddress(
	ctx context.Context,
	customerID, addressID string,
) (models.CustomerAddress, error) {
	s.lastCustomerID, s.lastAddressID = customerID, addressID
	if s.setDefaultShipFn == nil {
		return models.CustomerAddress{}, unset("SetDefaultShippingAddress")
	}
	return s.setDefaultShipFn(ctx, customerID, addressID)
}

func (s *stubCustomer) SetDefaultBillingAddress(
	ctx context.Context,
	customerID, addressID string,
) (models.CustomerAddress, error) {
	s.lastCustomerID, s.lastAddressID = customerID, addressID
	if s.setDefaultBillFn == nil {
		return models.CustomerAddress{}, unset("SetDefaultBillingAddress")
	}
	return s.setDefaultBillFn(ctx, customerID, addressID)
}
