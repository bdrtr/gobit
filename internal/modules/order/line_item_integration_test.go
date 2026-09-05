//go:build integration

// The tests in this file are the FIRST execution of the SQL behind the
// "order_line_item" Query entity: ListOrderLineItemsFiltered and
// GetOrderLineItemsByIDs in
// internal/modules/order/queries/order_line_items.sql, together with the
// indexes of internal/modules/order/migrations/000006_order_line_item_reads.up.sql.
//
// Everything else that covers this entity is a UNIT test over an in-memory fake
// store. That is not a substitute here, and the reason is sharper than "unit
// tests are not integration tests": the fake's filter is Go code written from
// the same intent as the SQL, by the same hand, at the same hour. The two can
// therefore be wrong TOGETHER and still agree, and a fake that mirrors a query
// nobody runs proves only that the fake agrees with itself. Four of the claims
// below are of exactly that kind — nothing but a real PostgreSQL can say them:
//
//   - a sqlc.narg spelled "$N::text IS NULL OR col = $N::text" means "criterion
//     not given" and not "match nothing". A fake writes that branch as
//     `if f.OrderID != nil`, which cannot fail the way a NULL comparison can;
//   - the date range is matched against the ORDER's placed_at through a JOIN,
//     not against the line's own created_at. A fake holds both stamps in the
//     same struct and cannot tell the two apart at all;
//   - the JOIN's `o.deleted_at IS NULL` hides the lines of a soft-deleted order
//     even though the LINE rows themselves are alive;
//   - a []string binds to `= ANY($1::text[])`.
//
// # Why the fixture is written straight into the table
//
// placed_at is stamped by the database with now(), so an order CANNOT be placed
// in the past over the service, and every claim about a date range needs orders
// that sit at known instants. [writePastOrder] already exists for that reason
// and is reused rather than duplicated. What is set up by hand is only the
// PAST; the behavior under test is still read through the real repository.
//
// # Why every test builds its own world on its own day
//
// The whole package shares ONE database and the tests before and after these
// ones leave live orders in it, all of them placed at now(). A test that asked
// for "everything" would therefore be asserting about rows it did not write. So
// each test gets its own variant id AND its own day in 2019 — a period no other
// test in this package writes into — and asserts on exact identifier sets
// rather than on counts of whatever happens to be in the table.
package order_test

import (
	"context"
	"slices"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/eventbus"
	"github.com/bdrtr/gobit/core/link"
	"github.com/bdrtr/gobit/core/query"
	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/modules/order/models"
	"github.com/bdrtr/gobit/internal/modules/order/repository"
	"github.com/bdrtr/gobit/internal/modules/order/service"
)

// lineItemReadLimit is the row limit handed to the repository in these tests.
//
// The repository does NOT apply a default: `LIMIT $n` with a zero would return
// nothing, and a test that passed 0 would be green for a reason that has
// nothing to do with what it claims to prove. The clamping to a default belongs
// to the service (see [service.Page]), which is a layer above this one.
const lineItemReadLimit = 100

// ptr takes the address of a value so that an optional filter criterion can be
// given inline. The filter fields are pointers because nil is "criterion not
// given" (see [models.OrderLineItemFilter]).
func ptr[T any](v T) *T { return &v }

// lineItemOrder is one order of a fixture together with the lines written on
// it.
type lineItemOrder struct {
	// id is the order's identifier.
	id string
	// placedAt is the instant the order was placed at; it is pinned by hand,
	// because now() cannot be argued with.
	placedAt time.Time
	// lines are the lines written on the order, in the order they were written.
	lines []models.OrderLineItem
}

// ids returns the identifiers of the order's lines.
func (o lineItemOrder) ids() []string {
	out := make([]string, 0, len(o.lines))
	for i := range o.lines {
		out = append(out, o.lines[i].ID)
	}
	return out
}

// lineItemWorld is the row shape every filtered read in this file is exercised
// against.
//
// The four orders are not four arbitrary rows; each one exists to make ONE
// branch of the query observable, and they are built together because a test
// that only ever saw the rows it expects could not tell a working filter from a
// filter that returns everything:
//
//   - atFrom sits EXACTLY on the lower bound, which is inclusive;
//   - atTo sits EXACTLY on the upper bound, which is exclusive;
//   - inside sits between the two and is the row a broken bound would take away;
//   - deleted also sits between the two but its ORDER is soft-deleted, so it is
//     the row a missing `o.deleted_at IS NULL` would hand back.
//
// Every line in the world carries the same variant id, which is unique to the
// world. That is what makes an assertion about an exact identifier set possible
// on a database other tests are also writing into.
type lineItemWorld struct {
	// repo is the real repository; the SQL is run through it and not through a
	// hand-written statement, so that the parameter binding done by the
	// generated code is part of what is under test.
	repo *repository.Repository
	// variant is the variant id every line of this world was sold under.
	variant string
	// dayOne is the inclusive lower bound of the window under test and dayTwo
	// the exclusive upper one; they are exactly a day apart.
	dayOne time.Time
	dayTwo time.Time

	// atFrom is placed exactly at dayOne and carries TWO lines, so that the
	// `li.id DESC` tie-break inside a single order has something to break.
	atFrom lineItemOrder
	// inside is placed within the window.
	inside lineItemOrder
	// atTo is placed exactly at dayTwo.
	atTo lineItemOrder
	// deleted is placed within the window and then soft-deleted.
	deleted lineItemOrder
}

// liveIDs returns the identifiers of the lines whose orders are alive, sorted
// ascending.
//
// The lines of [lineItemWorld.deleted] are deliberately absent: they are alive
// as rows and must still be invisible to both statements of the provider.
func (w lineItemWorld) liveIDs() []string {
	out := slices.Concat(w.atFrom.ids(), w.inside.ids(), w.atTo.ids())
	slices.Sort(out)
	return out
}

// newLineItemWorld builds a fixture whose window is [day, day+24h).
//
// tag makes the world's variant and customer identifiers unique so that two
// tests cannot see each other's rows; day does the same for the date range. The
// two are independent on purpose — a test that scopes by variant is still
// readable when a later test picks the same day by accident.
func newLineItemWorld(ctx context.Context, t *testing.T, tag string, day time.Time) lineItemWorld {
	t.Helper()

	world := lineItemWorld{
		repo:    repository.New(testPool.Pool()),
		variant: "variant_LI_" + tag,
		dayOne:  day,
		dayTwo:  day.Add(24 * time.Hour),
	}

	customer := "cus_LI_" + tag
	world.atFrom = world.place(ctx, t, customer, "at the lower bound", day, 2)
	world.inside = world.place(ctx, t, customer, "inside the window", day.Add(10*time.Hour), 1)
	world.atTo = world.place(ctx, t, customer, "at the upper bound", world.dayTwo, 1)
	world.deleted = world.place(ctx, t, customer, "on a deleted order", day.Add(5*time.Hour), 1)
	softDeleteOrderRow(ctx, t, world.deleted.id)

	return world
}

// place writes an order at the given instant together with count lines on it.
//
// The lines are written through [repository.Repository.CreateLineItem] rather
// than with an INSERT of their own: the row this file reads back has to be a
// row the module itself would have written, otherwise a column the writing side
// forgets would be invisible here as well.
func (w lineItemWorld) place(
	ctx context.Context, t *testing.T, customer, label string, placedAt time.Time, count int,
) lineItemOrder {
	t.Helper()

	placed := lineItemOrder{
		id:       writePastOrder(ctx, t, customer, int64(count)*3000, placedAt),
		placedAt: placedAt,
	}
	for i := 0; i < count; i++ {
		placed.lines = append(placed.lines, writeLineItem(ctx, t, w.repo, models.OrderLineItem{
			OrderID:   placed.id,
			VariantID: w.variant,
			Title:     "Line " + label,
			// The amounts satisfy order_line_items_totals_consistent: a
			// subtotal of 3000 less a discount of 500 plus a tax of 500 is the
			// total of 3000.
			Quantity: 2, UnitPrice: 1500, Subtotal: 3000,
			DiscountTotal: 500, TaxTotal: 500, TaxRateBps: 2000, Total: 3000,
		}))
	}
	return placed
}

// writeLineItem writes one line and returns it as it came back from the
// database.
//
// The identifier is produced here rather than by the caller so that a test
// never has to invent one; what a test cares about is the SET of identifiers it
// gets back, and that set is read off the return value.
func writeLineItem(
	ctx context.Context, t *testing.T, repo *repository.Repository, item models.OrderLineItem,
) models.OrderLineItem {
	t.Helper()

	item.ID = models.NewLineItemID()
	created, err := repo.CreateLineItem(ctx, item)
	require.NoError(t, err)
	return created
}

// softDeleteOrderRow marks the order deleted with a direct UPDATE.
//
// The module publishes no delete operation for an order — deletion is not part
// of the order's lifecycle, an order is canceled instead — but the schema and
// every read still carry a soft-delete condition, and the JOIN in
// internal/modules/order/queries/order_line_items.sql relies on it. Setting the
// column by hand is the only way to reach that branch, and the assertion that
// depends on it (the line row itself stays alive) is checked in the test rather
// than assumed here.
func softDeleteOrderRow(ctx context.Context, t *testing.T, orderID string) {
	t.Helper()

	tag, err := testPool.Pool().Exec(ctx,
		`UPDATE orders SET deleted_at = now() WHERE id = $1`, orderID)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected(), "the order to be deleted must exist")
}

// orderedLineIDs returns the identifiers of the lines IN THE ORDER the query
// produced them.
//
// The order is preserved because the ordering of both statements is one of the
// claims under test. A helper that sorted on the way out would destroy that
// evidence silently — and it did: the first version of this file sorted every
// result, and a mutation that turned the batch read's `ORDER BY li.id` into
// `ORDER BY li.id DESC` went through the whole suite unnoticed.
func orderedLineIDs(lines []models.OrderLineItem) []string {
	out := make([]string, 0, len(lines))
	for i := range lines {
		out = append(out, lines[i].ID)
	}
	return out
}

// softDeleteLineRow marks a single LINE deleted with a direct UPDATE.
//
// It exists for the same reason [softDeleteOrderRow] does — the module offers
// no delete operation for a line, because an order line is immutable once
// written and a correction goes through a return or an exchange — while both
// statements of this provider still carry `li.deleted_at IS NULL`. A condition
// no test can reach is a condition that can be deleted without a sound.
func softDeleteLineRow(ctx context.Context, t *testing.T, lineID string) {
	t.Helper()

	tag, err := testPool.Pool().Exec(ctx,
		`UPDATE order_line_items SET deleted_at = now() WHERE id = $1`, lineID)
	require.NoError(t, err)
	require.EqualValues(t, 1, tag.RowsAffected(), "the line to be deleted must exist")
}

// readLineIDs runs the filtered listing and returns the identifiers it produced,
// in the order the query produced them.
func readLineIDs(
	ctx context.Context, t *testing.T, repo *repository.Repository, filter models.OrderLineItemFilter,
) []string {
	t.Helper()

	lines, err := repo.ListLineItemsFiltered(ctx, filter)
	require.NoError(t, err)
	return orderedLineIDs(lines)
}

// sortedLineIDs returns the identifiers of the lines, sorted ascending.
//
// It is for the assertions whose claim is MEMBERSHIP — which rows came back —
// and not order. An assertion about the order must use [orderedLineIDs].
func sortedLineIDs(lines []models.OrderLineItem) []string {
	out := orderedLineIDs(lines)
	slices.Sort(out)
	return out
}

// TestLineItemMigrationBringsItsIndexes verifies that migration 000006 is
// really applied.
//
// It is the ground under every other test in this file: the harness migrates
// the module's whole directory in TestMain (see [runWithPostgres]) rather than
// pinning a version or listing files by hand, so a migration added to the
// directory is applied without anything else being touched. That is a property
// worth ASSERTING and not only reading, because the failure mode of the
// alternative is silent — a pinned harness runs every query in this file
// successfully, just without the indexes, and the only symptom is a sequential
// scan nobody sees in a test.
//
// The definitions are checked and not only the names. An index called
// orders_placed_at_idx that was built on the wrong column, or without the
// partial predicate, would carry the name that satisfies a `SELECT 1` and serve
// neither the range nor the ordering it exists for.
func TestLineItemMigrationBringsItsIndexes(t *testing.T) {
	ctx := context.Background()

	definitions := map[string]string{}
	rows, err := testPool.Pool().Query(ctx,
		`SELECT indexname, indexdef FROM pg_indexes
         WHERE schemaname = current_schema()
           AND indexname = ANY($1)`,
		[]string{"orders_placed_at_idx", "order_line_items_variant_idx"})
	require.NoError(t, err)
	defer rows.Close()

	for rows.Next() {
		var name, definition string
		require.NoError(t, rows.Scan(&name, &definition))
		definitions[name] = definition
	}
	require.NoError(t, rows.Err())

	placed, ok := definitions["orders_placed_at_idx"]
	require.True(t, ok, "migration 000006 was not applied: orders_placed_at_idx is missing")
	assert.Contains(t, placed, "placed_at DESC",
		"the index has to descend, otherwise the ORDER BY sorts the whole period first")
	assert.Contains(t, placed, "deleted_at IS NULL",
		"the index has to be partial, so a deleted order does not occupy it")

	variant, ok := definitions["order_line_items_variant_idx"]
	require.True(t, ok, "migration 000006 was not applied: order_line_items_variant_idx is missing")
	assert.Contains(t, variant, "variant_id",
		"the variant filter is the half of the question this index serves")
	assert.Contains(t, variant, "order_id",
		"the join key is the second column so the discarded rows never reach the heap")
}

// TestLineItemFilterTreatsANilCriterionAsNotGiven is the test of the
// sqlc.narg branch, and it is the one a fake store cannot stand in for.
//
// The generated condition is `$1::text IS NULL OR li.order_id = $1::text`. Its
// classic failure is not that it filters wrongly but that it collapses into
// "match nothing": in SQL a comparison against NULL is NULL rather than true,
// so a condition written one degree differently — `li.order_id = $1 OR $1 IS
// NULL` reordered by a rewrite, a cast dropped, a COALESCE added "for safety" —
// turns an absent criterion into an empty answer. The fake writes the same
// branch as `if f.OrderID != nil`, which has no NULL in it and therefore cannot
// exhibit the fault at all.
//
// All four criteria are exercised as absent, one group at a time, because
// "absent" is a different code path per parameter and a query where only the
// dates survived would still pass a test that left the dates out.
func TestLineItemFilterTreatsANilCriterionAsNotGiven(t *testing.T) {
	ctx := context.Background()
	world := newLineItemWorld(ctx, t, "NILNARG", time.Date(2019, 1, 14, 0, 0, 0, 0, time.UTC))

	// Both identifier criteria absent: the window alone selects, and it has to
	// bring back the lines of TWO different orders.
	byWindow := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		PlacedFrom: ptr(world.dayOne),
		PlacedTo:   ptr(world.dayTwo),
		Limit:      lineItemReadLimit,
	})
	slices.Sort(byWindow)
	expected := slices.Concat(world.atFrom.ids(), world.inside.ids())
	slices.Sort(expected)
	assert.Equal(t, expected, byWindow,
		"a nil order_id and a nil variant_id must mean the criterion was not given, "+
			"not that nothing matches")

	// Both date criteria absent: the order id alone selects. If the two
	// timestamptz nargs collapsed, this call would come back empty even though
	// the order plainly exists.
	byOrder := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		OrderID: ptr(world.atFrom.id),
		Limit:   lineItemReadLimit,
	})
	slices.Sort(byOrder)
	assert.Equal(t, slices.Sorted(slices.Values(world.atFrom.ids())), byOrder,
		"a nil placed_from and a nil placed_to must mean the range was not given")

	// Only the variant: neither an order nor a date. The deleted order's line
	// carries this variant too and must still stay out.
	byVariant := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		VariantID: ptr(world.variant),
		Limit:     lineItemReadLimit,
	})
	slices.Sort(byVariant)
	assert.Equal(t, world.liveIDs(), byVariant,
		"the variant criterion alone must select every live line of the variant")

	// Every criterion absent. The assertion is deliberately weak — the table
	// holds the rows of every other test in the package and no exact set can be
	// stated — but it is the only shape in which all four nargs are NULL at
	// once, which is the combination a rewrite would break first.
	all, err := world.repo.ListLineItemsFiltered(ctx, models.OrderLineItemFilter{Limit: 1000})
	require.NoError(t, err)
	assert.GreaterOrEqual(t, len(all), len(byVariant),
		"with all four criteria absent the listing must return at least the rows a "+
			"single criterion returned")
	assert.NotEmpty(t, all, "four NULL criteria must not mean an empty answer")
}

// TestLineItemDateRangeIsHalfOpen verifies that the interval is [from, to).
//
// The claim is not decoration. Two consecutive periods are asked for back to
// back — January then February, this month then last month — and if both bounds
// were inclusive an order placed exactly at midnight would be counted in BOTH
// of them. A total that is right for one month and double-counts one order at
// the seam is the kind of wrong that a report shows without ever failing.
//
// So the test asserts both halves of the same seam: the boundary order is in
// the window that starts at it and out of the window that ends at it. Checking
// only one half would pass on a query that lost the other bound entirely.
func TestLineItemDateRangeIsHalfOpen(t *testing.T) {
	ctx := context.Background()
	world := newLineItemWorld(ctx, t, "HALFOPEN", time.Date(2019, 2, 11, 0, 0, 0, 0, time.UTC))

	first := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		VariantID:  ptr(world.variant),
		PlacedFrom: ptr(world.dayOne),
		PlacedTo:   ptr(world.dayTwo),
		Limit:      lineItemReadLimit,
	})
	assert.Subset(t, first, world.atFrom.ids(),
		"an order placed EXACTLY at placed_from is inside the window")
	assert.NotSubset(t, first, world.atTo.ids(),
		"an order placed EXACTLY at placed_to is outside the window")

	// The very same order in the NEXT window. Together with the assertion
	// above this says the boundary order is counted exactly once across two
	// adjacent periods, which is the whole point of the half-open interval.
	second := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		VariantID:  ptr(world.variant),
		PlacedFrom: ptr(world.dayTwo),
		PlacedTo:   ptr(world.dayTwo.Add(24 * time.Hour)),
		Limit:      lineItemReadLimit,
	})
	assert.Equal(t, world.atTo.ids(), second,
		"the boundary order belongs to the period that STARTS at it")
}

// TestLineItemDateRangeMatchesTheOrdersMomentNotTheLinesRow is the reason the
// query joins orders at all.
//
// The fixture separates the two stamps the way an exchange does: the orders sit
// in 2019 and their line ROWS are written now. A query that filtered on
// li.created_at would therefore answer the exact opposite of this query on
// every single row — it would find nothing in the 2019 window and everything in
// the window around this instant.
//
// A fake store cannot express this fixture. Its lines are structs holding both
// a CreatedAt and an order reference, and its filter picks one of them by
// reading the field the author meant to read; there is no arrangement of the
// fake in which the wrong field is chosen. The separation only exists once the
// row is in a table and the stamp is a column of ANOTHER table.
func TestLineItemDateRangeMatchesTheOrdersMomentNotTheLinesRow(t *testing.T) {
	ctx := context.Background()
	world := newLineItemWorld(ctx, t, "EXCHANGE", time.Date(2019, 3, 11, 0, 0, 0, 0, time.UTC))

	// The shape really is the exchange shape: without this the rest of the test
	// would pass on a fixture where the two stamps happened to agree, and would
	// prove nothing.
	for _, line := range world.atFrom.lines {
		require.True(t, line.CreatedAt.After(world.dayTwo),
			"the line row must have been written LATER than its order was placed; "+
				"created_at=%s placed_at=%s", line.CreatedAt, world.atFrom.placedAt)
	}

	inThePast := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		VariantID:  ptr(world.variant),
		PlacedFrom: ptr(world.dayOne),
		PlacedTo:   ptr(world.dayTwo),
		Limit:      lineItemReadLimit,
	})
	slices.Sort(inThePast)
	expected := slices.Concat(world.atFrom.ids(), world.inside.ids())
	slices.Sort(expected)
	assert.Equal(t, expected, inThePast,
		"the window of the SALE must find the lines even though no line row was "+
			"written in it")

	// The mirror image: the window in which every one of those rows WAS written
	// must be empty, because no order was placed in it.
	now := time.Now().UTC()
	inTheRowsWindow := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		VariantID:  ptr(world.variant),
		PlacedFrom: ptr(now.Add(-time.Hour)),
		PlacedTo:   ptr(now.Add(time.Hour)),
		Limit:      lineItemReadLimit,
	})
	assert.Empty(t, inTheRowsWindow,
		"the window in which the ROWS were written must find nothing: the filter is "+
			"bound to the order's placed_at, not to the line's created_at")
}

// TestLineItemSoftDeletedOrderHidesItsLinesFromBothReads verifies the JOIN's
// liveness condition on BOTH statements of the provider.
//
// Both, and not one, because the failure this guards against is a DISAGREEMENT:
// the listing is what a report walks, FetchByIDs is what an expansion calls,
// and if only one of them carried `o.deleted_at IS NULL` the same line would
// exist or not exist depending on which side of the query it was reached from.
// That is the divergence migration 000001 warns about for order_summaries, and
// it is invisible to any test that only ever exercises the listing.
//
// The line ROW of the deleted order is deliberately left alive. If the test
// soft-deleted the line as well, it would pass on a query that only checks
// li.deleted_at — which is the condition ListOrderLineItems already had and the
// exact thing that is NOT enough here.
//
// The opposite case is covered too, with a second order that is alive and whose
// LINE is deleted. Both conditions are in both statements and a test that
// exercised only one of them would let the other be dropped.
func TestLineItemSoftDeletedOrderHidesItsLinesFromBothReads(t *testing.T) {
	ctx := context.Background()
	world := newLineItemWorld(ctx, t, "DELETED", time.Date(2019, 4, 8, 0, 0, 0, 0, time.UTC))
	hidden := world.deleted.ids()

	// A live order carrying a deleted LINE. It is written after the world is
	// built and is deliberately NOT part of [lineItemWorld.liveIDs], so both
	// assertions below fail the moment it becomes visible.
	withDeletedLine := world.place(ctx, t, "cus_LI_DELETED", "a deleted line",
		world.dayOne.Add(7*time.Hour), 1)
	softDeleteLineRow(ctx, t, withDeletedLine.ids()[0])
	hidden = slices.Concat(hidden, withDeletedLine.ids())

	// The line row of the deleted ORDER is untouched; only its order was
	// deleted. Without this the assertions could be satisfied by the wrong
	// condition.
	var lineAlive bool
	require.NoError(t, testPool.Pool().QueryRow(ctx,
		`SELECT deleted_at IS NULL FROM order_line_items WHERE id = $1`,
		world.deleted.ids()[0]).Scan(&lineAlive))
	require.True(t, lineAlive, "the LINE must still be alive; only the order was deleted")

	listed := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		VariantID: ptr(world.variant),
		Limit:     lineItemReadLimit,
	})
	slices.Sort(listed)
	assert.Equal(t, world.liveIDs(), listed,
		"the filtered listing must report neither the line of a deleted order nor a "+
			"deleted line as a sale")

	// The same identifiers through the batch read, asked for TOGETHER with the
	// live ones: a statement that answered by identity alone would hand the
	// hidden lines back here.
	fetched, err := world.repo.LineItemsByIDs(ctx, slices.Concat(world.liveIDs(), hidden))
	require.NoError(t, err)
	assert.Equal(t, world.liveIDs(), sortedLineIDs(fetched),
		"the batch read must hide exactly what the listing hides")
}

// TestLineItemsByIDsReadTheWholeBatchInOneStatement covers
// [repository.Repository.LineItemsByIDs] over `= ANY($1::text[])`.
//
// What has never been executed here is the ARRAY BINDING: a Go []string has to
// arrive as a Postgres text[], and until this test ran, nothing had ever handed
// the driver one. A fake store answers the same call with a map lookup, so the
// binding is not merely untested by the unit tests, it is unreachable from
// them.
//
// The test asks for a SUBSET plus an identifier that does not exist, rather than
// for everything. Asking for everything would be satisfied by a statement that
// ignored its parameter altogether and returned the table; asking for a subset
// makes the answer wrong the moment the array stops being read. The unknown
// identifier pins the other half of the contract: a missing record is an absent
// row, not an error and not a zero-valued one.
//
// # What it does not prove
//
// That there is exactly ONE round trip is a property of the statement's shape,
// not something asserted here — the repository issues a single Query and there
// is no per-identifier loop to observe. Counting statements would need a driver
// tracer, which is a heavier fixture than the claim is worth; the guard against
// an N+1 creeping back in is the shape of the query itself.
func TestLineItemsByIDsReadTheWholeBatchInOneStatement(t *testing.T) {
	ctx := context.Background()
	world := newLineItemWorld(ctx, t, "BATCH", time.Date(2019, 5, 13, 0, 0, 0, 0, time.UTC))

	// A line whose every amount is a DIFFERENT number, on purpose. The
	// generated scan reads fifteen columns positionally and puts tax_rate_bps
	// last, after the timestamps; two columns swapped there still compile and
	// still return rows, and with the fixture's uniform amounts the swap would
	// be invisible.
	distinct := writeLineItem(ctx, t, world.repo, models.OrderLineItem{
		OrderID:   world.inside.id,
		VariantID: world.variant,
		Title:     "Every amount different",
		// The totals identity still holds: a subtotal of 8638 less a discount
		// of 638 plus a tax of 1600 is the total of 9600.
		Quantity: 7, UnitPrice: 1234, Subtotal: 8638,
		DiscountTotal: 638, TaxTotal: 1600, TaxRateBps: 2000, Total: 9600,
		Metadata: map[string]any{"source": "batch-test"},
	})

	wanted := slices.Sorted(slices.Values(slices.Concat(world.atFrom.ids(), []string{distinct.ID})))

	// The identifiers are asked for in DESCENDING order, and the one that does
	// not exist is put first. Handing them over already ascending would make
	// "the statement sorted them" and "the statement echoed the input back"
	// indistinguishable, which is how the `ORDER BY li.id` of this query
	// survived the first version of this test untouched.
	asked := slices.Clone(wanted)
	slices.Reverse(asked)
	asked = slices.Concat([]string{"oli_0000000000000000000000000"}, asked)

	fetched, err := world.repo.LineItemsByIDs(ctx, asked)
	require.NoError(t, err)

	assert.Equal(t, wanted, orderedLineIDs(fetched),
		"the batch must return exactly the identifiers that exist, in ASCENDING "+
			"li.id order whatever order they were asked in, and invent nothing for "+
			"the one that does not exist")
	assert.NotSubset(t, orderedLineIDs(fetched), world.atTo.ids(),
		"a line that was not asked for must not come back")

	var got models.OrderLineItem
	for i := range fetched {
		if fetched[i].ID == distinct.ID {
			got = fetched[i]
		}
	}
	require.Equal(t, distinct.ID, got.ID, "the line with the distinct amounts must be in the batch")
	assert.Equal(t, world.inside.id, got.OrderID)
	assert.Equal(t, world.variant, got.VariantID)
	assert.Equal(t, "Every amount different", got.Title)
	assert.Equal(t, int64(7), got.Quantity)
	assert.Equal(t, int64(1234), got.UnitPrice)
	assert.Equal(t, int64(8638), got.Subtotal)
	assert.Equal(t, int64(638), got.DiscountTotal)
	assert.Equal(t, int64(1600), got.TaxTotal)
	assert.Equal(t, int32(2000), got.TaxRateBps)
	assert.Equal(t, int64(9600), got.Total)
	assert.Equal(t, map[string]any{"source": "batch-test"}, got.Metadata)
}

// TestLineItemListingReturnsTheNewestSaleFirst verifies the ORDER BY across
// orders placed on DIFFERENT DAYS.
//
// Two things are being claimed at once and they need different rows to be
// visible. The first is that the primary sort is the ORDER's placed_at
// descending: the fixture's three live orders are placed on two different days,
// so a query sorted by anything the line itself holds — its created_at, its id —
// would interleave them, because the line rows were all written within the same
// second of today.
//
// The second is the tie-break. Two lines of the SAME order share one placed_at
// exactly, and without `li.id DESC` their relative order is whatever the plan
// happens to produce; a page boundary drawn through them would then move
// between two calls over unchanged data, which shows up as a row read twice or
// not at all. The expected sequence is derived from the identifiers the
// database handed back rather than written out, because the identifier carries
// a random component and cannot be predicted.
func TestLineItemListingReturnsTheNewestSaleFirst(t *testing.T) {
	ctx := context.Background()
	world := newLineItemWorld(ctx, t, "ORDERING", time.Date(2019, 6, 10, 0, 0, 0, 0, time.UTC))

	// The window is stretched one second past dayTwo so that the order placed
	// at the upper bound joins the listing; the exclusive bound itself is the
	// subject of TestLineItemDateRangeIsHalfOpen and not of this test.
	listed := readLineIDs(ctx, t, world.repo, models.OrderLineItemFilter{
		VariantID:  ptr(world.variant),
		PlacedFrom: ptr(world.dayOne),
		PlacedTo:   ptr(world.dayTwo.Add(time.Second)),
		Limit:      lineItemReadLimit,
	})

	tieBroken := world.atFrom.ids()
	slices.Sort(tieBroken)
	slices.Reverse(tieBroken)

	expected := slices.Concat(world.atTo.ids(), world.inside.ids(), tieBroken)
	assert.Equal(t, expected, listed,
		"the newest sale comes first and the line identifier breaks the tie inside "+
			"one order")
}

// TestLineItemProviderIsRegisteredInTheContainer verifies the LAST link of the
// chain: that the provider this branch adds is actually reachable by name.
//
// Nothing proved this before. The unit test asserts that the CONSTANT
// [service.LineItemProviderName] spells "order_line_item.query", which is a
// statement about a string; whether Register ever puts anything under that name
// is a different claim, and forgetting the c.Provide call would leave every
// existing test green while the Query layer answered "no such entity" at run
// time. Consumers reach this module only by name (ADR 0001/0004), so the name
// is the whole contract and the compiler checks none of it.
//
// The order module has no other test that exercises Register, so the assertion
// lives here rather than being added to an existing file.
//
// It is not left at "the name resolves". The resolved provider is READ FROM,
// over the same database and through the whole chain — container to provider to
// service to repository to the SQL of this branch — because a name bound to a
// provider that cannot answer is not a registration worth having.
func TestLineItemProviderIsRegisteredInTheContainer(t *testing.T) {
	ctx := context.Background()

	c := container.New(nil)
	t.Cleanup(func() { _ = c.Shutdown(context.Background()) })

	bus := eventbus.NewInMemory(nil)
	t.Cleanup(func() {
		shutdownCtx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		defer cancel()
		if err := bus.Shutdown(shutdownCtx); err != nil {
			t.Logf("the event bus could not be shut down: %v", err)
		}
	})

	// The three CORE services the module resolves in Register. The query layer
	// is handed a real link service rather than a stub: the module resolves it
	// as a hard dependency and would not register at all without it.
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))
	require.NoError(t, c.Provide("core.query", query.New(link.New(testPool, nil), c, nil)))

	mod := order.New()
	require.NoError(t, mod.Register(ctx, c))

	assert.Equal(t, "order_line_item.query", order.LineItemProviderName,
		"the name is the contract a consumer repeats as a string literal (ADR 0001)")

	provider, err := container.Resolve[query.Provider](c, order.LineItemProviderName)
	require.NoError(t, err,
		"the line provider must be resolvable under the name %q", order.LineItemProviderName)
	assert.Equal(t, service.LineItemEntity, provider.Entity(),
		"the provider name's prefix must match Entity() (ADR 0004)")
	assert.Equal(t, "order_line_item", provider.Entity())

	// The order provider must still be there. Registering a second entity is
	// exactly the kind of change that overwrites the first one under a
	// mistyped name.
	orderProvider, err := container.Resolve[query.Provider](c, order.ProviderName)
	require.NoError(t, err)
	assert.Equal(t, service.EntityName, orderProvider.Entity(),
		"the order entity must not have been displaced by the line entity")

	// The registered surfaces really work together, over the real database.
	svc, err := container.Resolve[*service.Service](c, order.ServiceName)
	require.NoError(t, err)

	created, err := svc.CreateOrder(ctx, validInput())
	require.NoError(t, err)
	detail, err := svc.GetOrder(ctx, created.ID)
	require.NoError(t, err)
	require.Len(t, detail.Items, 1)

	records, err := provider.List(ctx, query.ListOptions{
		Filters: map[string]any{service.FieldLineItemOrderID: created.ID},
	})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, detail.Items[0].ID, records[0][query.IDField])
	assert.Equal(t, "variant_A", records[0][service.FieldLineItemVariantID])
	assert.Equal(t, int64(3600), records[0][service.FieldLineItemTotal])
}
