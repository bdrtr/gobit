package api_test

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/api"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// fakeFulfillments is the testable counterpart of api.Fulfillments.
//
// It CONTAINS no business rule: every method returns a preset result and stores
// the input it received. That is the behavior of the HTTP layer under test —
// does it decode the body correctly, does it give the service the right input,
// does it translate the error into the right status code, and what does it NOT
// WRITE into the store response.
type fakeFulfillments struct {
	providerIDs []string

	profile     models.ShippingProfile
	profiles    []models.ShippingProfile
	option      models.ShippingOption
	options     []models.ShippingOption
	rule        models.ShippingOptionRule
	rules       []models.ShippingOptionRule
	quoted      []service.QuotedOption
	fulfillment models.Fulfillment
	list        []models.Fulfillment
	location    models.ShippingLocation
	locations   []models.ShippingLocation
	count       int64

	err error

	// The last inputs handed to the service; they are what proves the handler
	// did the translation right.
	lastOptionInput   service.CreateOptionInput
	lastListInput     service.ListOptionsInput
	lastCreateInput   service.CreateFulfillmentInput
	lastRuleInput     service.CreateRuleInput
	lastUpdateOption  service.UpdateOptionInput
	lastUpdateProfile service.UpdateProfileInput
	lastShipTracking  [2]string
	lastCanceledID    string
	lastLocationInput service.SetShippingLocationInput
	// lastReadLocation and lastDeletedLocation prove that the path parameter
	// gets from the handler to the service under the RIGHT name. Had they not
	// been recorded, chi would return an empty string when the parameter name
	// is misspelled, the service would produce a 422, and the test would not
	// notice unless it said "an error was expected".
	lastReadLocation    string
	lastDeletedLocation string
	// lastLocationPage is the paging that reaches the service from the listing
	// endpoint. If the handler parses the page but does NOT GIVE it to the
	// service, the response is still 200 and a test that only looks at the
	// status code could not see it.
	lastLocationPage service.Page
}

// That the fake service satisfies the surface the handlers expect is verified
// at compile time.
var _ api.Fulfillments = (*fakeFulfillments)(nil)

func (f *fakeFulfillments) ProviderIDs(_ context.Context) []string { return f.providerIDs }

func (f *fakeFulfillments) CreateShippingProfile(
	_ context.Context,
	_ service.CreateProfileInput,
) (models.ShippingProfile, error) {
	return f.profile, f.err
}

func (f *fakeFulfillments) GetShippingProfile(
	_ context.Context,
	_ string,
) (models.ShippingProfile, error) {
	return f.profile, f.err
}

func (f *fakeFulfillments) ListShippingProfiles(
	_ context.Context,
	_ service.ListProfilesInput,
) ([]models.ShippingProfile, int64, error) {
	return f.profiles, f.count, f.err
}

func (f *fakeFulfillments) UpdateShippingProfile(
	_ context.Context,
	_ string,
	in service.UpdateProfileInput,
) (models.ShippingProfile, error) {
	f.lastUpdateProfile = in
	return f.profile, f.err
}

func (f *fakeFulfillments) DeleteShippingProfile(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeFulfillments) CreateShippingOption(
	_ context.Context,
	in service.CreateOptionInput,
) (models.ShippingOption, error) {
	f.lastOptionInput = in
	return f.option, f.err
}

func (f *fakeFulfillments) GetShippingOption(
	_ context.Context,
	_ string,
) (models.ShippingOption, error) {
	return f.option, f.err
}

func (f *fakeFulfillments) ListShippingOptions(
	_ context.Context,
	_ service.ListOptionsAdminInput,
) ([]models.ShippingOption, int64, error) {
	return f.options, f.count, f.err
}

func (f *fakeFulfillments) UpdateShippingOption(
	_ context.Context,
	_ string,
	in service.UpdateOptionInput,
) (models.ShippingOption, error) {
	f.lastUpdateOption = in
	return f.option, f.err
}

func (f *fakeFulfillments) DeleteShippingOption(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeFulfillments) CreateShippingOptionRule(
	_ context.Context,
	_ string,
	in service.CreateRuleInput,
) (models.ShippingOptionRule, error) {
	f.lastRuleInput = in
	return f.rule, f.err
}

func (f *fakeFulfillments) ListShippingOptionRules(
	_ context.Context,
	_ string,
) ([]models.ShippingOptionRule, error) {
	return f.rules, f.err
}

func (f *fakeFulfillments) DeleteShippingOptionRule(_ context.Context, _ string) error {
	return f.err
}

func (f *fakeFulfillments) ListShippingOptionsFor(
	_ context.Context,
	in service.ListOptionsInput,
) ([]service.QuotedOption, error) {
	f.lastListInput = in
	return f.quoted, f.err
}

func (f *fakeFulfillments) CreateFulfillment(
	_ context.Context,
	in service.CreateFulfillmentInput,
) (models.Fulfillment, error) {
	f.lastCreateInput = in
	return f.fulfillment, f.err
}

func (f *fakeFulfillments) GetFulfillment(_ context.Context, _ string) (models.Fulfillment, error) {
	return f.fulfillment, f.err
}

func (f *fakeFulfillments) ListFulfillments(
	_ context.Context,
	_ service.ListFulfillmentsInput,
) ([]models.Fulfillment, int64, error) {
	return f.list, f.count, f.err
}

func (f *fakeFulfillments) CancelFulfillment(_ context.Context, id string) error {
	f.lastCanceledID = id
	return f.err
}

func (f *fakeFulfillments) MarkShipped(
	_ context.Context,
	_, trackingNumber, trackingURL string,
) (models.Fulfillment, error) {
	f.lastShipTracking = [2]string{trackingNumber, trackingURL}
	return f.fulfillment, f.err
}

func (f *fakeFulfillments) MarkDelivered(_ context.Context, _ string) (models.Fulfillment, error) {
	return f.fulfillment, f.err
}

// notFoundError is a typed not-found error used in the tests.
func notFoundError() error {
	return errors.NotFound("fulfillment_not_found", "fulfillment not found")
}

// conflictError is a typed conflict error used in the tests.
func conflictError() error {
	return errors.Conflict(service.CodeInvalidTransition, "a delivered fulfillment cannot be canceled")
}

func (f *fakeFulfillments) SetShippingLocation(
	_ context.Context,
	in service.SetShippingLocationInput,
) (models.ShippingLocation, error) {
	f.lastLocationInput = in
	return f.location, f.err
}

func (f *fakeFulfillments) GetShippingLocation(
	_ context.Context,
	id string,
) (models.ShippingLocation, error) {
	f.lastReadLocation = id
	return f.location, f.err
}

func (f *fakeFulfillments) ListShippingLocations(
	_ context.Context,
	page service.Page,
) ([]models.ShippingLocation, int64, error) {
	f.lastLocationPage = page
	return f.locations, f.count, f.err
}

func (f *fakeFulfillments) DeleteShippingLocation(_ context.Context, id string) error {
	f.lastDeletedLocation = id
	return f.err
}
