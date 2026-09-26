package service

import "github.com/bdrtr/gobit/core/link"

// LinkOrderFulfillment binds an order to the shipments opened for it.
//
// # Why THIS module declares a link whose left side is an order
//
// A definition may be declared only ONCE (ADR 0005), so somebody has to own it,
// and the rule is that it belongs to the side that writes the record the
// binding carries. The shipment is this module's record, so the definition is
// this module's — the same reasoning that put "order_payment" in the payment
// module.
//
// The order module's own package doc has named this definition as somebody
// else's job since before it existed: it said the "order_fulfillment" bindings
// are not owned by that module either. Nothing declared it, so the name was a
// promise with neither a producer nor a consumer. This is the producer.
//
// # Why MANY TO MANY
//
// An order can ship in several parcels — that is what the fulfillment items
// are for, and the admin API's create endpoint has always accepted a subset of
// the lines. Until ADR 0197 a shipment belonged to exactly one order, and the
// declaration was ONE TO MANY because that was the strictest constraint that
// was true.
//
// An addition can now travel in its parent's parcel (ADR 0197), so a parcel is
// bound to the order it was opened for and to the additions that joined it.
// The declaration was widened rather than replaced, the one change a link
// allows (ADR 0116), and what the unique index on the parcel side used to hold
// is held by the one flow that writes a second binding: it binds only an
// addition of the parcel's own order, going to the same address. The parcel's
// ITEMS are still the lines of the order it was opened for, the one its
// reference names.
const LinkOrderFulfillment = "order_fulfillment"

// FulfillmentEntity is the name of the shipment record on the link's far side.
//
// It is NOT [EntityName]: that is the module's Query entity, the shipping
// option, and this end is the shipment. The distinction matters in one concrete
// way — the Query layer looks up an expansion's target under
// "<Entity>.query", and this module registers no provider under "fulfillment".
// So the binding is readable through the link service (ListMany), which is what
// the flow and the order's admin endpoints use, and NOT expandable through a
// Query request. Giving the shipment a Query provider is the step the order
// timeline will need; it is not needed to bind the two records.
const FulfillmentEntity = "fulfillment"

// Definitions are the link definitions this module declares.
//
// They are applied at startup, idempotently, by the module's Register (ADR
// 0005): the schema lives next to the definition rather than in a migration, so
// the two cannot drift.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name: LinkOrderFulfillment,
			From: link.LinkSide{Module: "order", Entity: "order", Field: "order_id"},
			// The module name is a literal rather than fulfillment.ModuleName:
			// that constant lives in the module package, which imports this
			// one, so reaching for it would invert the dependency. The same
			// repetition the workflows accept for the same reason (ADR 0001).
			To: link.LinkSide{
				Module: "fulfillment",
				Entity: FulfillmentEntity,
				Field:  "fulfillment_id",
			},
			Cardinality: link.ManyToMany,
		},
	}
}
