//go:build integration

// The tests in this file require a real PostgreSQL instance (and therefore
// Docker); they are separated behind the `integration` tag so that `make test`
// stays fast. To run them: make test-integration
//
// The unit tests prove the service's DECISIONS with a fake store. The tests
// here prove the GROUND those decisions stand on: that idempotency rests on a
// REAL unique index and not on a fake map, that two concurrent handlers produce
// a single notification, that the record CARRIES the recipient address in no
// column, and that the subscriber is triggered over a real event bus and reads
// the e-mail from the order record instead of from the event.
package notification_test

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	"github.com/bdrtr/gobit/internal/core/container"
	"github.com/bdrtr/gobit/internal/core/db"
	coreerrors "github.com/bdrtr/gobit/internal/core/errors"
	"github.com/bdrtr/gobit/internal/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/internal/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	"github.com/bdrtr/gobit/internal/modules/notification/models"
	"github.com/bdrtr/gobit/internal/modules/notification/repository"
	"github.com/bdrtr/gobit/internal/modules/notification/service"
)

const postgresImage = "postgres:16-alpine"

// Constants used in the test data. The reference belongs to ANOTHER module (to
// the order); this module does not verify its existence (Principle 2.2).
const (
	testEmail    = "customer@example.com"
	testTemplate = service.TemplateOrderPlaced
	testProvider = "test"
)

var (
	// testPool is the pool all the tests share.
	testPool *db.Pool
	// testDSN is the connection address for the migration calls.
	testDSN string
)

func TestMain(m *testing.M) {
	os.Exit(runWithPostgres(m))
}

// runWithPostgres brings up a single Postgres container and runs all the tests
// over it. It is a separate function because os.Exit skips the defers.
func runWithPostgres(m *testing.M) int {
	ctx := context.Background()

	ctr, err := tcpostgres.Run(ctx, postgresImage,
		tcpostgres.WithDatabase("gobit_test"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	defer func() {
		if termErr := testcontainers.TerminateContainer(ctr); termErr != nil {
			fmt.Fprintf(os.Stderr, "the postgres container could not be stopped: %v\n", termErr)
		}
	}()
	if err != nil {
		fmt.Fprintf(os.Stderr, "the postgres container could not be started: %v\n", err)
		return 1
	}

	testDSN, err = ctr.ConnectionString(ctx, "sslmode=disable")
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection address could not be obtained: %v\n", err)
		return 1
	}

	cfg := db.DefaultConfig(testDSN)
	// The concurrency test runs dozens of goroutines at the same time; the pool
	// is opened wider than the default.
	cfg.MaxConns = 24
	testPool, err = db.New(ctx, cfg, nil)
	if err != nil {
		fmt.Fprintf(os.Stderr, "the connection pool could not be opened: %v\n", err)
		return 1
	}
	defer testPool.Close()

	if err := db.Migrate(ctx, testDSN,
		notification.New(notification.Options{}).Migrations(), notification.ModuleName); err != nil {
		fmt.Fprintf(os.Stderr, "the migration could not be applied: %v\n", err)
		return 1
	}

	return m.Run()
}

// fakeProvider is a notification provider that counts what was sent.
type fakeProvider struct {
	mu   sync.Mutex
	sent []coreprovider.Notification
	err  error
}

func (p *fakeProvider) ID() string { return testProvider }

func (p *fakeProvider) Send(_ context.Context, n coreprovider.Notification) error {
	p.mu.Lock()
	defer p.mu.Unlock()

	p.sent = append(p.sent, n)

	return p.err
}

func (p *fakeProvider) count() int {
	p.mu.Lock()
	defer p.mu.Unlock()

	return len(p.sent)
}

func (p *fakeProvider) last() coreprovider.Notification {
	p.mu.Lock()
	defer p.mu.Unlock()

	if len(p.sent) == 0 {
		return coreprovider.Notification{}
	}

	return p.sent[len(p.sent)-1]
}

// fakeOrders stands in for the "order.interop" surface.
//
// The body is given as a STRING: the order module CANNOT BE IMPORTED (Principle
// 2.4) and the only thing the two sides share is the JSON schema. That the
// schema really matches is tested in the order module's own integration test as
// well — the two of these tests together build the link the compiler cannot
// build.
type fakeOrders struct {
	mu        sync.Mutex
	body      string
	calls     int
	requested string
}

func (s *fakeOrders) OrderContactJSON(_ context.Context, orderID string) (json.RawMessage, error) {
	s.mu.Lock()
	defer s.mu.Unlock()

	s.calls++
	s.requested = orderID

	return json.RawMessage(s.body), nil
}

// orderContactBody produces a contact response with the given id and address.
func orderContactBody(orderID, email string) string {
	return fmt.Sprintf(`{"order_id":%q,"display_id":"1042","email":%q,`+
		`"currency_code":"TRY","total":"6100","item_count":"2"}`, orderID, email)
}

// newService sets up a service that works over the REAL store and the given
// provider.
func newService(t *testing.T, prov coreprovider.NotificationProvider, orders service.OrderContactReader) *service.Service {
	t.Helper()

	registry := service.NewProviderRegistry()
	require.NoError(t, registry.Register(prov))

	svc, err := service.New(service.Options{
		Store:      repository.New(testPool.Pool()),
		Providers:  registry,
		ProviderID: prov.ID(),
		Contacts:   orders,
	})
	require.NoError(t, err)

	return svc
}

// uniqueReference produces an order id that prevents collisions between the
// tests.
//
// The tests share the SAME table and the idempotency key is the (template,
// reference) pair; a fixed reference would have meant the second test getting
// caught on the first test's record.
func uniqueReference(t *testing.T) string {
	t.Helper()

	return "order_" + models.NewDeliveryID(time.Now())
}

// TestTheLogDoesNotSENDTheSameTemplateAndReferenceASecondTime verifies that
// idempotency rests on the REAL unique index.
func TestTheLogDoesNotSENDTheSameTemplateAndReferenceASecondTime(t *testing.T) {
	prov := &fakeProvider{}
	svc := newService(t, prov, &fakeOrders{})
	ctx := context.Background()
	reference := uniqueReference(t)

	input := service.NotifyInput{
		Template:  testTemplate,
		Channel:   coreprovider.ChannelEmail,
		Reference: reference,
		To:        testEmail,
		Data:      map[string]string{"order_id": reference},
	}

	require.NoError(t, svc.Notify(ctx, input))
	require.NoError(t, svc.Notify(ctx, input), "the second call must not be an error but a silent skip")

	assert.Equal(t, 1, prov.count(), "the provider must be gone to ONLY once")

	records, total, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{Reference: &reference})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total)
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliverySent, records[0].Status)
}

// TestTwoConcurrentSendsProduceOneNotification exercises the race over REAL
// rows.
//
// The fake store provides this with a map and a single lock; here the winner is
// chosen by PostgreSQL. Had "read first, write if absent" been written, this
// test would have seen two notifications — the customer would have received two
// e-mails.
func TestTwoConcurrentSendsProduceOneNotification(t *testing.T) {
	prov := &fakeProvider{}
	svc := newService(t, prov, &fakeOrders{})
	ctx := context.Background()
	reference := uniqueReference(t)

	const concurrent = 8
	var wg sync.WaitGroup
	errs := make(chan error, concurrent)

	for range concurrent {
		wg.Add(1)
		go func() {
			defer wg.Done()
			errs <- svc.Notify(ctx, service.NotifyInput{
				Template:  testTemplate,
				Channel:   coreprovider.ChannelEmail,
				Reference: reference,
				To:        testEmail,
			})
		}()
	}
	wg.Wait()
	close(errs)

	for err := range errs {
		require.NoError(t, err, "none of the concurrent calls must return an error")
	}

	assert.Equal(t, 1, prov.count(), "only one of the %d concurrent calls must perform a send", concurrent)

	_, total, err := svc.ListDeliveries(ctx, service.ListDeliveriesInput{Reference: &reference})
	require.NoError(t, err)
	assert.Equal(t, int64(1), total, "there must be a single record in the log")
}

// TestAProviderErrorWritesFAILEDIntoTheLog verifies that the error is both
// written into the record and returned to the caller.
func TestAProviderErrorWritesFAILEDIntoTheLog(t *testing.T) {
	prov := &fakeProvider{err: coreerrors.Unavailable("smtp_down", "the provider could not be reached")}
	svc := newService(t, prov, &fakeOrders{})
	ctx := context.Background()
	reference := uniqueReference(t)

	err := svc.Notify(ctx, service.NotifyInput{
		Template:  testTemplate,
		Channel:   coreprovider.ChannelEmail,
		Reference: reference,
		To:        testEmail,
	})

	require.Error(t, err)
	assert.Equal(t, service.CodeSendFailed, coreerrors.CodeOf(err))

	records, _, listErr := svc.ListDeliveries(ctx, service.ListDeliveriesInput{Reference: &reference})
	require.NoError(t, listErr)
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliveryFailed, records[0].Status)
	assert.Contains(t, records[0].Error, "the provider could not be reached")

	// The status filter has to work on the real query as well; "show the failed
	// notifications" is the log's second most frequent question.
	status := models.DeliveryFailed.String()
	failed, _, listErr := svc.ListDeliveries(ctx,
		service.ListDeliveriesInput{Reference: &reference, Status: &status})
	require.NoError(t, listErr)
	assert.Len(t, failed, 1)
}

// TestTheLogCarriesNORecipientAddressCOLUMN verifies at the SCHEMA level that
// the record carries no personal data.
//
// An assertion at the code level would not have been enough: code that does not
// write today can write tomorrow as long as the column exists. The column not
// existing at all keeps the number of places to be cleaned on a KVKK/GDPR
// deletion request constant.
func TestTheLogCarriesNORecipientAddressCOLUMN(t *testing.T) {
	ctx := context.Background()

	rows, err := testPool.Pool().Query(ctx,
		`SELECT column_name FROM information_schema.columns
		 WHERE table_name = 'notification_deliveries'`)
	require.NoError(t, err)
	defer rows.Close()

	var columns []string
	for rows.Next() {
		var name string
		require.NoError(t, rows.Scan(&name))
		columns = append(columns, name)
	}
	require.NoError(t, rows.Err())

	assert.ElementsMatch(t, []string{
		"id", "template", "channel", "reference", "provider_id",
		"status", "error", "created_at", "updated_at",
	}, columns, "the log MUST NOT HAVE a column that would hold the recipient address")
}

// TestTheSubscriberReadsFromTheRecordNotFromTheEvent sets up the notification
// path END TO END.
//
// Everything that can be real is real: a real database, a real event bus, the
// module's own Register and the order surface resolved from the container BY
// NAME. The test pins down three claims at once:
//
//  1. The module REALLY subscribes to the "order.placed" event (in Register).
//  2. The event payload CARRIES NO E-MAIL; the subscriber reads the address
//     from the record over "order.interop".
//  3. A provider coming from a plugin is used even when it is registered AFTER
//     the module has been REGISTERED — because the resolution is done at send
//     time. coreplugin.Registry's two-phase structure rests on exactly this.
func TestTheSubscriberReadsFromTheRecordNotFromTheEvent(t *testing.T) {
	ctx := context.Background()
	reference := uniqueReference(t)

	bus := eventbus.NewInMemory(nil)
	orders := &fakeOrders{body: orderContactBody(reference, testEmail)}

	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))
	require.NoError(t, c.Provide(service.OrderInteropName, orders))

	mod := notification.New(notification.Options{ProviderID: testProvider})
	require.NoError(t, mod.Register(ctx, c))

	// The provider is added AFTER Register; the plugin system does exactly this
	// too (the registrations are applied after the modules have come up).
	prov := &fakeProvider{}
	require.NoError(t, mod.Providers().Register(prov))

	// The event's payload has the SAME shape as the one the order module
	// publishes and it DOES NOT CONTAIN the e-mail; that is what the test rests
	// on.
	require.NoError(t, bus.Publish(ctx, eventbus.Event{
		Name: service.EventOrderPlaced,
		Data: map[string]any{
			"order_id":      reference,
			"display_id":    "1042",
			"status":        "pending",
			"currency_code": "TRY",
			"total":         "6100",
			"item_count":    "2",
		},
	}))

	// Shutdown waits for the running handlers to finish; the bus's own contract
	// is used instead of a polling loop.
	require.NoError(t, bus.Shutdown(ctx))

	require.Equal(t, 1, prov.count(), "the subscriber must be triggered and must go to the provider")
	sent := prov.last()
	assert.Equal(t, testEmail, sent.To, "the address must be read FROM THE RECORD")
	assert.Equal(t, coreprovider.ChannelEmail, sent.Channel)
	assert.Equal(t, testTemplate, sent.Template)
	assert.Equal(t, reference, sent.Data["order_id"])
	assert.Equal(t, "6100", sent.Data["total"], "the amount must be carried as a string")

	require.Equal(t, 1, orders.calls, "the order record must be read")
	assert.Equal(t, reference, orders.requested)

	records, _, err := mod.Service().ListDeliveries(ctx,
		service.ListDeliveriesInput{Reference: &reference})
	require.NoError(t, err)
	require.Len(t, records, 1)
	assert.Equal(t, models.DeliverySent, records[0].Status)
	assert.Equal(t, testProvider, records[0].ProviderID)
}

// TestTheModuleRegistersWithTheDefaultProvider verifies that the out-of-the-box
// setup is complete: even when there is no plugin at all there is a provider,
// and the selected id finds it.
func TestTheModuleRegistersWithTheDefaultProvider(t *testing.T) {
	ctx := context.Background()

	bus := eventbus.NewInMemory(nil)
	c := container.New(nil)
	require.NoError(t, c.Provide("core.db", testPool))
	require.NoError(t, c.Provide("core.eventbus", bus))

	mod := notification.New(notification.Options{})
	require.NoError(t, mod.Register(ctx, c))
	t.Cleanup(func() { require.NoError(t, bus.Shutdown(ctx)) })

	assert.Equal(t, []string{logonly.ID}, mod.Providers().IDs())
	assert.Equal(t, notification.DefaultProviderID, mod.Service().ProviderID())

	registry, err := container.Resolve[*service.ProviderRegistry](c, notification.ProvidersName)
	require.NoError(t, err, "the provider registry must be found in the container under the name %q",
		notification.ProvidersName)
	assert.Same(t, mod.Providers(), registry)
}
