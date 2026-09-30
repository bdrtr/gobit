package payment_test

import (
	"context"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/payment"
	"github.com/bdrtr/gobit/internal/modules/payment/service"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// notPersonalColumns lists, per table, the columns that hold nothing about a
// person (ADR 0277); with the declaration it judges every column the
// migrations leave.
//
//   - IDENTIFIERS AND REFERENCES (id, the links between this module's rows,
//     reference where it names a collection, a session or the caller's cart or
//     order, the provider's id and external id, order_id on a credit). A key
//     names a row; the person is in the declared columns beside it.
//   - MONEY OF A PAYMENT (a collection's, a session's and a capture's amounts,
//     currency_code everywhere). They describe the sale. The ledger amounts
//     are declared instead: a store credit entry's amount and a loyalty
//     entry's points are what the customer holds.
//   - STATE AND STAMPS (status, kind, partial and every *_at). What the row is
//     and when it moved.
//   - A GIFT CARD, ITS LEDGER AND ITS SESSIONS' OWN COLUMNS. A card belongs to
//     whoever holds the code: its digest and tail, its reason, its source and
//     its balance name no person. A gift card session's idempotency key is
//     the caller's and is declared.
//   - A GIFT CARD SESSION'S DECLINE REASON. gobit's sentence about the card's
//     balance, which is the card's and not a person's.
var notPersonalColumns = map[string][]string{
	"payment_collections": {
		"id", "reference", "amount", "currency_code", "status", "authorized_amount",
		"captured_amount", "refunded_amount", "created_at", "updated_at",
	},
	"payment_sessions": {
		"id", "payment_collection_id", "provider_id", "external_id", "status", "amount",
		"authorized_amount", "currency_code", "created_at", "updated_at",
	},
	"payments": {
		"id", "payment_session_id", "payment_collection_id", "amount", "currency_code",
		"refunded_amount", "captured_at", "created_at", "updated_at",
	},
	"refunds": {
		"id", "payment_id", "amount", "reference", "created_at", "updated_at",
	},
	"payment_manual_sessions": {
		"id", "reference", "amount", "currency_code", "status", "authorized_amount",
		"captured_amount", "refunded_amount", "created_at", "updated_at",
	},
	"payment_store_credit_entries": {
		"id", "currency_code", "kind", "created_at", "expires_at", "order_id",
	},
	"payment_store_credit_sessions": {
		"id", "reference", "amount", "currency_code", "status", "authorized_amount",
		"captured_amount", "refunded_amount", "created_at", "updated_at", "partial",
	},
	"payment_loyalty_entries": {
		"id", "currency_code", "kind", "reference", "created_at",
	},
	"payment_loyalty_sessions": {
		"id", "reference", "amount", "currency_code", "status", "authorized_amount",
		"captured_amount", "refunded_amount", "created_at", "updated_at", "partial",
	},
	"payment_gift_cards": {
		"id", "code_digest", "code_tail", "currency_code", "reason", "created_at", "source",
		"source_reference", "code_changed_at", "disabled_at", "disable_reason", "expires_at",
	},
	"payment_gift_card_entries": {
		"id", "gift_card_id", "amount", "kind", "reference", "created_at",
	},
	"payment_gift_card_sessions": {
		"id", "reference", "gift_card_id", "amount", "currency_code", "status",
		"authorized_amount", "captured_amount", "refunded_amount", "decline_reason",
		"created_at", "updated_at",
	},
}

// TestTheDeclarationCoversEveryColumnOfTheSchema holds the declaration to the
// columns the migrations leave, each judged once (ADR 0277).
func TestTheDeclarationCoversEveryColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	module := payment.New()
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}

// TestTheHolderIsTheModuleName holds the service's holder name to the
// module's, which the service cannot import.
func TestTheHolderIsTheModuleName(t *testing.T) {
	t.Parallel()

	assert.Equal(t, payment.ModuleName, service.Holder)
	assert.Equal(t, payment.ModuleName, payment.New().PersonalData().Holder)
}

// TestEveryHoldingSaysWhyAndIsKept holds each holding to a reason a controller
// can repeat and to the answer the module gives an erasure: everything is
// kept.
func TestEveryHoldingSaysWhyAndIsKept(t *testing.T) {
	t.Parallel()

	for _, holding := range payment.New().PersonalData().Holdings {
		key := holding.Table + "." + holding.Column
		assert.NotEmpty(t, holding.Why, "%s says nothing about what it holds", key)
		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind, key)
		assert.Equal(t, personaldata.Kept, holding.OnErasure, "%s: the payment module rewrites nothing", key)
	}
}

// TestTheNamedColumnsAreTheCustomerAndWhatTheyHold pins the judgement ADR 0277
// made on each Named column: the customer id wherever a row names one, what a
// ledger entry gives or takes, and the decline sentence that states a balance.
// Everything else declared is free text gobit never reads.
func TestTheNamedColumnsAreTheCustomerAndWhatTheyHold(t *testing.T) {
	t.Parallel()

	var named []string
	for _, holding := range payment.New().PersonalData().Holdings {
		if holding.Kind == personaldata.Named {
			named = append(named, holding.Table+"."+holding.Column)
		}
	}

	assert.ElementsMatch(t, []string{
		"payment_collections.customer_id",
		"payment_store_credit_entries.customer_id", "payment_store_credit_entries.amount",
		"payment_store_credit_sessions.customer_id", "payment_store_credit_sessions.decline_reason",
		"payment_loyalty_entries.customer_id", "payment_loyalty_entries.points",
		"payment_loyalty_sessions.customer_id", "payment_loyalty_sessions.decline_reason",
	}, named)
}

// TestAnUnregisteredModuleRefusesRatherThanAnswerNothing holds the two answers
// of a module Register never wired to an error: "nothing here" from a module
// that never looked would be a false sentence handed to a person.
func TestAnUnregisteredModuleRefusesRatherThanAnswerNothing(t *testing.T) {
	t.Parallel()

	module := payment.New()
	subject := personaldata.Subject{CustomerID: "cus_1"}

	_, err := module.PersonalDataOf(context.Background(), subject)
	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))

	_, err = module.Erase(context.Background(), subject)
	require.Error(t, err)
	assert.Equal(t, errors.KindInternal, errors.KindOf(err))
}
