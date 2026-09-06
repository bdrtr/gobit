//go:build integration

package e2e

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net/http"
	"strconv"
	"sync"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/eventbus"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	notificationmod "github.com/bdrtr/gobit/internal/modules/notification"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
	notificationsvc "github.com/bdrtr/gobit/internal/modules/notification/service"
	ordersvc "github.com/bdrtr/gobit/internal/modules/order/service"
	"github.com/bdrtr/gobit/internal/modules/payment/manual"
	checkoutwf "github.com/bdrtr/gobit/internal/workflows/checkout"
)

// This file proves the ORDER -> EVENT -> NOTIFICATION chain end to end: a cart is
// turned into an order, the order module publishes "order.placed", the notification
// module receives the event, reads the order's contact details from the REAL order
// module, goes to the provider and writes the attempt into the delivery log.
//
// Four claims are proven:
//
//  1. A completed order drops a SINGLE record into the log and that record's
//     reference is that order's identifier.
//  2. The RECIPIENT of the notification that reaches the provider is the order's
//     email — the address is NOT in the event, which means the subscriber read it
//     from the record.
//  3. A second event for the same order does NOT produce a second notification.
//  4. The log is read from the admin endpoint and an unauthenticated request
//     gets 401.
//
// # Why this test is IN ADDITION to the module's own integration test
//
// The notification module CANNOT import order (Principle 2.4); in its own test the
// order surface is a fake, and that fake is a hand-written JSON schema. If the two
// sides' schemas diverge (if order renames a field) that test stays green and every
// order notification would be dropped in production. Here both sides are REAL; the
// divergence can only be seen here.
//
// # Why the provider is NOT the out-of-the-box "log" one
//
// The second claim — "the recipient is the order's email" — requires a place that
// SEES the address. The address, however, is stored nowhere, and that is
// deliberate: the delivery log has no column for it and the "log" provider does not
// log it either. That leaves a single place — the provider itself. This is why the
// harness has a SPY ([notificationProviderSpy]) do the sending.
//
// The spy is NOT A FAKE MODULE: the whole chain (subscription, order read, opening
// the record, resolving the provider, writing the outcome) is production code and
// the spy stands exactly where a plugin provider would stand. The out-of-the-box
// "log" provider REMAINS in the registry as well; both are verified by
// TestNotificationProviderRegistryKeepsDefault below.
//
// No email GOES anywhere; the spy only holds what it is handed.

// deliveryLogPath is the admin endpoint of the delivery log listing.
//
// The path is written out BY HAND: it is unexported in the notification/api package
// and the only reason to export it would be this test. What is really being proven
// is the path itself — this is the address the client knows.
const deliveryLogPath = "/admin/v1/notifications"

// Fixture constants of the notification scenario.
const (
	notificationUnitPrice   int64 = 30_000
	notificationQuantity    int64 = 1
	notificationStock       int64 = 5
	notificationWaitTimeout       = 5 * time.Second
	notificationInterval          = 25 * time.Millisecond
)

// notificationSpyID is the identifier of the spy provider (the
// NOTIFICATION_PROVIDER value).
//
// The name collides with no out-of-the-box provider and its "e2e" prefix says where
// it came from: it shows up as provider_id in the delivery log, and seeing it in a
// production installation is proof that the installation is wrong.
const notificationSpyID = "e2e-spy"

// notificationDataKeyOrderID is the order identifier key in the template data.
//
// The name is repeated by hand because the notification module's template data keys
// are unexported and must stay that way: the side that reads them is the provider,
// and a provider is FOREIGN code that reads the names as strings (most of the time
// a plugin). The spy behaves exactly that way; exporting the constant would mean
// the test looks on from a more privileged position than the provider does.
const notificationDataKeyOrderID = "order_id"

// notificationSpy is the only notification provider in the harness.
//
// The instance is PROCESS LIFETIME and all tests share it: a provider registration
// cannot be undone, just like a subscription, so registering a spy per test would
// give errors.Conflict on the second test. Tests filter their own notifications BY
// ORDER IDENTIFIER.
var notificationSpy = &notificationProviderSpy{}

// notificationProviderSpy is a notification provider that holds on to the
// notifications it is handed and sends them nowhere.
//
// It is safe for concurrent use: Send is called from an event handler and
// [eventbus.EventBus]'s in-memory backend runs every handler in its own goroutine.
type notificationProviderSpy struct {
	mu       sync.Mutex
	captured []coreprovider.Notification
}

// That the spy satisfies the core contract is pinned at compile time; if it did
// not, this would break here rather than while the harness is being built.
var _ coreprovider.NotificationProvider = (*notificationProviderSpy)(nil)

// ID returns the spy's identifier.
func (s *notificationProviderSpy) ID() string { return notificationSpyID }

// Send records the notification and returns SUCCESS.
//
// Returning success is a real choice: by the contract, returning an error does not
// mean "it did not go out" and the record would be "failed" — whereas the path
// these tests exercise is the happy path. How a provider error is written into the
// log lives in the notification module's own integration test.
//
// Data is COPIED: the contract does not guarantee that the caller will leave the
// map alone after the call, and a stored reference could lead to the test reading a
// payload that has changed in the meantime.
func (s *notificationProviderSpy) Send(_ context.Context, n coreprovider.Notification) error {
	copied := n
	copied.Data = maps.Clone(n.Data)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.captured = append(s.captured, copied)
	return nil
}

// notificationsFor returns the captured notifications belonging to the given order.
//
// The filter goes through the order identifier in the template data; filtering by
// RECIPIENT ADDRESS would be assuming exactly the thing that is meant to be proven.
func (s *notificationProviderSpy) notificationsFor(orderID string) []coreprovider.Notification {
	s.mu.Lock()
	defer s.mu.Unlock()

	var found []coreprovider.Notification
	for i := range s.captured {
		if s.captured[i].Data[notificationDataKeyOrderID] == orderID {
			found = append(found, s.captured[i])
		}
	}
	return found
}

// setUpNotificationSpy adds the spy to the notification module's provider registry.
//
// The registry is resolved from the container BY NAME and it happens AFTER the
// modules have come up; that is the path the plugin system follows too
// (coreplugin.Host's RegisterNotificationProvider resolves the same name and calls
// the same Register). Resolving the registry directly instead of writing a PLUGIN
// into the harness for the test does not change what is being exercised, but it
// does keep a plugin that does not exist in production out of the installation.
//
// The call cannot be made before Bootstrap: "notification.providers" is put into
// the container by the module's Register.
func setUpNotificationSpy() error {
	registry, err := container.Resolve[*notificationsvc.ProviderRegistry](ctr, notificationmod.ProvidersName)
	if err != nil {
		return err
	}
	return registry.Register(notificationSpy)
}

// notificationRecord is the response body of the delivery log endpoint.
//
// The schema is defined AGAIN here, independently of the notification module's DTO:
// what is being exercised is the JSON the client sees, and using the module's type
// would have meant the test stays green even when a field name changes.
type notificationRecord struct {
	ID         string `json:"id"`
	Template   string `json:"template"`
	Channel    string `json:"channel"`
	Reference  string `json:"reference"`
	ProviderID string `json:"provider_id"`
	Status     string `json:"status"`
	Error      string `json:"error"`
}

// fetchNotifications reads an order's delivery records from the admin endpoint and
// returns trouble as an ERROR.
//
// The distinction is mandatory: require.Eventually and require.Never run the
// condition in a SEPARATE GOROUTINE, whereas t.FailNow may only be called from the
// test's own goroutine. A helper that used require inside a wait would leave the
// test HANGING instead of failing it when the endpoint breaks — that is, it would
// hide the real fault behind the timeout.
func fetchNotifications(t *testing.T, token, reference string) ([]notificationRecord, error) {
	t.Helper()

	response := adminRequest(t, http.MethodGet,
		deliveryLogPath+"?reference="+reference, "Bearer "+token)
	if response.Code != http.StatusOK {
		return nil, fmt.Errorf("delivery log returned %d; body: %s", response.Code, response.Body.String())
	}

	var envelope struct {
		Data  []notificationRecord `json:"data"`
		Count int64                `json:"count"`
	}
	if err := json.Unmarshal(response.Body.Bytes(), &envelope); err != nil {
		return nil, fmt.Errorf("response could not be decoded (%w); body: %s", err, response.Body.String())
	}
	if envelope.Count != int64(len(envelope.Data)) {
		return nil, fmt.Errorf(
			"the count in the envelope (%d) and the number of records returned (%d) must match for this filter",
			envelope.Count, len(envelope.Data))
	}

	return envelope.Data, nil
}

// readNotifications reads the delivery records; if it cannot, it fails the test.
func readNotifications(t *testing.T, token, reference string) []notificationRecord {
	t.Helper()

	records, err := fetchNotifications(t, token, reference)
	require.NoError(t, err, "the delivery log must be readable")
	return records
}

// awaitNotification waits until the order's delivery record reaches its FINAL
// state.
//
// The wait is MANDATORY: the subscriber is triggered over [eventbus.EventBus],
// Publish does NOT WAIT for the handlers and the in-memory backend runs every
// handler in its own goroutine — even when the order has been written, the
// notification record may not have been written yet.
//
// Waiting until the final state (anything other than "pending") has a second
// benefit: the outcome is only written after the provider has RETURNED, so looking
// at the spy's ledger once this call comes back is not open to a race.
//
// # Why NOT require.Eventually
//
// The rest of the package uses require.Eventually; here it is not enough, and there
// is a single reason: the condition runs in a SEPARATE GOROUTINE. That has two
// consequences — (1) require cannot be used inside the condition, because t.FailNow
// may only be called from the test's own goroutine; (2) Eventually's message
// arguments are evaluated AT CALL TIME, that is before the condition has run at
// all, so there is no way to carry the last error the condition saw into the
// message. EventuallyWithT solves both: the assertions inside the condition are
// collected and on timeout the failures of the LAST round are printed. The
// difference shows up in diagnosis — a broken endpoint is reported with the status
// it returned, not as "no notification was written".
func awaitNotification(t *testing.T, token, reference string) notificationRecord {
	t.Helper()

	var found notificationRecord
	require.EventuallyWithT(t, func(c *assert.CollectT) {
		records, err := fetchNotifications(t, token, reference)
		if !assert.NoError(c, err, "the delivery log must be readable") {
			return
		}
		if !assert.NotEmpty(c, records, "a delivery record must be opened for order %s", reference) {
			return
		}

		// "pending" is an INTERMEDIATE state: the record has been opened but the
		// outcome has not been written yet. Taking it for final would leave the
		// test open to a race.
		if !assert.NotEqual(c, "pending", records[0].Status,
			"the record's outcome must be written; a row left at 'pending' reports "+
				"that the outcome of the send could not be written") {
			return
		}

		found = records[0]
	}, notificationWaitTimeout, notificationInterval,
		"the order notification must be written into the delivery log (reference %s)", reference)

	return found
}

// notificationOrder is the order fixture the notification scenarios share: it opens
// a single-line cart for a registered customer and turns it into an order.
//
// Every scenario sets up its OWN order. That is mandatory: events go over the
// in-memory bus and the tests run in sequence, so a single shared order would be
// notified in the first test and the second test would exercise its "no second
// notification was produced" claim on the leftovers of the previous test rather
// than on state it set up itself.
func notificationOrder(
	ctx context.Context,
	t *testing.T,
	title string,
) (orderID, email string, total int64) {
	t.Helper()

	customerID, address := newCustomer(ctx, t)
	variantID, _ := newStockedVariant(ctx, t, title, map[string]int64{
		taxedCurrency: notificationUnitPrice,
	}, notificationStock)

	cartID, totals := prepareCart(ctx, t, customerID, variantID, notificationQuantity)

	result, err := orderWorkflows.CompleteCart(ctx, checkoutwf.CompleteCartInput{
		CartID:            cartID,
		LocationID:        stockLocationID,
		PaymentProviderID: manual.ID,
		PaymentData:       paymentBehavior(t, manual.OutcomeAuthorize),
		Email:             address,
		ExpectedTotal:     totals.Total,
	})
	require.NoError(t, err, "the cart must be convertible into an order")

	return result.OrderID, address, totals.Total
}

// TestOrderConfirmationIsWrittenToDeliveryLog verifies that a completed order drops
// a SINGLE record into the delivery log and that the record references that order.
//
// The record's UNIQUENESS is exercised separately: a second record means the
// customer gets two confirmations for the same order, and every link in the chain
// (publication, subscription, idempotency) can break that on its own.
func TestOrderConfirmationIsWrittenToDeliveryLog(t *testing.T) {
	ctx := t.Context()
	token := jetonAl(t, adminEmail, adminPassword)

	orderID, email, _ := notificationOrder(ctx, t, "E2E Notification Product")

	record := awaitNotification(t, token, orderID)

	records := readNotifications(t, token, orderID)
	require.Len(t, records, 1,
		"a SINGLE delivery record must be opened for the order; a second one means a second confirmation for the customer")

	assert.Equal(t, notificationsvc.TemplateOrderPlaced, record.Template,
		"the template must carry the same name as the event that triggered it")
	assert.Equal(t, orderID, record.Reference,
		"the record is tied to the order BY REFERENCE; there is no foreign key (Principle 2.2)")
	assert.Equal(t, notificationSpyID, record.ProviderID,
		"the send must be taken on by the provider selected in the harness")
	assert.Equal(t, "sent", record.Status,
		"the provider accepted the request; 'sent' reports ACCEPTANCE, not delivery")
	assert.Empty(t, record.Error)

	// Channel and status are WIRE values; writing them as strings is deliberate.
	// Comparing against a constant ties the JSON's public contract to the module's
	// internal naming, and when the constant was renamed the test would stay green
	// and let through a change that breaks clients.
	assert.Equal(t, "email", record.Channel)

	// The address is stored NOWHERE: the response body carries no personal data
	// (plan Section 8).
	raw, err := json.Marshal(record)
	require.NoError(t, err)
	assert.NotContains(t, string(raw), email,
		"the delivery record must NOT CARRY the recipient address; the address lives "+
			"only on the order")
}

// TestNotificationRecipientIsReadFromOrderNotEvent verifies that the recipient of
// the notification that goes to the provider is the order's email.
//
// # Why the real claim is about the event payload
//
// "The recipient is correct" is weak on its own: the subscriber could just as well
// have read the address from the event payload, and the test could not tell the two
// apart. This is why the same test also inspects the event ITSELF — there is no
// "email" field in the payload and no value carries the address. Together the two
// give a single conclusion: the subscriber can only have read the address FROM THE
// ORDER RECORD, over "order.interop".
//
// The inspection is at the same time the guardian of an order module decision: the
// email is deliberately NOT PUT into the event payload, because in production
// events are written to Redis and are durable there. If somebody adds the field
// "for convenience", it breaks here.
func TestNotificationRecipientIsReadFromOrderNotEvent(t *testing.T) {
	ctx := t.Context()
	token := jetonAl(t, adminEmail, adminPassword)

	orderID, email, total := notificationOrder(ctx, t, "E2E Notification Recipient Product")

	// Waiting for the record to reach its final state also makes the spy's ledger
	// race-free: the outcome is only written after the provider has returned.
	require.Equal(t, "sent", awaitNotification(t, token, orderID).Status)

	sent := notificationSpy.notificationsFor(orderID)
	require.Len(t, sent, 1,
		"exactly ONE notification must go to the provider per order")
	notification := sent[0]

	assert.Equal(t, email, notification.To,
		"the notification's recipient must be the order's email; any other address "+
			"means the subscriber read the contact details off the wrong order")
	assert.Equal(t, coreprovider.ChannelEmail, notification.Channel)
	assert.Equal(t, notificationsvc.TemplateOrderPlaced, notification.Template)

	// The template data comes FROM THE RECORD too; the amount and the line count
	// must match the order itself. Not matching would mean that the "order.interop"
	// response describes the WRONG order even though it decodes as the schema.
	assert.Equal(t, orderID, notification.Data[notificationDataKeyOrderID])
	assert.Equal(t, strconv.FormatInt(total, 10), notification.Data["total"],
		"the amount in the template must be the order's total and must carry a STRING without decimals")
	assert.Equal(t, "1", notification.Data["item_count"],
		"the fixture order has a single line")

	// And the event does NOT CARRY the address — so the address above could not
	// have come from there.
	event := eventLog.waitFor(t, orderID)
	assert.Equal(t, orderID, olayAlani(t, event, ordersvc.EventFieldOrderID),
		"the tie the event gives the subscriber is the order IDENTIFIER")

	_, hasEmailField := event.Data["email"]
	assert.False(t, hasEmailField,
		"there must be NO 'email' field in the event payload; in production events are "+
			"written to Redis and are durable there (plan Section 8: no sensitive data "+
			"is carried)")

	rawEvent, err := json.Marshal(event.Data)
	require.NoError(t, err)
	assert.NotContains(t, string(rawEvent), email,
		"NO field of the event payload may carry the address; if one did, this test's "+
			"recipient claim would not prove that the address was read from the record")
}

// TestSecondEventForSameOrderProducesNoSecondNotification verifies that a manually
// republished event does not send the customer a second email.
//
// The scenario is not made up: even though the bus does not redeliver today, an
// operator can republish missed events and the Redis backend delivers AT LEAST
// ONCE. The protection is the (template, reference) uniqueness in the delivery log
// and it is exercised here on the REAL index.
//
// Two separate claims are set up together: no second RECORD is opened in the log
// and no second SEND goes to the spy. The second one is the one that matters — what
// the customer sees is not the number of records but the number of emails that
// arrive.
func TestSecondEventForSameOrderProducesNoSecondNotification(t *testing.T) {
	ctx := t.Context()
	token := jetonAl(t, adminEmail, adminPassword)

	orderID, _, _ := notificationOrder(ctx, t, "E2E Duplicate Notification Product")

	first := awaitNotification(t, token, orderID)

	// The event is republished BY HAND; its payload has the same shape as the
	// order's own event.
	bus, err := container.Resolve[eventbus.EventBus](ctr, svcEventBus)
	require.NoError(t, err, "the event bus must be resolvable")

	require.NoError(t, bus.Publish(ctx, eventbus.Event{
		Name: notificationsvc.EventOrderPlaced,
		Data: map[string]any{ordersvc.EventFieldOrderID: orderID},
	}))

	// The second event is given time to be processed. What the wait proves is that
	// "nothing happened"; looking too early would turn the test falsely green.
	require.Never(t, func() bool {
		records, err := fetchNotifications(t, token, orderID)
		if err != nil {
			// A read error does NOT MEAN "a second record was opened"; if the
			// condition returns false, Never stays green and the fault fails the
			// test in the read below.
			return false
		}
		return len(records) > 1 || len(notificationSpy.notificationsFor(orderID)) > 1
	}, time.Second, notificationInterval,
		"there must be no SECOND delivery record and no second send for the same order")

	records := readNotifications(t, token, orderID)
	require.Len(t, records, 1)
	assert.Equal(t, first.ID, records[0].ID, "the first record must be preserved")
	assert.Len(t, notificationSpy.notificationsFor(orderID), 1,
		"exactly ONE send must go to the provider; a second one means a second order "+
			"confirmation in the customer's inbox")
}

// TestDeliveryLogCannotBeReadUnauthenticated verifies that the delivery log is
// protected.
//
// BOTH of the two requests are necessary. A 401 on its own does not say that the
// endpoint EXISTS: the guard runs before route matching, so a path that was never
// defined returns 401 as well (see identity_test.go). The same address returning 200
// with a valid token proves that what was refused really was this endpoint.
//
// The log carries no personal data but it does show which order was notified and
// when; that is, it is the timeline of the order flow and must not be readable
// without an identity.
func TestDeliveryLogCannotBeReadUnauthenticated(t *testing.T) {
	anonymous := adminRequest(t, http.MethodGet, deliveryLogPath, "")
	require.Equal(t, http.StatusUnauthorized, anonymous.Code,
		"an unauthenticated request must return 401; body: %s", anonymous.Body.String())
	assert.Equal(t, "Bearer", anonymous.Header().Get("WWW-Authenticate"),
		"RFC 9110: a 401 must report which scheme is expected")

	token := jetonAl(t, adminEmail, adminPassword)
	authenticated := adminRequest(t, http.MethodGet, deliveryLogPath, "Bearer "+token)
	require.Equal(t, http.StatusOK, authenticated.Code,
		"the same address must work with a valid token; if it does not, the 401 would be "+
			"coming from the endpoint's absence rather than its presence; body: %s", authenticated.Body.String())
}

// TestNotificationProviderRegistryKeepsDefault verifies that the out-of-the-box
// provider stays registered even when the harness has selected another one.
//
// The test pays the price of the harness swapping the provider for the spy: the
// module's default must not disappear just because the selection changed. The
// registry is a LIST, not a selection — when an installation switches
// NOTIFICATION_PROVIDER to "log", the provider has to be sitting there.
func TestNotificationProviderRegistryKeepsDefault(t *testing.T) {
	registry, err := container.Resolve[*notificationsvc.ProviderRegistry](ctr, notificationmod.ProvidersName)
	require.NoError(t, err,
		"the provider registry must be found in the ctr under the name %q; plugins resolve it by that name",
		notificationmod.ProvidersName)

	assert.Contains(t, registry.IDs(), logonly.ID,
		"the out-of-the-box %q provider must stay in the registry", logonly.ID)
	assert.Contains(t, registry.IDs(), notificationSpyID,
		"the provider added to the harness must be in the registry too; otherwise no notification could be sent at all")
}
