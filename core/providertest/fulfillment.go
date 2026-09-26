package providertest

import (
	"bytes"
	"context"
	"strconv"
	"time"

	"github.com/bdrtr/gobit/core/provider"
)

// Fulfillment runs the compliance suite for [provider.FulfillmentProvider]
// (ADR 0202).
//
// It calls [Identity] and then the three rules the contract states about a
// shipment, which it can check only by opening one:
//
//   - The destination is not returned. [provider.CreateFulfillmentInput]
//     says a provider hands it to the carrier and does not put it in
//     [provider.Fulfillment.Data]: this repository stores that data on the
//     parcel, and the parcel is not erased with the person (ADR 0194).
//   - Create with the same idempotency key returns the shipment it opened the
//     first time, and not a second one: a saga's retry would print a second
//     label.
//   - Cancel is idempotent: a shipment canceled twice does not fail the
//     second time, because the cancel is the saga's compensation.
//
// # It needs a provider that can open a shipment in a test
//
// Unlike [Classifier], nothing here is refused before a call, so the provider
// has to reach something: a stub of the carrier's service, which is how a
// provider's own tests usually run, or the carrier's sandbox. in is what that
// needs — the option, the reference, the data. The suite fills in the
// destination, every field a marker it can find again, and an idempotency key
// of its own, so a sandbox that keeps shipments between runs sees a new one.
//
// A provider that transforms the address before it returns it — upper case, an
// encoding, a hash — is not caught: the suite finds the markers as they were
// given.
func Fulfillment(t T, p provider.FulfillmentProvider, in provider.CreateFulfillmentInput) {
	t.Helper()

	Identity(t, p)

	if p == nil {
		return
	}

	ctx := context.Background()
	in.Destination = markedDestination()
	in.IdempotencyKey = "providertest-" + strconv.FormatInt(time.Now().UnixNano(), 36)

	first, err := p.Create(ctx, in)
	if err != nil {
		t.Errorf("Create failed, so no shipment rule could be checked: %v\n"+
			"The suite opens a real shipment; give it a provider that can, against a stub "+
			"of the carrier's service or its sandbox, and the option and data it needs.", err)

		return
	}
	if first.ID == "" {
		t.Errorf("Create answered with an empty ID; the parcel stores it to track and to " +
			"cancel the shipment, and an empty one names nothing")
	}
	reportEcho(t, "Create", first.Data)

	again, err := p.Create(ctx, in)
	switch {
	case err != nil:
		t.Errorf("Create failed the second time with the same idempotency key: %v\n"+
			"A saga retries a step, and the retry has to find the shipment the first "+
			"call opened.", err)
	case again.ID != first.ID:
		t.Errorf("Create opened %q and then %q under one idempotency key; a saga's retry "+
			"would print a second label", first.ID, again.ID)
	default:
		reportEcho(t, "Create, repeated,", again.Data)
	}

	if err := p.Cancel(ctx, first.ID); err != nil {
		t.Errorf("Cancel failed on the shipment Create opened: %v", err)

		return
	}
	if err := p.Cancel(ctx, first.ID); err != nil {
		t.Errorf("Cancel failed the second time: %v\n"+
			"The cancel is the saga's compensation and runs again on a retry; a "+
			"shipment already canceled is not an error.", err)
	}
}

// The markers the destination is filled with. Each is a word a carrier's
// answer has no reason to contain unless it was copied from the request.
const (
	markerFirstName  = "providertest-first-name"
	markerLastName   = "providertest-last-name"
	markerCompany    = "providertest-company"
	markerAddress1   = "providertest-address-1"
	markerAddress2   = "providertest-address-2"
	markerCity       = "providertest-city"
	markerProvince   = "providertest-province"
	markerPostalCode = "providertest-postal-code"
	markerPhone      = "providertest-phone"
	markerMetadata   = "providertest-metadata"
)

// markedDestination is a destination whose every field is a marker.
//
// The country is a real code rather than a marker: a carrier that validates it
// would refuse anything else, and two letters found in an answer would say
// nothing.
func markedDestination() *provider.Address {
	return &provider.Address{
		FirstName:   markerFirstName,
		LastName:    markerLastName,
		Company:     markerCompany,
		Address1:    markerAddress1,
		Address2:    markerAddress2,
		City:        markerCity,
		Province:    markerProvince,
		PostalCode:  markerPostalCode,
		CountryCode: "TR",
		Phone:       markerPhone,
		Metadata:    map[string]any{"note": markerMetadata},
	}
}

// reportEcho reports every marker the returned data carries.
func reportEcho(t T, call string, data []byte) {
	t.Helper()

	for _, marker := range []string{
		markerFirstName, markerLastName, markerCompany, markerAddress1, markerAddress2,
		markerCity, markerProvince, markerPostalCode, markerPhone, markerMetadata,
	} {
		if bytes.Contains(data, []byte(marker)) {
			t.Errorf("%s returned the destination in its data (%q); the parcel stores that "+
				"data and is not erased with the person, so the address would outlive the "+
				"erasure that empties it on the order (ADR 0194)", call, marker)
		}
	}
}
