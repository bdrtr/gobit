package returns

import (
	"context"

	"github.com/bdrtr/gobit/core/errors"
)

// WithdrawResult reports what withdrawing a replacement did.
type WithdrawResult struct {
	// ReplacementID is the replacement taken back.
	ReplacementID string
	// ReleasedPromises is how many of its lines' promises were given back, one
	// per part of a line that replaces a bundle.
	ReleasedPromises int
}

// WithdrawReplacement takes back a replacement that has not left, and gives
// back the units its lines set aside (ADR 0237).
//
// # The promises go back BEFORE the record is withdrawn
//
// A dispatch sets a line's units aside and writes the promise on the line
// before it opens the parcel; one that stopped there — a later line refused, a
// parcel that would not open — leaves the promise standing for the retry. A
// withdrawal that only wrote the record left those units held for ever: the
// order module cannot reach the inventory, and nothing else ever released them
// (D159).
//
// The release comes first because it is the step that can say the goods have
// gone. A promise the inventory module refuses to release as CONFIRMED is one a
// dispatch took out of the count after opening its parcel, and died before it
// recorded the parcel; withdrawing that replacement would say the goods were
// never promised while they are in a box. It is refused, and the answer is to
// run the dispatch again, which finishes it. Nothing has been withdrawn then.
//
// # A refusal the order module can name comes first
//
// The record says, on the read above, whether the order module would take it
// back; a replacement a funded exchange holds money for is refused there,
// before any release (ADR 0432). An exchange funded between that read and the
// withdrawal is still refused by the order module after the release: the
// answer is the same 409, the units are back on the shelf, and the line keeps
// naming their released promise until the replacement is withdrawn.
//
// Every step repeats safely: a release of a released promise does nothing, and
// withdrawing a withdrawn record answers it as it stands. So a withdrawal that
// released the units and then failed to write the record is finished by asking
// again.
func (w *Workflows) WithdrawReplacement(ctx context.Context, replacementID string) (WithdrawResult, error) {
	detail, err := w.readReplacement(ctx, replacementID)
	if err != nil {
		return WithdrawResult{}, err
	}
	if detail.Status == statusReplacementDispatched {
		return WithdrawResult{}, errors.Conflict(CodeReplacementNotOpen,
			"replacement %s left in parcel %s; goods that have gone out are taken back by a return",
			replacementID, detail.FulfillmentID)
	}
	// The order module's own refusal is asked before any unit goes back: a
	// release followed by a refused record would leave the line naming a
	// promise that no longer holds, and the next dispatch would open a parcel
	// it cannot fill (ADR 0432).
	if detail.Withdrawable == nil {
		return WithdrawResult{}, errors.Internal(CodeReplacementUnreadable,
			"the order surface did not say whether replacement %s can be withdrawn; nothing was released",
			replacementID)
	}
	if !*detail.Withdrawable {
		return WithdrawResult{}, errors.Conflict(codeExchangeDifferenceHeld,
			"replacement %s answers %s %s, which is %s and holds the buyer's money for it; refund "+
				"the exchange's difference, which withdraws the exchange, and then withdraw the "+
				"replacement; nothing was released",
			replacementID, detail.SourceKind, detail.SourceID, detail.SourceStatus)
	}

	out := WithdrawResult{ReplacementID: replacementID}
	for i := range detail.Lines {
		line := detail.Lines[i]
		// A line that replaces a bundle holds one promise per part (ADR 0238),
		// and every one of them goes back.
		for _, promise := range line.promises() {
			if promise.reservationID == "" {
				continue
			}
			if err := w.inventory.ReleaseReservation(ctx, promise.reservationID); err != nil {
				if errors.IsConflict(err) {
					return WithdrawResult{}, errors.Wrap(err, errors.KindConflict, CodeStockNotReleased,
						"the units of %s on line %s were already taken out of the count for a "+
							"parcel; dispatch the replacement again to finish it rather than "+
							"withdrawing it",
						promise.variantID, line.ReplacementItemID)
				}
				return WithdrawResult{}, errors.Wrap(err, errors.KindOf(err), CodeStockNotReleased,
					"the units of %s on line %s could not be given back; nothing was withdrawn",
					promise.variantID, line.ReplacementItemID)
			}
			out.ReleasedPromises++
		}
	}

	if err := w.orders.CancelReplacement(ctx, replacementID); err != nil {
		return WithdrawResult{}, err
	}

	return out, nil
}
