package models

// HeldUnits are the units of one order line in the outgoing parcels opened for
// the order, in the two figures a held count needs (ADR 0423).
type HeldUnits struct {
	// Live are the units of the pending, shipped and delivered parcels, and of
	// a parcel that came back before ADR 0423 ([Fulfillment.HeldWhole]).
	Live int64
	// Back are the units of the other parcels that came back to the sender
	// undelivered ([StatusReturned]).
	Back int64
}

// Held is how many of the line's units its parcels hold when a return or a
// replacement speaks for spoken of them (ADR 0423): every live unit, and of the
// units that came back, as many as are spoken for. This is the one place the
// rule is spelled.
//
// # Why a unit that came back is held only so far
//
// It is on the shelf again, deducted at the checkout and recorded nowhere. When
// a return asks it back, the return's receipt restocks it and a refund or an
// exchange settles the buyer; when a replacement sends goods in its place, the
// buyer has them. Either way it stays held and is not owed a second time. When
// neither speaks for it, the order owes it again: a new parcel may take it and
// a write-off puts it back on the shelf.
//
// Neither says which units it speaks for, so on a line whose returns or
// replacements also cover delivered goods the units that came back are counted
// as spoken for first: as many of them as are spoken for stay held, the count
// every line kept before ADR 0423.
func (h HeldUnits) Held(spoken int64) int64 {
	return h.Live + min(h.Back, max(spoken, 0))
}
