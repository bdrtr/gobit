//go:build integration

package pricing_test

import (
	"context"
	"io/fs"
	"slices"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/db"
	"github.com/bdrtr/gobit/internal/modules/pricing"
	"github.com/bdrtr/gobit/internal/modules/pricing/models"
	"github.com/bdrtr/gobit/internal/modules/pricing/repository"
	"github.com/bdrtr/gobit/internal/modules/pricing/service"
	"github.com/bdrtr/gobit/internal/testdb"
)

// The price history against a real server (ADR 0167).
//
// The unit tests prove the ladder's past is rebuilt right from snapshots they
// plant. These prove the snapshots exist: that every write the module can make
// leaves one, in its own transaction, in the shape the reader decodes — the
// migration's seed included, which is written in SQL and read in Go.

// historyClock is a clock a test moves by hand, so a snapshot's moment is the
// test's choice.
type historyClock struct{ now time.Time }

func (c *historyClock) Now() time.Time { return c.now }

// historyService builds a real-repository service on a clock the test moves.
func historyService(t *testing.T, start time.Time) (*service.Service, *repository.Repo, *historyClock) {
	t.Helper()

	clock := &historyClock{now: start}
	repo := repository.New(testPool.Pool())

	return service.New(repo, service.Options{Now: clock.Now}), repo, clock
}

// setSnapshots reads every snapshot of one set.
func setSnapshots(ctx context.Context, t *testing.T, repo *repository.Repo, id string) []models.PriceSetSnapshot {
	t.Helper()

	snapshots, err := repo.PriceSetHistory(ctx, []string{id})
	require.NoError(t, err)

	return snapshots
}

// listSnapshots reads every snapshot of one list.
func listSnapshots(ctx context.Context, t *testing.T, repo *repository.Repo, id string) []models.PriceListSnapshot {
	t.Helper()

	snapshots, err := repo.PriceListHistory(ctx, []string{id})
	require.NoError(t, err)

	return snapshots[id]
}

// TestEveryWriterLeavesASnapshot walks the eight writes the module can make and
// reads the history after each.
//
// A writer that changed what the ladder reads without a snapshot would leave
// the history wrong from that moment on, and the reduction a storefront
// announces would be computed from a catalog that never existed. The arch gate
// holds the pairing in the source; this is the pairing on the server.
func TestEveryWriterLeavesASnapshot(t *testing.T) {
	ctx := context.Background()
	start := time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)
	svc, repo, clock := historyService(t, start)

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title: "History sale", Type: models.PriceListSale, Status: models.PriceListDraft,
	})
	require.NoError(t, err)
	require.Len(t, listSnapshots(ctx, t, repo, list.ID), 1, "creating a list records it")

	clock.now = start.Add(time.Hour)
	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10_000},
		{CurrencyCode: "TRY", Amount: 7_000, PriceListID: &list.ID},
	})
	require.NoError(t, err)
	snapshots := setSnapshots(ctx, t, repo, set.ID)
	require.Len(t, snapshots, 1, "creating a set records it")
	assert.Len(t, snapshots[0].Prices, 2)
	assert.Equal(t, clock.now, snapshots[0].RecordedAt.UTC(), "on the application's clock")

	clock.now = start.Add(2 * time.Hour)
	written, err := svc.SetPrices(ctx, set.ID, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 9_000},
		{CurrencyCode: "TRY", Amount: 6_500, PriceListID: &list.ID},
	})
	require.NoError(t, err)
	snapshots = setSnapshots(ctx, t, repo, set.ID)
	require.Len(t, snapshots, 2, "replacing the prices records the replacement")
	assert.Equal(t, int64(10_000), basePrice(t, snapshots[0]).Amount,
		"the replaced price is deleted from the table and kept in the snapshot before")

	base := written[0]
	if base.PriceListID != nil {
		base = written[1]
	}

	clock.now = start.Add(3 * time.Hour)
	rule, err := svc.CreatePriceRule(ctx, base.ID, service.RuleInput{
		Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_1"},
	})
	require.NoError(t, err)
	snapshots = setSnapshots(ctx, t, repo, set.ID)
	require.Len(t, snapshots, 3, "a rule takes its price out of the storefront, so it is recorded")
	assert.Len(t, ruledPrice(t, snapshots[2], base.ID).Rules, 1)

	clock.now = start.Add(4 * time.Hour)
	require.NoError(t, svc.DeletePriceRule(ctx, rule.ID))
	snapshots = setSnapshots(ctx, t, repo, set.ID)
	require.Len(t, snapshots, 4, "removing the rule is recorded")
	assert.Empty(t, ruledPrice(t, snapshots[3], base.ID).Rules)

	clock.now = start.Add(5 * time.Hour)
	_, err = svc.UpdatePriceList(ctx, list.ID, service.PriceListInput{
		Title: list.Title, Type: models.PriceListSale, Status: models.PriceListActive,
	})
	require.NoError(t, err)
	lists := listSnapshots(ctx, t, repo, list.ID)
	require.Len(t, lists, 2, "activating a list is recorded")
	assert.Equal(t, models.PriceListActive, lists[1].Info.Status)

	clock.now = start.Add(6 * time.Hour)
	require.NoError(t, svc.DeletePriceList(ctx, list.ID))
	lists = listSnapshots(ctx, t, repo, list.ID)
	require.Len(t, lists, 3, "deleting a list is recorded")
	assert.True(t, lists[2].Deleted)

	clock.now = start.Add(7 * time.Hour)
	require.NoError(t, svc.DeletePriceSet(ctx, set.ID))
	snapshots = setSnapshots(ctx, t, repo, set.ID)
	require.Len(t, snapshots, 5, "deleting a set is recorded")
	assert.Empty(t, snapshots[4].Prices, "a deleted set offers nothing from then on")

	clock.now = start.Add(8 * time.Hour)
	_, err = svc.CreatePriceRule(ctx, base.ID, service.RuleInput{
		Attribute: "region_id", Operator: models.OpEq, Values: []string{"reg_2"},
	})
	require.NoError(t, err, "a rule can still be written to a deleted price")
	assert.Len(t, setSnapshots(ctx, t, repo, set.ID), 5,
		"and it changes nothing the ladder reads, so it records nothing")
}

// basePrice finds the snapshot's price that belongs to no list. Prices are in
// id order, and two ids minted in one instant are in no order the test can know.
func basePrice(t *testing.T, snapshot models.PriceSetSnapshot) models.Price {
	t.Helper()

	for i := range snapshot.Prices {
		if snapshot.Prices[i].PriceListID == nil {
			return snapshot.Prices[i]
		}
	}
	t.Fatal("the snapshot holds no base price")

	return models.Price{}
}

// ruledPrice finds one price in a snapshot.
func ruledPrice(t *testing.T, snapshot models.PriceSetSnapshot, id string) models.Price {
	t.Helper()

	for i := range snapshot.Prices {
		if snapshot.Prices[i].ID == id {
			return snapshot.Prices[i]
		}
	}
	t.Fatalf("the snapshot holds no price %s", id)

	return models.Price{}
}

// pricesByID returns the prices and each price's rules sorted by id, the order
// the history's seed records them in.
func pricesByID(prices []models.Price) []models.Price {
	out := slices.Clone(prices)
	for i := range out {
		out[i].Rules = slices.Clone(out[i].Rules)
		slices.SortFunc(out[i].Rules, func(a, b models.PriceRule) int { return strings.Compare(a.ID, b.ID) })
	}
	slices.SortFunc(out, func(a, b models.Price) int { return strings.Compare(a.ID, b.ID) })
	return out
}

// TestTheSeedIsReadAsTheWriterWrites rolls the history back over live data and
// forward again, so the migration's seed runs on real prices.
//
// The seed is SQL building JSON and the reader is Go decoding it; they are one
// contract written twice, and the only way to know they agree is to decode what
// the seed wrote. It runs in a database of its own because it rolls a
// migration back.
func TestTheSeedIsReadAsTheWriterWrites(t *testing.T) {
	ctx := context.Background()

	dsn := testdb.New(t, testDSN, "pricing_history_seed")

	src := pricing.New(nil).Migrations()
	require.NoError(t, db.Migrate(ctx, dsn, src, pricing.Name))

	pool, err := db.New(ctx, db.DefaultConfig(dsn), nil)
	require.NoError(t, err)
	t.Cleanup(pool.Close)

	repo := repository.New(pool.Pool())
	svc := service.New(repo, service.Options{})

	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title: "Seed sale", Type: models.PriceListSale, Status: models.PriceListActive,
	})
	require.NoError(t, err)
	maxQuantity := int32(9)
	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 10_000},
		{CurrencyCode: "TRY", Amount: 7_000, PriceListID: &list.ID, MinQuantity: 2, MaxQuantity: &maxQuantity},
		{CurrencyCode: "EUR", Amount: 300, Rules: []service.RuleInput{
			{Attribute: "region_id", Operator: models.OpIn, Values: []string{"reg_1", "reg_2"}},
		}},
	})
	require.NoError(t, err)

	written := setSnapshots(ctx, t, repo, set.ID)
	require.Len(t, written, 1)

	// The history's own migration and every one after it roll back, so the seed
	// runs again; the count is read from the set, since newer migrations sit
	// above it.
	ups, err := fs.Glob(src, "*.up.sql")
	require.NoError(t, err)
	steps := 0
	for _, name := range ups {
		if name >= "000004_a_price_keeps_its_history.up.sql" {
			steps++
		}
	}
	require.Positive(t, steps, "the history's migration is in the set")
	require.NoError(t, db.MigrateDown(ctx, dsn, src, pricing.Name, steps),
		"the history's own migration rolls back and leaves the prices")
	require.NoError(t, db.Migrate(ctx, dsn, src, pricing.Name))

	// The seed runs before seq exists (D175), so it cannot know the order the
	// prices were written in and records them by id; what it has to agree with
	// the writer on is the content.
	seeded := setSnapshots(ctx, t, repo, set.ID)
	require.Len(t, seeded, 1, "the seed records every live set once")
	assert.Equal(t, pricesByID(written[0].Prices), pricesByID(seeded[0].Prices),
		"the seed's JSON decodes to exactly what the writer records, rules and bounds included")

	lists := listSnapshots(ctx, t, repo, list.ID)
	require.Len(t, lists, 1)
	assert.Equal(t, models.PriceListSale, lists[0].Info.Type)
	assert.Equal(t, models.PriceListActive, lists[0].Info.Status)
	assert.False(t, lists[0].Deleted)
}

// TestAReductionIsReadFromTheRealHistory is the decision end to end: a base
// price that moved, a sale that opened with its window, and both storefront
// surfaces announcing the reduction with the lowest price of the thirty days
// before it.
func TestAReductionIsReadFromTheRealHistory(t *testing.T) {
	ctx := context.Background()
	day0 := time.Date(2026, 4, 1, 12, 0, 0, 0, time.UTC)
	day := func(n int) time.Time { return day0.AddDate(0, 0, n) }
	svc, _, clock := historyService(t, day(0))

	set, err := svc.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 10_000}})
	require.NoError(t, err)

	clock.now = day(20)
	_, err = svc.SetPrices(ctx, set.ID, []service.PriceInput{{CurrencyCode: "TRY", Amount: 9_000}})
	require.NoError(t, err)

	clock.now = day(25)
	_, err = svc.SetPrices(ctx, set.ID, []service.PriceInput{{CurrencyCode: "TRY", Amount: 11_000}})
	require.NoError(t, err)

	clock.now = day(30)
	opens := day(40)
	list, err := svc.CreatePriceList(ctx, service.PriceListInput{
		Title: "Spring", Type: models.PriceListSale, Status: models.PriceListActive, StartsAt: &opens,
	})
	require.NoError(t, err)
	_, err = svc.SetPrices(ctx, set.ID, []service.PriceInput{
		{CurrencyCode: "TRY", Amount: 11_000},
		{CurrencyCode: "TRY", Amount: 8_000, PriceListID: &list.ID},
	})
	require.NoError(t, err)

	clock.now = day(50)

	prices, err := svc.ListStorePrices(ctx, set.ID)
	require.NoError(t, err)
	var sale *models.StorePrice
	for i := range prices {
		if prices[i].ListType == models.PriceListSale {
			sale = &prices[i]
		}
	}
	require.NotNil(t, sale, "the sale price is listed, and says it is one")
	require.NotNil(t, sale.Reduction, "it is the price charged now, so it carries its reduction")
	require.NotNil(t, sale.Reduction.ReducedSince)
	assert.Equal(t, day(40), sale.Reduction.ReducedSince.UTC(),
		"the reduction began when the window opened, with no write on that day")
	require.NotNil(t, sale.Reduction.LowestPrior)
	assert.Equal(t, int64(9_000), *sale.Reduction.LowestPrior,
		"the lowest of days 10 to 40 — not the 11,000 the price was raised to before the sale")

	records, err := service.NewQueryProvider(svc).FetchByIDs(ctx, []string{set.ID}, nil)
	require.NoError(t, err)
	require.Len(t, records, 1)
	listed, ok := records[0]["prices"].([]map[string]any)
	require.True(t, ok)
	var announced map[string]any
	for _, price := range listed {
		if price["price_list_type"] == "sale" {
			announced = price
		}
	}
	require.NotNil(t, announced, "the product listing says the same as the price set endpoint")
	assert.Equal(t, int64(9_000), announced["lowest_prior_amount"])

	timeline, err := svc.PriceTimeline(ctx, set.ID, service.TimelineQuery{CurrencyCode: "TRY"})
	require.NoError(t, err)
	assert.True(t, timeline.Covers(timeline.From), "fifty days of history cover the last thirty")
	lowest, ok := timeline.Lowest(timeline.From, timeline.To)
	require.True(t, ok)
	assert.Equal(t, int64(8_000), lowest.Amount, "the last thirty days' lowest is the sale itself")
}

// tickingClock is one wall clock two services share: every reading is a
// second after the one before.
type tickingClock struct {
	mu   sync.Mutex
	next time.Time
}

// Now returns the next moment.
func (c *tickingClock) Now() time.Time {
	c.mu.Lock()
	defer c.mu.Unlock()
	now := c.next
	c.next = c.next.Add(time.Second)

	return now
}

// pausingReader reads the shared clock and, at its at-th reading, holds its
// caller until released: the shape of a writer that read its moment and then
// waited. A zero at holds the first reading.
type pausingReader struct {
	clock    *tickingClock
	at       int
	mu       sync.Mutex
	readings int
	read     chan struct{}
	release  chan struct{}
}

// Now reads the clock, and holds the reader at the chosen reading.
func (p *pausingReader) Now() time.Time {
	now := p.clock.Now()
	p.mu.Lock()
	p.readings++
	hold := p.readings == max(p.at, 1)
	p.mu.Unlock()
	if hold {
		close(p.read)
		<-p.release
	}

	return now
}

// TestThePricesWrittenLastAreTheStandingOnes is ADR 0242 on the price history:
// a replacement that read the clock and waited is written after the one that
// held the set's lock, and its prices are the live ones. Its snapshot was
// stamped with the moment read before the wait, earlier than the other's, and
// the history, whose latest snapshot is the standing price, answered with the
// prices that had been replaced.
func TestThePricesWrittenLastAreTheStandingOnes(t *testing.T) {
	ctx := context.Background()
	clock := &tickingClock{next: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	repo := repository.New(testPool.Pool())
	plain := service.New(repo, service.Options{Now: clock.Now})
	set, err := plain.CreatePriceSet(ctx, []service.PriceInput{{CurrencyCode: "TRY", Amount: 10_000}})
	require.NoError(t, err)

	// The replacement reads the clock twice: the service for the prices' ids,
	// and the repository for the stamp. The second reading is held, since the
	// stamp is what has to come after the lock.
	reader := &pausingReader{clock: clock, at: 2, read: make(chan struct{}), release: make(chan struct{})}
	waiting := service.New(repo, service.Options{Now: reader.Now})
	waited := make(chan error, 1)
	go func() {
		_, err := waiting.SetPrices(ctx, set.ID, []service.PriceInput{{CurrencyCode: "TRY", Amount: 9_000}})
		waited <- err
	}()
	select {
	case <-reader.read:
	case err := <-waited:
		t.Fatalf("the replacement read the clock once and finished (%v); its stamp was read before the lock", err)
	}

	other := make(chan error, 1)
	go func() {
		_, err := plain.SetPrices(ctx, set.ID, []service.PriceInput{{CurrencyCode: "TRY", Amount: 8_000}})
		other <- err
	}()
	// The other replacement finishes unless the held one already has the set's
	// lock; the wait is bounded so that shape cannot hang the test.
	select {
	case err := <-other:
		require.NoError(t, err)
		other <- nil
	case <-time.After(500 * time.Millisecond):
	}
	close(reader.release)
	require.NoError(t, <-waited)
	require.NoError(t, <-other)

	live, err := plain.ListPrices(ctx, set.ID)
	require.NoError(t, err)
	snapshots := setSnapshots(ctx, t, repo, set.ID)
	require.NotEmpty(t, snapshots)
	var liveBase int64
	for _, p := range live {
		if p.PriceListID == nil {
			liveBase = p.Amount
		}
	}
	assert.Equal(t, liveBase, basePrice(t, snapshots[len(snapshots)-1]).Amount,
		"the latest snapshot holds the prices the set holds")
}

// TestTheListUpdateWrittenLastIsTheStandingOne is ADR 0242 on a price list: an
// update that read the clock and waited is written after the one that held the
// list, and its status is the live one, which the history's latest snapshot
// has to say.
func TestTheListUpdateWrittenLastIsTheStandingOne(t *testing.T) {
	ctx := context.Background()
	clock := &tickingClock{next: time.Date(2026, 5, 1, 9, 0, 0, 0, time.UTC)}
	repo := repository.New(testPool.Pool())
	plain := service.New(repo, service.Options{Now: clock.Now})
	list, err := plain.CreatePriceList(ctx, service.PriceListInput{
		Title: "Raced sale", Type: models.PriceListSale, Status: models.PriceListDraft,
	})
	require.NoError(t, err)

	reader := &pausingReader{clock: clock, read: make(chan struct{}), release: make(chan struct{})}
	waiting := service.New(repo, service.Options{Now: reader.Now})
	waited := make(chan error, 1)
	go func() {
		_, err := waiting.UpdatePriceList(ctx, list.ID, service.PriceListInput{
			Title: "Raced sale", Type: models.PriceListSale, Status: models.PriceListActive,
		})
		waited <- err
	}()
	<-reader.read

	other := make(chan error, 1)
	go func() {
		_, err := plain.UpdatePriceList(ctx, list.ID, service.PriceListInput{
			Title: "Raced sale", Type: models.PriceListSale, Status: models.PriceListDraft,
		})
		other <- err
	}()
	select {
	case err := <-other:
		require.NoError(t, err)
		other <- nil
	case <-time.After(500 * time.Millisecond):
	}
	close(reader.release)
	require.NoError(t, <-waited)
	require.NoError(t, <-other)

	live, err := plain.GetPriceList(ctx, list.ID)
	require.NoError(t, err)
	snapshots := listSnapshots(ctx, t, repo, list.ID)
	require.NotEmpty(t, snapshots)
	assert.Equal(t, live.Status, snapshots[len(snapshots)-1].Info.Status,
		"the latest snapshot holds the list's status")
}
