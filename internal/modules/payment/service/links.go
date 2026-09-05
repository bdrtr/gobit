package service

import "github.com/bdrtr/gobit/core/link"

// LinkOrderPayment binds an order to the payment collection opened for it.
//
// # Why THIS module declares a link whose left side is an order
//
// A link definition may be declared only ONCE (ADR 0005), so somebody has to
// own it, and the order module states the rule in its own package doc: the
// definition belongs to "the side that writes the record the binding carries —
// payment, fulfillment". The collection is this module's record, so the
// definition is this module's.
//
// The order module deliberately declares NO links of its own: its region and
// customer live in their own columns, and holding the same relation twice was
// removed once already ("order_customer/order_region removed").
//
// # Why a link and not the collection's Reference field
//
// [models.PaymentCollection.Reference] already carries an identifier of the
// caller's own record, and the checkout saga puts the CART id there. It is free
// text this module never validates, and its own godoc says where the binding
// actually belongs: "Principle 2.2 — the link is established through Module
// Links". Overloading Reference to mean the order instead would break the cart
// meaning for every existing row and would still leave the association
// unqueryable from the order side.
//
// # Why the name was already in the code before the link existed
//
// Two godocs named "order_payment" — this module's query provider, describing
// how an order listing would see its payment status, and the order module's
// package doc, saying whose job the definition is. Nothing declared it. The
// promise had no consumer and no producer; this is the producer.
const LinkOrderPayment = "order_payment"

// Definitions are the link definitions this module declares.
//
// They are applied at startup, idempotently, by the module's Register (ADR
// 0005): the schema lives next to the definition rather than in a migration, so
// the two cannot drift.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name: LinkOrderPayment,
			From: link.LinkSide{Module: "order", Entity: "order", Field: "order_id"},
			// The module name is repeated as a literal rather than taken from
			// payment.ModuleName: that constant lives in the module package,
			// which imports this one, so reaching for it would invert the
			// dependency. The same repetition the workflows accept for the same
			// reason (ADR 0001).
			To: link.LinkSide{
				Module: "payment",
				Entity: EntityName,
				Field:  "payment_collection_id",
			},
			// ONE TO ONE, which is the strictest constraint that is true today:
			// the checkout saga opens exactly one collection per order.
			//
			// It is chosen over OneToMany on the repository's own principle —
			// an undeclared cardinality defaults to the strictest, because a
			// constraint that turns out to be too tight fails LOUDLY while one
			// that is too loose lets a wrong row in silently.
			//
			// It reopens the day an order legitimately needs a second
			// collection. The nearest candidate is already visible in the
			// schema: an exchange whose difference_due is positive is money
			// collected against an existing order. That day this becomes
			// OneToMany and nothing else changes.
			Cardinality: link.OneToOne,
		},
	}
}
