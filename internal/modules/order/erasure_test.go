package order_test

import (
	"testing"

	"github.com/bdrtr/gobit/internal/modules/order"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// The order module's declaration held to its schema (D188).
//
// The module declared what its first tables hold and the tables added after
// them went undeclared: a replacement's note, a credit's reason and note, a
// claim photograph's caption and a line cancellation's reason and note were
// free text in no declaration, so no disclosure showed them and no report
// named them. The integration tests derive from the declaration and so could
// not notice a column it never named; this audit starts from the migrations,
// as the auth, b2b, cart, customer, inventory and review audits do, and needs
// no database.

// notPersonalColumns lists, per table, the columns that hold nothing about a
// person. Every column the migrations leave in the schema is either here or in
// the declaration, never both, and every name here is a column the schema has.
//
// Why each group holds nobody:
//
//   - IDENTIFIERS AND ORDERING (id, the *_id links, display_id, seq, position,
//     rank). A key names a row, and the person is in the declared columns
//     beside it; orders.customer_id and order_addresses.source_address_id are
//     declared because they are the installation's handle for the PERSON.
//     order_claim_evidence.upload_id names the file module's record, which
//     answers for itself. orders.placed_by is a staff member's id, which
//     resolves through the auth module as the cart's opened_by does and is
//     judged there (ADR 0298).
//   - MONEY, QUANTITY AND TAX (every amount, total, quantity, rate, compound,
//     is_giftcard, prices_include_tax, difference_due). They describe the sale.
//   - STATE AND STAMPS (status, claim_type, address_type, price_list_type,
//     recalls and every *_at). What the record is and when it moved.
//   - WHAT WAS SOLD AND HOW IT WAS SENT (order_line_items.title,
//     product_title and components, the name of a shipping method or a
//     delivery change). A catalog copy and a shipping option's name, the same
//     for every buyer.
var notPersonalColumns = map[string][]string{
	"orders": {
		"id", "display_id", "status", "region_id", "currency_code", "cart_id",
		"subtotal", "discount_total", "tax_total", "shipping_total", "total",
		"placed_at", "completed_at", "canceled_at", "created_at", "updated_at",
		"archived_at", "personal_data_erased_at", "adds_to_order_id", "prices_include_tax",
		"placed_by",
	},
	"order_line_items": {
		"id", "order_id", "variant_id", "title", "quantity", "unit_price", "subtotal",
		"discount_total", "tax_total", "total", "created_at", "updated_at", "tax_rate_bps",
		"price_id", "price_list_id", "price_list_type", "is_giftcard", "parent_line_item_id",
		"seq", "components", "product_title", "unit_cost",
	},
	"order_addresses": {
		"id", "order_id", "address_type", "created_at", "updated_at", "superseded_at",
	},
	"order_summaries": {
		"id", "order_id", "paid_total", "refunded_total", "created_at", "updated_at",
	},
	"order_returns": {
		"id", "order_id", "status", "refund_amount", "received_at", "canceled_at",
		"created_at", "updated_at", "received_location_id",
	},
	"order_return_items": {
		"id", "order_return_id", "order_line_item_id", "quantity", "refund_amount",
		"created_at", "updated_at", "seq",
	},
	"order_exchanges": {
		"id", "order_id", "status", "difference_due", "canceled_at", "created_at",
		"updated_at", "completed_at", "payment_collection_id", "funded_at",
	},
	"order_claims": {
		"id", "order_id", "claim_type", "status", "refund_amount", "completed_at",
		"canceled_at", "created_at", "updated_at",
	},
	"order_replacements": {
		"id", "order_claim_id", "status", "shipping_option_id", "location_id", "canceled_at",
		"created_at", "updated_at", "dispatched_at", "fulfillment_id", "order_exchange_id", "recalls",
	},
	"order_replacement_items": {
		"id", "order_replacement_id", "order_line_item_id", "quantity", "created_at",
		"updated_at", "reservation_id", "variant_id", "seq",
	},
	"order_replacement_item_parts": {
		"order_replacement_item_id", "variant_id", "quantity", "rank", "reservation_id",
		"created_at", "updated_at",
	},
	"order_credit_lines": {
		"id", "order_id", "amount", "created_at", "updated_at",
	},
	"order_claim_evidence": {
		"id", "order_claim_id", "upload_id", "created_at", "updated_at",
	},
	"order_line_cancellations": {
		"id", "order_line_item_id", "quantity", "created_at", "updated_at", "seq",
	},
	"order_line_taxes": {
		"id", "order_line_item_id", "position", "rate_id", "rate_bps", "compound",
		"taxable_amount", "tax_amount", "created_at", "updated_at",
	},
	"order_shipping_methods": {
		"id", "order_id", "shipping_option_id", "name", "amount", "created_at", "seq",
	},
	"order_delivery_changes": {
		"id", "order_id", "shipping_method_id", "shipping_option_id", "name", "amount",
		"difference", "credit_line_id", "created_at", "payment_collection_id",
	},
}

// TestPersonalDataCoversEveryColumnOfTheSchema holds the declaration to the
// schema in both directions: every column is declared or judged to hold
// nobody, every declared column exists, every judged column exists, and every
// table is judged.
func TestPersonalDataCoversEveryColumnOfTheSchema(t *testing.T) {
	t.Parallel()

	module := order.New()
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}
