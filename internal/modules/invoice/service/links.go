package service

import "github.com/bdrtr/gobit/core/link"

// EntityName is the name of this module's entity in a link definition.
const EntityName = "invoice"

// LinkOrderInvoice binds an order to the document issued for it.
//
// # Why THIS module declares a link whose left side is an order
//
// A link definition may be declared only ONCE (ADR 0005), so somebody has to
// own it. The rule the repository follows is that the definition belongs to the
// side that writes the record the binding carries; the document is this
// module's record, so the definition is this module's — exactly as
// order_payment belongs to the payment module.
//
// The invoicing workflow is what WRITES the binding, and it repeats the name as
// a literal rather than importing it: a flow that reached into a module for a
// string would be tied to it at compile time for no gain.
//
// # Why a link and not a column on the invoice
//
// An order_id column here would be a foreign key into another module's table in
// everything but the constraint, and Principle 2.2 refuses that. It would also
// be the wrong shape the day a document covers more than one order — a monthly
// invoice to a corporate buyer — while a link's cardinality can be widened
// without touching the table.
const LinkOrderInvoice = "order_invoice"

// Definitions are the link definitions this module declares.
//
// They are applied at startup, idempotently, by the module's Register (ADR
// 0005): the schema lives next to the definition rather than in a migration, so
// the two cannot drift.
func Definitions() []link.LinkDefinition {
	return []link.LinkDefinition{
		{
			Name: LinkOrderInvoice,
			From: link.LinkSide{Module: "order", Entity: "order", Field: "order_id"},
			// The module name is a literal rather than invoice.ModuleName: that
			// constant lives in the module package, which imports this one, so
			// reaching for it would invert the dependency.
			To: link.LinkSide{
				Module: "invoice",
				Entity: EntityName,
				Field:  "invoice_id",
			},
			// ONE TO ONE, which is the strictest constraint that is true today:
			// the flow issues one document per order and returns the existing
			// one on a second call.
			//
			// It is chosen over OneToMany on the repository's own principle — an
			// undeclared cardinality defaults to the strictest, because a
			// constraint that turns out to be too tight fails LOUDLY while one
			// that is too loose lets a wrong row in silently.
			//
			// It stays one to one when a sale is amended: a refund or a
			// charge after the sale is a document naming the sale document
			// (ADR 0406), found by the act it documents, and the order binds
			// its sale document alone.
			//
			// This comment used to end "that day this becomes OneToMany and
			// nothing else changes", the same sentence the payment module's
			// link definition carried, and it was wrong in the same way (gap
			// D51). ADR 0116 made the widening itself real, which makes the
			// claim MORE dangerous rather than less: the edit now succeeds.
			//
			// What it would cost is not the mechanism but the reader.
			// internal/workflows/invoicing/issue.go takes the first linked id
			// and says why — "the definition is one to one, so more than one is
			// a data fault rather than a choice" — and a widening rots that
			// sentence without changing a single behavior, so no test sees it.
			// Widening past OneToOne also drops from_uniq, which is the only
			// structural bar to two writers binding two documents to one order.
			Cardinality: link.OneToOne,
		},
	}
}
