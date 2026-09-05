package logonly_test

import (
	"bytes"
	"context"
	"log/slog"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/internal/modules/notification/logonly"
)

// testNotification carries the shape of a real order confirmation.
func testNotification() coreprovider.Notification {
	return coreprovider.Notification{
		Channel:  coreprovider.ChannelEmail,
		To:       "customer@example.com",
		Template: "order.placed",
		Data: map[string]string{
			"order_id":   "order_01H",
			"display_id": "1042",
			"total":      "6100",
		},
	}
}

// capturing produces a logger that writes its output into a buffer.
func capturing() (*slog.Logger, *bytes.Buffer) {
	buf := &bytes.Buffer{}
	handler := slog.NewJSONHandler(buf, &slog.HandlerOptions{Level: slog.LevelDebug})

	return slog.New(handler), buf
}

// TestSendDoesNotLOGTheRecipientAddress verifies the plan's "sensitive data is
// not logged" rule (Section 8).
//
// The log collector is open to a far wider audience than the admin surface; an
// address landing there would make data deliberately not kept in the delivery
// log permanent through the back door. That is why not only "To" but also the
// VALUES of the payload are left unwritten — template data is free by
// definition and tomorrow it may carry a customer name.
func TestSendDoesNotLOGTheRecipientAddress(t *testing.T) {
	log, buf := capturing()
	prov := logonly.New(log)

	require.NoError(t, prov.Send(context.Background(), testNotification()))

	output := buf.String()
	require.NotEmpty(t, output, "the provider must write at least one line")
	assert.NotContains(t, output, "customer@example.com", "the recipient address MUST NOT BE LOGGED")
	assert.NotContains(t, output, "@", "no part of an address may land in the log")
	assert.NotContains(t, output, "6100", "the VALUES of the payload must not be logged")

	assert.Contains(t, output, "order.placed", "the template must be logged")
	assert.Contains(t, output, coreprovider.ChannelEmail, "the channel must be logged")
	assert.Contains(t, output, "order_id", "the data KEYS are logged for diagnosis")
}

// TestSendSAYSItDidNotSend verifies that the log line states plainly that no
// send was performed.
//
// Believing "the notification went out" is believing the order confirmation
// reached the customer; a silent placeholder would produce exactly that
// illusion. The level is WARN as well: an installation that has not picked a
// real provider must not be a silent installation.
func TestSendSAYSItDidNotSend(t *testing.T) {
	log, buf := capturing()

	require.NoError(t, logonly.New(log).Send(context.Background(), testNotification()))

	output := buf.String()
	assert.Contains(t, output, "NOT SENT", "the log line must say that no send was performed")
	assert.Contains(t, output, `"level":"WARN"`, "the level must be WARN")
}

// TestIDIsANameThatSaysItDoesNotSend verifies that the IDENTITY of the provider
// tells the behavior too.
//
// The identity is the value written into the configuration
// (NOTIFICATION_PROVIDER=log); a name like "smtp" or "default" would make the
// owner of the installation think a send was performed.
func TestIDIsANameThatSaysItDoesNotSend(t *testing.T) {
	assert.Equal(t, "log", logonly.ID)
	assert.Equal(t, logonly.ID, logonly.New(nil).ID())
}

// TestSendReturnsNOError verifies that not sending is not reported as a FAULT.
//
// Had an error been returned, the delivery log would have been painted red as
// if there were a real provider outage, and a missing configuration could not
// have been told apart from a real fault.
func TestSendReturnsNOError(t *testing.T) {
	assert.NoError(t, logonly.New(nil).Send(context.Background(), testNotification()))
}
