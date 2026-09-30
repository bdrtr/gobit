package fulfillment

import "github.com/bdrtr/gobit/core/personaldata"

// PersonalData declares what a parcel's records may hold about a person
// (ADR 0278).
//
// The module keeps no address: the order holds it and hands it to the
// provider (ADR 0194). What it does keep is a parcel's tracking number and
// link, which the carrier resolves to the addressee, and free values nobody
// here reads: the caller's data and metadata and the replay key it chose. A
// parcel names its order, not a person, so the module cannot attribute a row
// and offers no disclosure and no erasure; the sweep reports it as keeping
// what is listed here.
func (m *Module) PersonalData() personaldata.Declaration {
	holdings := make([]personaldata.Holding, 0, len(parcelColumns)*2)
	for _, table := range []string{tableFulfillments, tableManualShipments} {
		for _, column := range parcelColumns {
			holdings = append(holdings, personaldata.Holding{
				Table: table, Column: column.name, Kind: personaldata.Open,
				Why: column.why, OnErasure: personaldata.Kept,
			})
		}
	}
	holdings = append(holdings, personaldata.Holding{
		Table: tableFulfillments, Column: "metadata", Kind: personaldata.Open,
		Why:       "the caller's own data on the parcel",
		OnErasure: personaldata.Kept,
	})

	return personaldata.Declaration{Holder: ModuleName, Holdings: holdings}
}

// The two tables a parcel is recorded in: the module's, and the manual
// provider's own copy of it.
const (
	tableFulfillments    = "fulfillments"
	tableManualShipments = "fulfillment_manual_shipments"
)

// parcelColumns are declared on both tables, which keep the same values.
var parcelColumns = []struct{ name, why string }{
	{"data", "the data the caller handed the provider for the parcel, stored whole and never read"},
	{"idempotency_key", "the replay key the caller chose for the parcel; gobit neither builds it nor reads it"},
	{"tracking_number", "the carrier's number for the parcel, which the carrier resolves to its addressee"},
	{"tracking_url", "the carrier's link that follows the parcel to its addressee"},
}

var _ personaldata.Declarer = (*Module)(nil)
