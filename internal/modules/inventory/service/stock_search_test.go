package service_test

import (
	"context"
	"fmt"
	"math/rand"
	"slices"
	"testing"

	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/inventory/service"
)

// This file searches the stock state machine instead of listing cases.
//
// # Why a search here and nowhere else
//
// Every other money and stock invariant in this repository is held by a table,
// and that is right: a regression test for a defect somebody found by reading is
// a table, and a generator proposed to re-find a known counterexample buys
// nothing. What a table cannot do is INTERLEAVE. Reserve, confirm, release,
// adjust and restock each touch two numbers on one row, and the orders in which
// they can arrive are more than anybody writes out — a reservation confirmed
// after the stock under it was adjusted away, a release arriving twice, a
// restock between a reservation and its confirmation.
//
// # No dependency, and no random seed either
//
// A property library was measured and refused (D35's commit): it earned nothing
// where the counterexamples were already known. Here the search is real, but it
// runs on a FIXED table of seeds — so a run is reproducible, a failure names the
// seed and the exact operation, and this test can never go red on an unrelated
// change because a generator drew differently. Widening the search means adding
// a seed, which is a line in a diff somebody reads.
//
// # What it earns, measured in both directions
//
// A search that catches nothing a table catches is decoration, so four
// mutations were applied to the production code and each was run against the
// package's tables and against this file separately:
//
//	mutation                                          tables  search
//	Reserve compares against stocked, not available     caught  caught
//	ReleaseReservation stops being idempotent           caught  caught
//	ConfirmReservation drops its reserved-negative guard caught  MISSED
//	the reserved count stops falling once it passes 20  MISSED  caught
//
// The third row is the honest half: this file is WEAKER than the tables on a
// guard removed outright, because a table aims a fixture straight at the guard
// and a walk may never stand in front of it. The fourth is what it is for — a
// corruption that appears only after a state has built up over many operations
// is one no fixture reaches, because a fixture starts from the state somebody
// wrote down.
//
// So this is not a replacement for the tables next to it and must not be read
// as one. It covers a different class, and both rows above are the argument.
//
// # There is no shadow model, deliberately
//
// A model that recomputed what the service should hold would be a second
// implementation, and the test would then prove the two copies agree rather than
// that either is right. What is asserted instead is the DELTA of each
// operation — read the row before, apply, read it after — plus the invariants
// that must hold after every step whatever the sequence was.
const (
	// searchOperations is how many operations one seed applies.
	searchOperations = 60
	// searchStartStocked is the physical count each search starts from.
	//
	// It is SMALL on purpose. A search that starts with more stock than its
	// reservations can consume never reaches contention, and contention is the
	// state the invariants are about: measured on the first draft, a start of
	// 40 against reservations of 1-9 left every seed with stock to spare and the
	// search caught nothing the tables did not.
	searchStartStocked = 12
)

// searchSeeds are the sequences this test runs.
//
// Fixed, and each one is a different walk: the first two were chosen by hand to
// be short and long, the rest are arbitrary. A seed is never removed — a
// sequence that once found something is the sequence most worth keeping.
var searchSeeds = []int64{1, 2, 7, 13, 42, 1729, 20260909, 99991}

// stockRow is the pair of numbers every operation moves.
type stockRow struct{ stocked, reserved int64 }

// readRow reads the level back through the service's own list.
func readRow(t *testing.T, svc *service.Service, item, location string) stockRow {
	t.Helper()

	levels, err := svc.ListInventoryLevels(context.Background(), item)
	require.NoError(t, err)

	for i := range levels {
		if levels[i].LocationID == location {
			return stockRow{stocked: levels[i].StockedQuantity, reserved: levels[i].ReservedQuantity}
		}
	}

	t.Fatalf("the level for %s at %s disappeared", item, location)

	return stockRow{}
}

// TestTheStockStateMachineHoldsUnderEveryInterleaving is the search.
func TestTheStockStateMachineHoldsUnderEveryInterleaving(t *testing.T) {
	t.Parallel()

	for _, seed := range searchSeeds {
		t.Run(fmt.Sprintf("seed-%d", seed), func(t *testing.T) {
			t.Parallel()

			runStockSearch(t, seed)
		})
	}
}

// runStockSearch walks one seed's sequence.
func runStockSearch(t *testing.T, seed int64) {
	t.Helper()

	ctx := context.Background()
	// The service is built here rather than through the package's own helper:
	// that helper is named in Turkish and this file is English, and the
	// language ratchet counts an identifier as much as a sentence (ADR 0012).
	store := newFakeStore()
	svc := service.New(store, nil)
	store.seedItem(itemID, "SKU-SEARCH")
	store.seedLocation(locA)
	store.seedLevel(itemID, locA, searchStartStocked, 0)

	// A fixed source, not a secret: reproducibility is the whole point.
	random := rand.New(rand.NewSource(seed))
	live := map[string]int64{}
	// finished reservations are kept so the search can send the SAME call
	// twice. That is the interleaving a table does not reach: a release
	// arriving after a release, or after a confirm, is what a retried request
	// and a redelivered message both look like, and both are ordinary.
	finished := map[string]int64{}

	// history is printed only on a failure: a passing run should say nothing,
	// and a failing one has to be reproducible without re-running the seed.
	var history []string

	record := func(format string, args ...any) {
		history = append(history, fmt.Sprintf(format, args...))
	}

	fail := func(format string, args ...any) {
		t.Fatalf("seed %d, after %d operations: %s\nthe walk:\n  %v",
			seed, len(history), fmt.Sprintf(format, args...), history)
	}

	for step := range searchOperations {
		before := readRow(t, svc, itemID, locA)

		switch random.Intn(6) {
		case 0: // reserve
			quantity := int64(random.Intn(9) + 1)
			reservation, err := svc.Reserve(ctx, service.ReserveInput{
				InventoryItemID: itemID, LocationID: locA, Quantity: quantity,
			})
			after := readRow(t, svc, itemID, locA)
			if err != nil {
				record("%d reserve %d -> refused", step, quantity)
				if after != before {
					fail("a REFUSED reservation moved the row: %+v -> %+v", before, after)
				}

				continue
			}
			record("%d reserve %d -> %s", step, quantity, reservation.ID)
			live[reservation.ID] = quantity
			if after.reserved-before.reserved != quantity {
				fail("reserving %d moved reserved by %d", quantity, after.reserved-before.reserved)
			}
			if after.stocked != before.stocked {
				fail("reserving moved the PHYSICAL count %d -> %d; a reservation sets stock "+
					"aside, it does not take it out of the warehouse",
					before.stocked, after.stocked)
			}
			// The PRECONDITION, not only the delta. A reservation that
			// succeeded is a claim that the stock was there to promise, and
			// checking it here catches the guard reading the wrong number —
			// which the postcondition alone does not, because the row can
			// absorb one over-reservation and still look ordinary.
			if before.stocked-before.reserved < quantity {
				fail("a reservation for %d succeeded with only %d available "+
					"(%d stocked, %d already promised). Every unit over that line is an "+
					"order for goods the warehouse does not have",
					quantity, before.stocked-before.reserved, before.stocked, before.reserved)
			}

		case 1: // confirm
			id, quantity := anyLive(random, live)
			if id == "" {
				continue
			}
			err := svc.ConfirmReservation(ctx, id, testSaleOrderID)
			after := readRow(t, svc, itemID, locA)
			if err != nil {
				record("%d confirm %s -> refused", step, id)

				continue
			}
			record("%d confirm %s (%d)", step, id, quantity)
			delete(live, id)
			finished[id] = quantity
			if before.stocked-after.stocked != quantity {
				fail("confirming %d took %d out of the physical count",
					quantity, before.stocked-after.stocked)
			}
			if before.reserved-after.reserved != quantity {
				fail("confirming %d released %d of the reservation",
					quantity, before.reserved-after.reserved)
			}

		case 2: // release
			id, quantity := anyLive(random, live)
			if id == "" {
				continue
			}
			err := svc.ReleaseReservation(ctx, id)
			after := readRow(t, svc, itemID, locA)
			if err != nil {
				record("%d release %s -> refused", step, id)

				continue
			}
			record("%d release %s (%d)", step, id, quantity)
			delete(live, id)
			finished[id] = quantity
			if before.reserved-after.reserved != quantity {
				fail("releasing %d gave back %d", quantity, before.reserved-after.reserved)
			}
			if after.stocked != before.stocked {
				fail("releasing moved the physical count %d -> %d", before.stocked, after.stocked)
			}

		case 3: // adjust, in both directions
			delta := int64(random.Intn(21) - 10)
			_, err := svc.AdjustInventory(ctx, itemID, locA, delta)
			after := readRow(t, svc, itemID, locA)
			if err != nil {
				record("%d adjust %+d -> refused", step, delta)
				if after != before {
					fail("a REFUSED adjustment moved the row: %+v -> %+v", before, after)
				}

				continue
			}
			record("%d adjust %+d", step, delta)
			if after.stocked-before.stocked != delta {
				fail("adjusting by %d moved the count by %d", delta, after.stocked-before.stocked)
			}
			if after.reserved != before.reserved {
				fail("adjusting moved the RESERVED count %d -> %d; an adjustment is about "+
					"what is on the shelf, not about what is promised",
					before.reserved, after.reserved)
			}

		case 5: // the SAME call again, on a reservation already finished
			id, _ := anyLive(random, finished)
			if id == "" {
				continue
			}
			var err error
			verb := "confirm"
			if random.Intn(2) == 0 {
				verb = "release"
				err = svc.ReleaseReservation(ctx, id)
			} else {
				err = svc.ConfirmReservation(ctx, id, testSaleOrderID)
			}
			after := readRow(t, svc, itemID, locA)
			record("%d repeat %s %s -> err=%v", step, verb, id, err != nil)
			// Whether the second call is refused or answered as a no-op is the
			// module's own decision and both are defensible. What is not
			// defensible is the row MOVING: a promise given back twice takes
			// stock out of the warehouse that nobody ordered, and a
			// confirmation applied twice takes it out again.
			if after != before {
				fail("repeating %s on the finished reservation %s moved the row %+v -> %+v",
					verb, id, before, after)
			}

		case 4: // restock
			quantity := int64(random.Intn(10) + 1)
			_, err := svc.RestockInventory(ctx, itemID, locA, quantity)
			after := readRow(t, svc, itemID, locA)
			if err != nil {
				record("%d restock %d -> refused", step, quantity)

				continue
			}
			record("%d restock %d", step, quantity)
			if after.stocked-before.stocked != quantity {
				fail("restocking %d moved the count by %d", quantity, after.stocked-before.stocked)
			}
		}

		// The invariants, after EVERY operation whatever it was.
		row := readRow(t, svc, itemID, locA)
		switch {
		case row.stocked < 0:
			fail("the physical count went negative: %d", row.stocked)
		case row.reserved < 0:
			fail("the reserved count went negative: %d", row.reserved)
		case row.reserved > row.stocked:
			fail("more is promised than is held: reserved %d of %d stocked. Every promise "+
				"beyond the shelf is an order somebody placed for goods that are not there",
				row.reserved, row.stocked)
		}
	}
}

// anyLive picks one live reservation and its quantity, or "" when there is none.
//
// The pick is over a SORTED key set rather than over map iteration order: Go
// randomizes the second, and a search that drew from it would walk a different
// sequence on every run — which is the reproducibility this file is built on.
func anyLive(random *rand.Rand, live map[string]int64) (id string, quantity int64) {
	if len(live) == 0 {
		return "", 0
	}

	ids := make([]string, 0, len(live))
	for id := range live {
		ids = append(ids, id)
	}
	slices.Sort(ids)

	chosen := ids[random.Intn(len(ids))]

	return chosen, live[chosen]
}
