package cart_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/cart"
	"github.com/bdrtr/gobit/internal/modules/cart/service"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// These tests need NO database. A declaration is a property of the code and not
// of the data — the same sentence on an empty database and a full one — which
// is why [personaldata.Declarer] takes no context and returns no error, and why the
// audit of it belongs in a plain unit test that an auditor can run anywhere.

// The declaration is checked against EVERY up migration the module ships,
// because those files, not this package, decide what columns exist (D137).
//
// It read 000001 alone, as the customer module's audit did, and 000002 created
// a whole table, cart_promotion_code, that no audit looked at.

// notPersonalColumns are the columns that hold nothing about the person, table
// by table.
//
// EVERY table the migration creates has an entry, including the two whose whole
// row is exempt. That is not bookkeeping for its own sake: a table missing from
// this map would be a table nothing checks, and half the schema could grow a
// personal column without a single test noticing.
//
// The list is written down rather than inferred, and it is the load-bearing half
// of [TestPersonalDataCoversEveryPersonalColumn]: everything NOT in it has to
// appear in the declaration, so a column added tomorrow fails until somebody
// decides which side of the line it is on. Deciding is the whole point — a rule
// that guessed would let a "date_of_birth" column past on the grounds that it
// looked technical.
//
// The reasons, per table:
//
//   - cart_promotion_code is exempt as a whole row (D137). A code names a
//     promotion the shop wrote, not the shopper — the promotion module, which
//     owns the codes, declares none of them — and the cart id beside it resolves
//     to the cart the erasure already handles.
//   - The identifiers are here on purpose. A synthetic key names a row, not a
//     person, and after the columns beside it are anonymized it resolves to
//     nobody; that is exactly the fact the anonymization rests on. region_id,
//     variant_id and shipping_option_id name records in OTHER modules — a
//     region, a product, a delivery option — and each of those modules answers
//     for its own rows. carts.customer_id is the one identifier that IS
//     declared, because it is the installation's stable handle for the PERSON
//     rather than for a thing. carts.adds_to_order_id names an ORDER, whose
//     module declares and erases its own contact (ADR 0192).
//   - The money, the quantities and the currency describe the basket. What
//     somebody put in it and what it came to is a fact about the sale, and a
//     line's title is a copy of the catalog's own words. Whether the prices
//     included their tax is the market's, not the shopper's (ADR 0246).
//   - cart_shipping_methods.name is the label of the delivery service the shop
//     offers ("standard", "next day"): written about the service, not about the
//     shopper. Its free-form data column is NOT exempt and is declared Open.
//   - revision, totals_revision and every stamp describe the record's state:
//     they are true of the row whether or not anybody is behind it. That
//     includes completed_at, which says the cart became a sale and not who
//     bought.
//   - cart_addresses.address_type says whether the address was for delivery or
//     for billing; it is true of the row with every name in it emptied.
var notPersonalColumns = map[string][]string{
	"carts": {
		"id", "region_id", "currency_code", "adds_to_order_id",
		"subtotal", "discount_total", "tax_total", "shipping_total", "total",
		"prices_include_tax", "revision", "totals_revision",
		"completed_at", "created_at", "updated_at", "deleted_at",
	},
	"cart_line_items": {
		"id", "cart_id", "variant_id", "title", "quantity", "unit_price",
		"subtotal", "discount_total", "tax_total", "total",
		// The add-on bond (ADR 0229): a line id and a digest of variant ids
		// and the add-ons' words, which the declared properties already are.
		"parent_line_id", "add_on_key",
		// The order the database took the row in (ADR 0233).
		"seq",
		"created_at", "updated_at", "deleted_at",
	},
	"cart_addresses": {
		"id", "cart_id", "address_type",
		"created_at", "updated_at", "deleted_at",
	},
	"cart_shipping_methods": {
		"id", "cart_id", "name", "shipping_option_id", "amount",
		"created_at", "updated_at", "deleted_at",
	},
	"cart_promotion_code": {
		"cart_id", "code", "created_at",
	},
}

// TestPersonalDataCoversEveryPersonalColumn proves the declaration is
// EXHAUSTIVE against the schema it describes.
//
// A declaration is the only answer an embedder has to "where is this person in
// your database", so a missing row does not make the list shorter — it makes it
// FALSE, and the column it forgot is one no audit will ever look at. Comparing
// the list against the migration is what keeps the two from drifting: a column
// added to a table without a line here fails this test, which is the only moment
// anybody is thinking about that column at all.
//
// Both directions are checked. A column in the migration and not in the
// declaration is a hole; a holding naming a column the migration does not create
// sends an auditor searching for data that does not exist.
func TestPersonalDataCoversEveryPersonalColumn(t *testing.T) {
	module := cart.New(cart.Options{})
	for _, holding := range module.PersonalData().Holdings {
		key := holding.Table + "." + holding.Column
		assert.Contains(t, []personaldata.Kind{personaldata.Named, personaldata.Open}, holding.Kind,
			"%s: a holding with no kind says nothing about who wrote it", key)
		assert.NotEmpty(t, holding.Why,
			"%s: a column named without a reason cannot be repeated to a data subject", key)
	}

	// The schema is read by internal/schemaaudit, which replays every
	// migration — the ALTER TABLE this audit once read with a copy of its own
	// included (D187, D189).
	schemaaudit.Cover(t, module.Migrations(), module.PersonalData(), notPersonalColumns)
}

// TestTheDeclarationLeavesTheHolderToTheSweep pins a field that is empty ON
// PURPOSE.
//
// The coordinator overwrites [personaldata.Declaration.Holder] with the name the
// registry knows this module by (internal/workflows/datasubject), so a name written
// in here would be a second place to keep true with nothing comparing the two —
// and the report a controller reads would be attributed by whichever of them
// drifted. The name this module answers under is still pinned, one test below,
// where it is actually used.
func TestTheDeclarationLeavesTheHolderToTheSweep(t *testing.T) {
	assert.Empty(t, cart.New(cart.Options{}).PersonalData().Holder,
		"the registry's name is the authority on who answered; the sweep fills this in")
}

// TestTheHolderIsTheModuleName pins the one thing the service cannot check for
// itself.
//
// [service.ErasureHolder] cannot be read from the module package — that import
// would be a cycle — so it repeats the module name as a constant, and a repeated
// constant is exactly the kind that drifts. It is what a caller holding the
// service directly reads off the result, and what the module writes into its log
// line when it answers a request.
func TestTheHolderIsTheModuleName(t *testing.T) {
	assert.Equal(t, cart.ModuleName, service.ErasureHolder)
}
