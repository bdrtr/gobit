package offline_test

import (
	"context"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	coreprovider "github.com/bdrtr/gobit/core/provider"
	"github.com/bdrtr/gobit/core/providertest"
	"github.com/bdrtr/gobit/internal/modules/payment/offline"
)

// TestTheProviderIsCompliant runs the published compliance suite, from the
// provider's own package as an embedder's provider runs it.
func TestTheProviderIsCompliant(t *testing.T) {
	t.Parallel()

	p, err := offline.New("bank_transfer")
	require.NoError(t, err)
	providertest.Identity(t, p)
}

// TestAMethodIsNamedAsConfigurationTypesIt pins the names a method can take:
// what an operator types into PAYMENT_OFFLINE_METHODS and a client sends.
func TestAMethodIsNamedAsConfigurationTypesIt(t *testing.T) {
	t.Parallel()

	for _, method := range []string{"bank_transfer", "cash_on_delivery", "money_order2", "x"} {
		p, err := offline.New(method)
		require.NoError(t, err, method)
		assert.Equal(t, method, p.ID())
	}
	for _, method := range []string{
		"", "Bank", "bank transfer", " bank", "2cheque", "_bank", "bank-transfer", "café",
		strings.Repeat("a", 41),
	} {
		_, err := offline.New(method)
		require.Error(t, err, method)
		assert.Equal(t, offline.CodeInvalidMethod, errors.CodeOf(err), method)
	}
}

// TestASessionIsTheSameForTheSameKey is the core contract's idempotency with
// nothing stored: the id is derived from the method and the key.
func TestASessionIsTheSameForTheSameKey(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p, err := offline.New("bank_transfer")
	require.NoError(t, err)

	in := coreprovider.CreateSessionInput{Amount: 12_500, CurrencyCode: "TRY", IdempotencyKey: "exec_1"}
	first, err := p.CreateSession(ctx, in)
	require.NoError(t, err)
	second, err := p.CreateSession(ctx, in)
	require.NoError(t, err)
	assert.Equal(t, first.ID, second.ID, "the same key opens the same session")
	assert.Equal(t, coreprovider.SessionPending, first.Status)
	assert.Equal(t, int64(12_500), first.Amount)
	assert.Equal(t, "TRY", first.CurrencyCode)

	other, err := p.CreateSession(ctx, coreprovider.CreateSessionInput{Amount: 12_500, IdempotencyKey: "exec_2"})
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, other.ID, "another key opens another session")

	cash, err := offline.New("cash_on_delivery")
	require.NoError(t, err)
	cashSession, err := cash.CreateSession(ctx, in)
	require.NoError(t, err)
	assert.NotEqual(t, first.ID, cashSession.ID, "another method's session for the same key is its own")

	_, err = p.CreateSession(ctx, coreprovider.CreateSessionInput{Amount: 1, IdempotencyKey: " "})
	require.Error(t, err)
	assert.Equal(t, offline.CodeInvalidInput, errors.CodeOf(err))
}

// TestAPromiseHoldsNothingAndCoversTheSession is what the provider answers for
// one of its own sessions: authorized with no amount, which the payment module
// reads as the session's own, and every later act accepted.
func TestAPromiseHoldsNothingAndCoversTheSession(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	p, err := offline.New("bank_transfer")
	require.NoError(t, err)
	session, err := p.CreateSession(ctx, coreprovider.CreateSessionInput{Amount: 900, IdempotencyKey: "k"})
	require.NoError(t, err)

	result, err := p.Authorize(ctx, session.ID)
	require.NoError(t, err)
	assert.Equal(t, coreprovider.SessionAuthorized, result.Status)
	assert.Zero(t, result.AuthorizedAmount, "the module reads no amount as the session's own")

	require.NoError(t, p.Capture(ctx, session.ID, 900))
	require.NoError(t, p.Refund(ctx, session.ID, 300))
	require.NoError(t, p.Cancel(ctx, session.ID))
	assert.True(t, p.CapturesLater())
}

// TestAMethodOwnsOnlyItsOwnSessions refuses an id the method never gave,
// including one of a method whose name begins with this one's.
func TestAMethodOwnsOnlyItsOwnSessions(t *testing.T) {
	t.Parallel()
	ctx := context.Background()
	bank, err := offline.New("bank")
	require.NoError(t, err)
	transfer, err := offline.New("bank_transfer")
	require.NoError(t, err)
	session, err := transfer.CreateSession(ctx, coreprovider.CreateSessionInput{Amount: 1, IdempotencyKey: "k"})
	require.NoError(t, err)

	ownSession, err := bank.CreateSession(ctx, coreprovider.CreateSessionInput{Amount: 1, IdempotencyKey: "k"})
	require.NoError(t, err)
	for _, id := range []string{
		session.ID,
		"bank_",
		"bank_" + strings.Repeat("z", 32),
		ownSession.ID + "0",
		"stripe_" + strings.TrimPrefix(ownSession.ID, "bank_"),
	} {
		for name, act := range map[string]func() error{
			"authorize": func() error { _, err := bank.Authorize(ctx, id); return err },
			"capture":   func() error { return bank.Capture(ctx, id, 1) },
			"refund":    func() error { return bank.Refund(ctx, id, 1) },
			"cancel":    func() error { return bank.Cancel(ctx, id) },
		} {
			err := act()
			require.Error(t, err, "%s %q", name, id)
			assert.True(t, errors.HasKind(err, errors.KindNotFound), "%s %q", name, id)
			assert.Equal(t, offline.CodeUnknownSession, errors.CodeOf(err), "%s %q", name, id)
		}
	}
	_, err = bank.Authorize(ctx, ownSession.ID)
	require.NoError(t, err, "its own session is served")
}
