package order_test

import (
	"io/fs"
	"regexp"
	"slices"
	"sort"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/internal/modules/order"
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
//     answers for itself.
//   - MONEY, QUANTITY AND TAX (every amount, total, quantity, rate, compound,
//     is_giftcard, prices_include_tax, difference_due). They describe the sale.
//   - STATE AND STAMPS (status, claim_type, address_type, price_list_type,
//     recalls and every *_at). What the record is and when it moved.
//   - WHAT WAS SOLD AND HOW IT WAS SENT (order_line_items.title and
//     components, the name of a shipping method or a delivery change). A
//     catalog copy and a shipping option's name, the same for every buyer.
var notPersonalColumns = map[string][]string{
	"orders": {
		"id", "display_id", "status", "region_id", "currency_code", "cart_id",
		"subtotal", "discount_total", "tax_total", "shipping_total", "total",
		"placed_at", "completed_at", "canceled_at", "created_at", "updated_at",
		"archived_at", "personal_data_erased_at", "adds_to_order_id", "prices_include_tax",
	},
	"order_line_items": {
		"id", "order_id", "variant_id", "title", "quantity", "unit_price", "subtotal",
		"discount_total", "tax_total", "total", "created_at", "updated_at", "tax_rate_bps",
		"price_id", "price_list_id", "price_list_type", "is_giftcard", "parent_line_item_id",
		"seq", "components",
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

	declared := map[string]bool{}
	for _, holding := range order.New().PersonalData().Holdings {
		key := holding.Table + "." + holding.Column
		assert.False(t, declared[key], "%s is declared twice", key)
		declared[key] = true
	}

	schema := orderSchema(t)
	require.Contains(t, schema, "orders", "no table was read out of the migrations; the reader has gone blind")

	for table, columns := range schema {
		exempt, judged := notPersonalColumns[table]
		assert.True(t, judged,
			"%s is created by the migrations and judged nowhere: list its columns in "+
				"notPersonalColumns with the reason each holds nobody, and declare the rest", table)

		for _, column := range exempt {
			assert.True(t, columns[column],
				"%s.%s is judged to hold nobody and the migrations leave no such column", table, column)
		}
		for column := range columns {
			key := table + "." + column
			if slices.Contains(exempt, column) {
				assert.False(t, declared[key],
					"%s is declared as personal data and also judged to hold nobody", key)
				continue
			}
			assert.True(t, declared[key],
				"%s is in the migrations and NOT in the declaration: declare it in "+
					"service.personalColumns, or judge it in notPersonalColumns with the reason", key)
			delete(declared, key)
		}
	}
	for table := range notPersonalColumns {
		assert.Contains(t, schema, table, "%s is judged here and no migration leaves it", table)
	}

	remaining := make([]string, 0, len(declared))
	for key := range declared {
		remaining = append(remaining, key)
	}
	sort.Strings(remaining)
	assert.Empty(t, remaining, "these holdings name columns the migrations do not leave")
}

// TestTheSchemaReaderReplaysTheMigrationsInOrder holds the reader to the four
// statements the audit depends on, on a schema written for it: a column
// created, one added, one dropped after, and several added by one statement.
func TestTheSchemaReaderReplaysTheMigrationsInOrder(t *testing.T) {
	t.Parallel()

	schema := replaySchema("CREATE TABLE IF NOT EXISTS planted (\n" +
		"    id TEXT PRIMARY KEY,\n" +
		"    gone TEXT,\n" +
		"    price NUMERIC(10, 2),\n" +
		"    CONSTRAINT planted_check CHECK (id <> ';')\n" +
		");\n" +
		"ALTER TABLE planted DROP COLUMN IF EXISTS gone;\n" +
		"ALTER TABLE planted ADD COLUMN IF NOT EXISTS note TEXT;\n" +
		"ALTER TABLE planted\n    ADD COLUMN one TEXT,\n    ADD COLUMN two TEXT,\n" +
		"    ADD CONSTRAINT planted_two CHECK (two <> '');\n" +
		"CREATE TABLE IF NOT EXISTS dropped (id TEXT);\nDROP TABLE IF EXISTS dropped;\n")

	assert.Equal(t, map[string]map[string]bool{
		"planted": {"id": true, "price": true, "note": true, "one": true, "two": true},
	}, schema)
}

// orderSchema replays every up-migration the module ships, in name order.
func orderSchema(t *testing.T) map[string]map[string]bool {
	t.Helper()

	migrations := order.New().Migrations()
	names, err := fs.Glob(migrations, "*.up.sql")
	require.NoError(t, err)
	require.NotEmpty(t, names, "the module ships no up-migration; the reader has gone blind")
	sort.Strings(names)

	var sql strings.Builder
	for _, name := range names {
		raw, err := fs.ReadFile(migrations, name)
		require.NoError(t, err)
		sql.Write(raw)
		sql.WriteString("\n")
	}

	return replaySchema(sql.String())
}

// The patterns the reader uses.
var (
	schemaLineComment = regexp.MustCompile(`--[^\n]*`)
	schemaStringLit   = regexp.MustCompile(`'[^']*'`)
	schemaCreateTable = regexp.MustCompile(
		`(?is)^\s*create\s+table\s+(?:if\s+not\s+exists\s+)?([a-z0-9_]+)\s*\((.*)\)\s*$`)
	schemaAlterTable = regexp.MustCompile(`(?is)^\s*alter\s+table\s+(?:if\s+exists\s+)?([a-z0-9_]+)\s`)
	schemaAddColumn  = regexp.MustCompile(`(?is)\badd\s+column\s+(?:if\s+not\s+exists\s+)?([a-z0-9_]+)`)
	schemaDropColumn = regexp.MustCompile(`(?is)\bdrop\s+column\s+(?:if\s+exists\s+)?([a-z0-9_]+)`)
	schemaDropTable  = regexp.MustCompile(`(?is)^\s*drop\s+table\s+(?:if\s+exists\s+)?([a-z0-9_]+)`)
)

// schemaConstraintWords open a table constraint rather than a column.
var schemaConstraintWords = map[string]bool{
	"constraint": true, "primary": true, "unique": true, "foreign": true, "check": true, "exclude": true,
}

// replaySchema replays CREATE TABLE, ALTER TABLE ADD and DROP COLUMN and DROP
// TABLE in order, and returns the tables and columns they leave.
func replaySchema(sql string) map[string]map[string]bool {
	sql = schemaLineComment.ReplaceAllString(sql, " ")
	sql = schemaStringLit.ReplaceAllString(sql, "''")

	schema := map[string]map[string]bool{}
	for _, statement := range splitTopLevel(sql, ';') {
		switch {
		case schemaCreateTable.MatchString(statement):
			match := schemaCreateTable.FindStringSubmatch(statement)
			columns := map[string]bool{}
			for _, item := range splitTopLevel(match[2], ',') {
				fields := strings.Fields(item)
				if len(fields) == 0 || schemaConstraintWords[strings.ToLower(fields[0])] {
					continue
				}
				columns[fields[0]] = true
			}
			schema[match[1]] = columns
		case schemaAlterTable.MatchString(statement):
			table := schemaAlterTable.FindStringSubmatch(statement)[1]
			for _, match := range schemaAddColumn.FindAllStringSubmatch(statement, -1) {
				if schema[table] == nil {
					schema[table] = map[string]bool{}
				}
				schema[table][match[1]] = true
			}
			for _, match := range schemaDropColumn.FindAllStringSubmatch(statement, -1) {
				delete(schema[table], match[1])
			}
		case schemaDropTable.MatchString(statement):
			delete(schema, schemaDropTable.FindStringSubmatch(statement)[1])
		}
	}

	return schema
}

// splitTopLevel cuts text on sep where it is not inside parentheses.
func splitTopLevel(text string, sep rune) []string {
	var (
		out   []string
		depth int
		start int
	)
	for i, r := range text {
		switch r {
		case '(':
			depth++
		case ')':
			depth--
		case sep:
			if depth == 0 {
				out = append(out, text[start:i])
				start = i + 1
			}
		}
	}

	return append(out, text[start:])
}
