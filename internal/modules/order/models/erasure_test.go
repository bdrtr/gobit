package models_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/internal/modules/order/models"
)

// TestUnsettledReadsTheFactsAsATable verifies the rule that decides whether an
// order can be forgotten.
//
// The rule is worth a table test rather than an integration one because it is
// where the erasure REFUSES, and a refusal that fires on the wrong fact is
// invisible from the outside: the report still says "retained", still carries a
// sentence, and the sentence is simply about the wrong thing. The two rows that
// carry the most weight are the canceled order — whose outstanding amount is
// permanent and must NOT hold a person forever — and the soft-deleted one,
// which is erased rather than retained.
func TestUnsettledReadsTheFactsAsATable(t *testing.T) {
	t.Parallel()

	// settled is the shape everything else varies from: a completed order that
	// was paid in full and has no open after-sales record.
	settled := models.OrderErasureCandidate{
		OrderID:      "order_1",
		DisplayID:    1042,
		Status:       models.OrderCompleted,
		CurrencyCode: "TRY",
		Total:        6100,
		PaidTotal:    6100,
	}

	cases := []struct {
		name      string
		candidate func() models.OrderErasureCandidate
		want      models.UnsettledFact
	}{
		{
			name:      "a completed and fully paid order is settled",
			candidate: func() models.OrderErasureCandidate { return settled },
			want:      models.Settled,
		},
		{
			name: "a pending order is held by its status",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.Status = models.OrderPending

				return c
			},
			want: models.UnsettledPending,
		},
		{
			name: "an unpaid completed order is held by the money",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.PaidTotal = 0

				return c
			},
			want: models.UnsettledOutstanding,
		},
		{
			name: "an OVERCOLLECTED order is held too, because the money is owed back",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.PaidTotal = 6600

				return c
			},
			want: models.UnsettledOutstanding,
		},
		{
			// The regression. paid_total NEVER SHRINKS — the summary merges it
			// with GREATEST — so the difference total - (paid - refunded) reads
			// 6100 here and read this order as unsettled on every sweep for
			// ever, which made the module refuse to forget a person who had
			// been given all of their money back.
			name: "a fully REFUNDED order is settled, not owed for ever",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.RefundedTotal = 6100

				return c
			},
			want: models.Settled,
		},
		{
			name: "an overcollection refunded back down to the total is settled",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.PaidTotal = 6600
				c.RefundedTotal = 500

				return c
			},
			want: models.Settled,
		},
		{
			name: "a PARTIAL refund still leaves the uncollected part owed",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.PaidTotal = 4000
				c.RefundedTotal = 4000

				return c
			},
			want: models.UnsettledOutstanding,
		},
		{
			name: "a canceled order is NOT held by an amount nobody will ever collect",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.Status = models.OrderCanceled
				c.PaidTotal = 0

				return c
			},
			want: models.Settled,
		},
		{
			name: "an archived order IS held by an outstanding amount",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.Status = models.OrderArchived
				c.PaidTotal = 0

				return c
			},
			want: models.UnsettledOutstanding,
		},
		{
			name: "a requested return holds the order",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.ReturnRequested = true

				return c
			},
			want: models.UnsettledReturnRequested,
		},
		{
			name: "an open exchange holds the order",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.ExchangeRequested = true

				return c
			},
			want: models.UnsettledExchangeRequested,
		},
		{
			name: "an open claim holds the order",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.ClaimRequested = true

				return c
			},
			want: models.UnsettledClaimRequested,
		},
		{
			name: "the status is reported before the money when both hold",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.Status = models.OrderPending
				c.PaidTotal = 0
				c.ClaimRequested = true

				return c
			},
			want: models.UnsettledPending,
		},
		{
			name: "a soft-deleted order is settled whatever else is true of it",
			candidate: func() models.OrderErasureCandidate {
				c := settled
				c.Deleted = true
				c.Status = models.OrderPending
				c.PaidTotal = 0
				c.ReturnRequested = true

				return c
			},
			want: models.Settled,
		},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			assert.Equal(t, tc.want, tc.candidate().Unsettled())
		})
	}
}

// TestOwedMeasuresWhatIsStillMoving verifies the money half of the settlement
// rule, in the amounts as well as in the verdict.
//
// The figure is not internal: whyRetained prints it into the sentence a
// controller repeats to the data subject ("money is still outstanding on the
// order (6100 TRY, in minor units)"), so a wrong amount is a wrong statement
// made to a person about their own money. The rows that carry the weight are
// the refunded ones: paid_total never shrinks, so every one of them read as a
// full debt under the difference the payment screen uses.
func TestOwedMeasuresWhatIsStillMoving(t *testing.T) {
	t.Parallel()

	cases := []struct {
		name                          string
		total, paid, refunded, wanted int64
	}{
		{name: "nothing collected yet is the whole sale", total: 6100, wanted: 6100},
		{name: "paid in full owes nothing", total: 6100, paid: 6100},
		{
			name:  "paid in full and refunded in full owes nothing IN EITHER DIRECTION",
			total: 12000, paid: 12000, refunded: 12000,
		},
		{
			name:  "an overcollection is owed BACK, so the amount is negative",
			total: 6100, paid: 6600, wanted: -500,
		},
		{
			name:  "the overcollection refunded is settled",
			total: 6100, paid: 6600, refunded: 500,
		},
		{
			name:  "a refunded PART payment still leaves the uncollected part owed",
			total: 12000, paid: 5000, refunded: 5000, wanted: 7000,
		},
		{name: "an order with no summary row owes its whole total", total: 6100, wanted: 6100},
	}

	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			candidate := models.OrderErasureCandidate{
				Total: tc.total, PaidTotal: tc.paid, RefundedTotal: tc.refunded,
			}
			assert.Equal(t, tc.wanted, candidate.Owed())
		})
	}
}

// TestEveryUnsettledFactSaysSomething verifies that no fact reaches a report as
// "unknown".
//
// The string is not decoration: it is the half of the answer that tells the
// person WHY their data was kept, and a fact added to the type without a
// sentence would reach them as the word "unknown" — which is exactly the
// reasonless refusal the third outcome exists to prevent.
func TestEveryUnsettledFactSaysSomething(t *testing.T) {
	t.Parallel()

	facts := []models.UnsettledFact{
		models.Settled,
		models.UnsettledPending,
		models.UnsettledOutstanding,
		models.UnsettledReturnRequested,
		models.UnsettledExchangeRequested,
		models.UnsettledClaimRequested,
	}

	seen := make(map[string]struct{}, len(facts))
	for _, fact := range facts {
		text := fact.String()
		assert.NotEqual(t, "unknown", text, "fact %d has no sentence", int(fact))
		assert.NotContains(t, seen, text, "two facts share the sentence %q", text)
		seen[text] = struct{}{}
	}

	assert.Equal(t, "unknown", models.UnsettledFact(len(facts)).String(),
		"a fact this test does not know about must not borrow another one's sentence")
}
