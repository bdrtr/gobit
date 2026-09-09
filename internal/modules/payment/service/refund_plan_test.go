package service

import (
	"testing"

	"github.com/bdrtr/gobit/internal/modules/payment/models"
)

// TestPlanRefundNeverGivesBackMoreThanACaptureHolds covers the two branches no
// other test reaches.
//
// # Why these rows and not a generated set
//
// Every existing test of this function drives a collection with ONE payment, so
// two of `planRefund`'s branches — the `left == 0` break and the `available <= 0`
// skip — have never been executed. Both are about a collection with SEVERAL
// captures, which is the ordinary shape once a payment is retried or split.
//
// A generated search was considered and refused: the counterexamples here were
// found by reading the function, they are two-line literals, and a generator
// proposed to re-find a known defect is a regression test with a random seed
// attached. What a table cannot do is SEARCH, and nothing here is being
// searched for.
//
// # What each row would catch
//
// The mutation is `available := payments[i].Amount` — dropping the amount
// already refunded. It compiles, the whole existing suite stays green, and this
// table fails: the first payment is given back money it no longer holds.
func TestPlanRefundNeverGivesBackMoreThanACaptureHolds(t *testing.T) {
	t.Parallel()

	for name, testCase := range map[string]struct {
		payments []models.Payment
		amount   int64
		want     map[string]int64
	}{
		// The exhausted capture: p1 has already given everything back, so the
		// whole refund must come out of p2. A planner that ignored
		// RefundedAmount would take it from p1 and the database's own
		// payments_refund_le_amount CHECK would then refuse the write — after
		// the plan said it was fine.
		"a capture with nothing left is skipped": {
			payments: []models.Payment{
				{ID: "p1", Amount: 100, RefundedAmount: 100},
				{ID: "p2", Amount: 100, RefundedAmount: 0},
			},
			amount: 100,
			want:   map[string]int64{"p2": 100},
		},
		// The span: the amount is larger than the first capture, so the plan
		// has to reach the second and then STOP. A planner without the break
		// would carry on and hand back more than was asked for.
		"an amount spanning two captures stops when it is satisfied": {
			payments: []models.Payment{
				{ID: "p1", Amount: 100, RefundedAmount: 0},
				{ID: "p2", Amount: 100, RefundedAmount: 0},
				{ID: "p3", Amount: 100, RefundedAmount: 0},
			},
			amount: 150,
			want:   map[string]int64{"p1": 100, "p2": 50},
		},
		// A partly refunded capture gives back only its remainder.
		"a partly refunded capture gives back the remainder": {
			payments: []models.Payment{
				{ID: "p1", Amount: 100, RefundedAmount: 60},
				{ID: "p2", Amount: 100, RefundedAmount: 0},
			},
			amount: 90,
			want:   map[string]int64{"p1": 40, "p2": 50},
		},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			plan, err := planRefund("col_1", testCase.payments, testCase.amount)
			if err != nil {
				t.Fatalf("the plan was refused: %v", err)
			}

			held := map[string]int64{}
			for i := range testCase.payments {
				held[testCase.payments[i].ID] =
					testCase.payments[i].Amount - testCase.payments[i].RefundedAmount
			}

			var total int64

			seen := map[string]bool{}
			for _, part := range plan {
				if seen[part.paymentID] {
					t.Errorf("payment %s appears twice in one plan; the second refund would "+
						"be written against a capture the first has already emptied",
						part.paymentID)
				}
				seen[part.paymentID] = true

				if part.amount > held[part.paymentID] {
					t.Errorf("payment %s is given back %d and holds only %d. The database's "+
						"payments_refund_le_amount CHECK would refuse this write, after the "+
						"plan said the refund was possible",
						part.paymentID, part.amount, held[part.paymentID])
				}

				total += part.amount
			}

			if total != testCase.amount {
				t.Errorf("the plan gives back %d and %d was asked for", total, testCase.amount)
			}

			got := map[string]int64{}
			for _, part := range plan {
				got[part.paymentID] = part.amount
			}
			for id, want := range testCase.want {
				if got[id] != want {
					t.Errorf("payment %s: plan gives %d, expected %d (whole plan: %v)",
						id, got[id], want, got)
				}
			}
			if len(got) != len(testCase.want) {
				t.Errorf("the plan touches %d captures, expected %d: %v",
					len(got), len(testCase.want), got)
			}
		})
	}
}
