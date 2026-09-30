package customer_test

import (
	"testing"

	"github.com/stretchr/testify/assert"

	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/modules/customer"
	"github.com/bdrtr/gobit/internal/modules/customer/service"
	"github.com/bdrtr/gobit/internal/schemaaudit"
)

// These tests need NO database. A declaration is a property of the code and not
// of the data — the same sentence on an empty database and a full one — which
// is why [personaldata.Declarer] takes no context and returns no error, and why the
// audit of it belongs in a plain unit test that an auditor can run anywhere.

// The declaration is checked against EVERY up migration the module ships,
// because those files, not this package, decide what columns exist (D137).
//
// It read one file, 000001, whose name it kept as a constant described as "the
// module's only migration". 000002 added customer_group.rank and nothing looked
// at it, and a personal column added by a later migration would have passed the
// same way: measured by appending a date_of_birth column to 000002, which left
// this test green.

// notPersonalColumns are the columns that hold nothing about the person, table
// by table.
//
// EVERY table the migration creates has an entry, including the two the
// declaration barely touches. That is not bookkeeping for its own sake: a table
// missing from this map used to be a table nobody checked, so half the schema
// could grow a personal column without a single test noticing. The map is now
// the answer to "which tables were looked at", and
// [TestPersonalDataCoversEveryPersonalColumn] fails if the migration creates a
// table this map does not name.
//
// The list is written down rather than inferred, and it is the load-bearing
// half of that test: everything NOT in it has to appear in the declaration, so
// a column added tomorrow fails until somebody decides which side of the line
// it is on. Deciding is the whole point — a rule that guessed would let a
// "date_of_birth" column past on the grounds that it looked technical.
//
// The reasons, per table:
//
//   - The identifiers are here on purpose, in all five tables. A synthetic key
//     names a row, not a person, and after the row beside it is anonymized it
//     resolves to nobody; that is exactly the fact the anonymization rests on.
//     Declaring one would also drag in every foreign id in the deployment.
//     customer_group_customer is nothing BUT identifiers plus the moment the
//     membership was made, which is why the whole row is exempt: it says that
//     some row belongs to some segment, and once the customer row is anonymous
//     it no longer says that about anybody.
//   - customer_group.name is the segment's own label ("wholesalers"), written
//     by the shop about a category rather than about a customer, and its rank
//     is the shop's ordering of its segments (ADR 0049). Its metadata
//     is NOT exempt and is declared Open, because it is the same free-form
//     jsonb as customer.metadata and gobit refuses to look inside either one.
//     Its segment is exempt although it is jsonb too, because gobit does look
//     inside it: the service admits only the rule vocabulary of ADR 0217, whose
//     values are booleans, whole numbers and country codes, so it can name a
//     kind of customer and never one; segment_set_at and segment_evaluated_at
//     are its moments.
//   - The timestamps and the flags (has_account, is_default_shipping,
//     is_default_billing) describe the record's state, not the person: they are
//     true of the row whether or not anybody is behind it.
var notPersonalColumns = map[string][]string{
	"customer": {
		"id", "has_account", "created_at", "updated_at", "deleted_at",
	},
	"customer_address": {
		"id", "customer_id", "is_default_shipping", "is_default_billing",
		"created_at", "updated_at", "deleted_at",
	},
	"customer_group": {
		"id", "name", "rank", "created_at", "updated_at", "deleted_at",
		"segment", "segment_set_at", "segment_evaluated_at",
	},
	"customer_group_customer": {
		"customer_id", "customer_group_id", "created_at",
	},
	// The wishlist's variant id and its two alerts are declared and the row is
	// deleted by an erasure (ADR 0190, ADR 0215, ADR 0216); what is left is the
	// owner's id, the moment it was saved, and the alerts' working state — the
	// shop's sales channels and region they are judged in, the moments the shop
	// armed and marked them, and the shop's own price at the mark — which say
	// nothing the declared marks do not.
	"customer_wishlist_item": {
		"customer_id", "created_at", "stock_alert_channels", "stock_alert_armed_at",
		"price_alert_marked_at", "price_alert_region_id", "price_alert_channels",
		"price_alert_currency", "price_alert_amount",
	},
}

// TestPersonalDataCoversEveryPersonalColumn proves the declaration is
// EXHAUSTIVE against the schema it describes.
//
// A declaration is the only answer an embedder has to "where is this person in
// your database", so a missing row does not make the list shorter — it makes it
// FALSE, and the column it forgot is one no audit will ever look at. Comparing
// the list against the migration is what keeps the two from drifting: a column
// added to the table without a line here fails this test, which is the only
// moment anybody is thinking about that column at all.
func TestPersonalDataCoversEveryPersonalColumn(t *testing.T) {
	module := customer.New(nil)
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

// TestTheHolderIsTheModuleName pins the one thing the service cannot check for
// itself.
//
// [service.ErasureHolder] cannot be read from the module package — that import
// would be a cycle — so it repeats the module name as a constant, and a
// repeated constant is exactly the kind that drifts. The report is where the
// drift would show up: a controller reading "customer" off one holder and
// looking for a module by that name has to find this one.
func TestTheHolderIsTheModuleName(t *testing.T) {
	assert.Equal(t, customer.ModuleName, service.ErasureHolder)
}
