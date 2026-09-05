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
// # Why ONE TO MANY and not one to one
//
// An order can ship in several parcels — that is what the fulfillment items
// are for, and the admin API's create endpoint has always accepted a subset of
// the lines. A shipment, on the other hand, belongs to exactly one order; the
// reverse would mean one parcel settling two orders, which no flow here can
// produce and no operator could unpick.
//
// The constraint is therefore the strictest one that is TRUE, which is this
// repository's rule for cardinality: a looser declaration cannot be tightened
// later without data already violating it.
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
			Cardinality: link.OneToMany,
		},
	}
}
