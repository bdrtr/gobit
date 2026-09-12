package service_test

import (
	"context"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/models"
	"github.com/bdrtr/gobit/internal/modules/fulfillment/service"
)

// The tracking read (ADR 0149).
//
// # What every test here is really about
//
// Not the happy path. Asking a carrier where a parcel is has FIVE outcomes and
// four of them are silences of different kinds: the carrier disowns the label, the
// carrier cannot be asked at all, the carrier was asked and did not answer, and no
// label was ever opened. A surface that collapsed them would report the last four
// as a parcel that has not moved — which is the sentence an operator would repeat
// to a customer.
//
// So each outcome has its own test, and each asserts the ANSWER rather than the
// absence of provider fields: a carrier answering "pending" with no tracking
// number produces exactly the same empty fields as a carrier nobody could ask.

// trackingProvider is a provider that CAN be asked where a shipment is.
//
// The service's ordinary [fakeProvider] deliberately does NOT implement the
// capability — that is what makes the "unaskable" case testable at all — so this
// one wraps it and adds the one method.
type trackingProvider struct {
	*fakeProvider

	// update is what Track answers with.
	update coreprovider.TrackingUpdate
	// err, when set, is what Track fails with.
	err error
	// asked holds, in order, the shipment ids Track was called with.
	asked []string
}

// That it satisfies the optional capability is pinned here: the service asks for
// it with a type assertion, so a drifted signature would make this fake silently
// unaskable and every assertion below would still "pass" by reporting a silence.
var _ coreprovider.ShipmentTracker = (*trackingProvider)(nil)

// newTrackingProvider wraps the ordinary fake with a scripted answer.
func newTrackingProvider(id string, update coreprovider.TrackingUpdate) *trackingProvider {
	return &trackingProvider{fakeProvider: newFakeProvider(id), update: update}
}

// Track answers the scripted update and records what it was asked about.
func (p *trackingProvider) Track(
	_ context.Context, shipmentID string,
) (coreprovider.TrackingUpdate, error) {
	p.asked = append(p.asked, shipmentID)
	if p.err != nil {
		return coreprovider.TrackingUpdate{}, p.err
	}

	return p.update, nil
}

// trackingSetup builds a service whose single provider answers tracking reads,
// and returns the setup together with that provider.
func trackingSetup(t *testing.T, update coreprovider.TrackingUpdate) (testSetup, *trackingProvider) {
	t.Helper()

	setup := newSetup(t)
	tracker := newTrackingProvider(setup.provider.ID(), update)

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(tracker))

	svc, err := service.New(service.Options{
		Store:         setup.store,
		Providers:     registry,
		Clock:         func() time.Time { return testNow },
		DispatchBound: setup.bound,
		Events:        setup.events,
	})
	require.NoError(t, err)
	setup.svc = svc

	return setup, tracker
}

// carrierMoved is the answer a carrier that knows where the parcel is gives.
func carrierMoved() coreprovider.TrackingUpdate {
	return coreprovider.TrackingUpdate{
		Status:         coreprovider.FulfillmentShipped,
		TrackingNumber: "CARRIER-1",
		TrackingURL:    "https://carrier.example/CARRIER-1",
		Detail:         "held at depot",
		MovedAt:        testNow.Add(-2 * time.Hour),
	}
}

// TestTheCarriersAnswerIsReportedBesideTheModulesOwn is the decision.
//
// BOTH halves are asserted, and the provider's tracking number is deliberately
// different from the one the module holds: that difference is a label printed
// under one number and recorded under another, and it is the thing this endpoint
// can show that nothing else could.
func TestTheCarriersAnswerIsReportedBesideTheModulesOwn(t *testing.T) {
	setup, tracker := trackingSetup(t, carrierMoved())
	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-1")

	shipped, err := setup.svc.MarkShipped(context.Background(), ful.ID, "OPERATOR-1", "")
	require.NoError(t, err)

	tracking, err := setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, service.TrackingAnswered, tracking.Answer)
	assert.Empty(t, tracking.Reason, "an answered report has nothing to explain")

	assert.Equal(t, shipped.Status, tracking.LocalStatus)
	assert.Equal(t, "OPERATOR-1", tracking.LocalTrackingNumber,
		"the module's half is what an operator typed at dispatch")

	assert.Equal(t, models.StatusShipped, tracking.ProviderStatus)
	assert.Equal(t, "CARRIER-1", tracking.ProviderTrackingNumber,
		"the carrier's half is the carrier's own, and it may differ")
	assert.Equal(t, "held at depot", tracking.ProviderDetail)
	require.NotNil(t, tracking.ProviderMovedAt)
	assert.Equal(t, testNow.Add(-2*time.Hour).UTC(), *tracking.ProviderMovedAt)

	assert.False(t, tracking.TrackingNumbersAgree(),
		"two different numbers do not agree, and that is the finding")

	// The carrier is asked by ITS OWN identifier and not by the module's: a read
	// keyed on the wrong id would answer "unknown" for every parcel.
	assert.Equal(t, []string{ful.ExternalID}, tracker.asked)
}

// TestTrackingNumbersAgreeWhenTheTwoSidesMatch is the other direction of the same
// comparison.
func TestTrackingNumbersAgreeWhenTheTwoSidesMatch(t *testing.T) {
	update := carrierMoved()
	update.TrackingNumber = "SAME-1"

	setup, _ := trackingSetup(t, update)
	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-2")

	_, err := setup.svc.MarkShipped(context.Background(), ful.ID, "SAME-1", "")
	require.NoError(t, err)

	tracking, err := setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err)

	assert.True(t, tracking.TrackingNumbersAgree())
}

// TestAProviderWithNoTrackingIsUNASKABLEAndSaysSo is the ordinary case today.
//
// The provider that ships in the box is the only one that answers; a plugin's
// carrier may legitimately offer no tracking at all. What must not happen is that
// silence looking like a parcel which has not moved.
func TestAProviderWithNoTrackingIsUNASKABLEAndSaysSo(t *testing.T) {
	setup := newSetup(t)
	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-3")

	tracking, err := setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err, "a provider that cannot be asked is not an error")

	assert.Equal(t, service.TrackingUnaskable, tracking.Answer)
	assert.NotEmpty(t, tracking.Reason,
		"an operator reading a non-answer needs a sentence, not only a status word")
	assert.Empty(t, tracking.ProviderStatus, "nothing may be filled in for a silence")
	assert.False(t, tracking.TrackingNumbersAgree(),
		"agreement must not be readable out of a question nobody asked")

	// The local half is still reported: refusing the whole read because the
	// carrier cannot be asked would hide what the module does know.
	assert.Equal(t, ful.Status, tracking.LocalStatus)
}

// TestAgreementIsNotReadableOutOfASilence is the assertion a mutation asked for.
//
// The report says the two numbers do not agree whenever the carrier did not
// answer, and the ONLY fixture that can prove it is one whose local number is
// EMPTY: with a number on the module's side the two differ anyway, so a
// [ShipmentTracking.TrackingNumbersAgree] that had lost its guard would still
// answer false and every other test here would pass.
//
// That is the case a parcel is in before anybody types a number — the ordinary
// state of a freshly opened parcel at a carrier that reports its waybill later.
func TestAgreementIsNotReadableOutOfASilence(t *testing.T) {
	setup := newSetup(t)
	// The provider opens the label WITHOUT a tracking number, so the module's own
	// half is empty too.
	setup.provider.createWithoutTracking = true

	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-8")
	require.Empty(t, ful.TrackingNumber, "the fixture has to leave both sides empty")

	tracking, err := setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err)

	require.Equal(t, service.TrackingUnaskable, tracking.Answer)
	require.Empty(t, tracking.LocalTrackingNumber)
	require.Empty(t, tracking.ProviderTrackingNumber)

	assert.False(t, tracking.TrackingNumbersAgree(),
		"two empty numbers are not an agreement when NOBODY WAS ASKED: the carrier "+
			"never said what it holds, so there is nothing for the module's blank to "+
			"agree with")
}

// TestACarrierThatDisownsTheLabelIsItsOwnAnswer keeps a finding out of the error
// path.
//
// The module holds an identifier the carrier says is not its own, which is what a
// label opened against the wrong merchant account looks like from here. Folding it
// into "unreachable" would file a standing misconfiguration as a network blip.
func TestACarrierThatDisownsTheLabelIsItsOwnAnswer(t *testing.T) {
	setup, tracker := trackingSetup(t, carrierMoved())
	tracker.err = errors.NotFound("carrier_no_such_shipment", "no such shipment")

	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-4")

	tracking, err := setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, service.TrackingUnknown, tracking.Answer)
	assert.Contains(t, tracking.Reason, ful.ExternalID,
		"the report has to name the identifier the carrier disowned")
}

// TestACarrierThatCannotAnswerIsUNREACHABLE separates the retry from the repair.
func TestACarrierThatCannotAnswerIsUNREACHABLE(t *testing.T) {
	setup, tracker := trackingSetup(t, carrierMoved())
	tracker.err = errors.Unavailable("carrier_timeout", "the carrier did not answer in time")

	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-5")

	tracking, err := setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err,
		"a carrier that did not answer is a report, not a failure of this read")

	assert.Equal(t, service.TrackingUnreachable, tracking.Answer)
	assert.Contains(t, tracking.Reason, "did not answer in time",
		"the carrier's own words are what an operator can act on")
}

// TestAParcelWithNoLabelIsNOTOPENED is the fifth outcome.
//
// It is NOT "unknown": nobody disowned anything, there was simply never a label.
// A parcel a manual intervention left without a provider identifier looks like
// this, and so would one whose opening rolled back.
func TestAParcelWithNoLabelIsNOTOPENED(t *testing.T) {
	setup, _ := trackingSetup(t, carrierMoved())
	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-6")

	// The external identifier is taken away, which is the state the service's own
	// cancel path already guards against (see CancelFulfillment).
	setup.store.setExternalID(t, ful.ID, "")

	tracking, err := setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, service.TrackingNotOpened, tracking.Answer)
	assert.NotEmpty(t, tracking.Reason)
}

// TestTrackingAnUnknownParcelIsANotFound keeps a wrong identifier loud.
//
// A parcel that cannot be TRACKED is an answer; a parcel that does not EXIST is a
// 404. Answering "unaskable" for a mistyped id would tell an operator their
// carrier has no tracking.
func TestTrackingAnUnknownParcelIsANotFound(t *testing.T) {
	setup, _ := trackingSetup(t, carrierMoved())

	_, err := setup.svc.TrackShipment(context.Background(), "ful_01NOSUCHPARCEL000")

	require.Error(t, err)
	assert.True(t, errors.IsNotFound(err), "got %v", err)
}

// TestTheTrackingReadWritesNothing is the rule the whole file rests on.
//
// Which side is authoritative depends on the provider, so recording the carrier's
// answer would mean this module deciding on its own that a parcel moved. The
// carrier here says SHIPPED while the module has it pending; after the read the
// module must still have it pending.
func TestTheTrackingReadWritesNothing(t *testing.T) {
	setup, _ := trackingSetup(t, carrierMoved())
	option := readyOption(t, setup)
	ful := setup.createFulfillment(t, option, "key-track-7")

	before, err := setup.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)

	_, err = setup.svc.TrackShipment(context.Background(), ful.ID)
	require.NoError(t, err)

	after, err := setup.svc.GetFulfillment(context.Background(), ful.ID)
	require.NoError(t, err)

	assert.Equal(t, before.Status, after.Status,
		"the carrier's status must not be written onto the module's record")
	assert.Equal(t, before.TrackingNumber, after.TrackingNumber)
	assert.Equal(t, before.UpdatedAt, after.UpdatedAt,
		"a read that touched the row would move its updated_at")
}
