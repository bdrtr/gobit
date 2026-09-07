package service

import (
	"slices"

	"github.com/bdrtr/gobit/core/personaldata"
)

// The names of the tables the declaration speaks about.
//
// [TableCustomer] carries the same letters as the module's name but is NOT
// derived from it: one is the name of a table in the database, the other is the
// module's name in the container, and changing one does not change the other.
// They are exported because two different answers are built out of them — the
// declaration below and the records of [Service.PersonalDataOf] — and a table
// name spelled twice is a table name that can be spelled differently twice.
const (
	// TableCustomer is the table holding both guests and registered accounts.
	TableCustomer = "customer"
	// TableAddress is the table holding a customer's saved addresses.
	TableAddress = "customer_address"
	// TableGroup is the table holding customer segments.
	TableGroup = "customer_group"
)

// The names of the declared columns.
//
// They are constants because the same spelling has to appear in two places that
// must agree exactly: the declaration below, and the map that says what a row
// holds ([customerValues], [addressValues]). The disclosure JOINS those two on
// the column name, so a typo in either one would produce a declared column
// disclosed as empty rather than a compile error.
//
// They are NOT the provider's field* constants, which spell some of the same
// words. Those are the Query layer's vocabulary and are a different list: it
// contains names that are no column at all ("group_ids" is assembled from a join
// table), and it is a public API surface that may be renamed for reasons that
// have nothing to do with the schema. Tying the two together would let a rename
// of a query field silently rewrite gobit's statement about where a person is
// kept — the same argument that keeps [TableCustomer] from being derived from
// the module's name.
const (
	columnEmail       = "email"
	columnFirstName   = "first_name"
	columnLastName    = "last_name"
	columnPhone       = "phone"
	columnMetadata    = "metadata"
	columnCompany     = "company"
	columnAddress1    = "address_1"
	columnAddress2    = "address_2"
	columnCity        = "city"
	columnPostalCode  = "postal_code"
	columnCountryCode = "country_code"
)

// personalDataHoldings is every place this module keeps personal data.
//
// # Why the list lives in the service and not on the module
//
// The module is still the DECLARER — Module.PersonalData is what a controller
// calls, it is where the argument about what is in this list and what is
// deliberately not is written, and it answers correctly on a module whose
// Register was never called. What lives here is the list ITSELF, and it lives
// here because the disclosure has to be built out of it.
//
// [Service.PersonalDataOf] does not carry a second list of columns: it walks
// these holdings, table by table, and asks a row for the value of each declared
// column. That is the rule ADR 0034 states — a disclosure lists exactly the
// declared columns, and a holder should derive one from the other rather than
// maintain both — and deriving is the only version of it that cannot rot. Two
// hand-kept lists would agree on the day they were written and would part
// company the first time somebody added a column to one of them; the column
// would then be either declared and never handed over, or handed over after the
// declaration told the controller it was not there. Both are answers given to a
// person about their own data, and both are wrong.
//
// The service cannot import the module package (that is a cycle), so the list
// had to be on this side of the line for the derivation to exist at all. The
// module reads it back through [PersonalDataHoldings].
//
// # Why the reasons are in English in a package that is not
//
// The Why strings are DATA that crosses into core/personaldata and is read by
// the embedder — not prose about the code. One dossier assembles the sentences
// of every holder, so a per-file language rule would produce an answer to a data
// subject written in two languages (ADR 0033, "The declaration text is English
// even in a Turkish file").
var personalDataHoldings = []personaldata.Holding{
	{
		Table: TableCustomer, Column: columnEmail, Kind: personaldata.Named,
		Why: "the address the person gave; it is also how a guest checkout is recognized",
	},
	{
		Table: TableCustomer, Column: columnFirstName, Kind: personaldata.Named,
		Why: "the person's first name as they typed it",
	},
	{
		Table: TableCustomer, Column: columnLastName, Kind: personaldata.Named,
		Why: "the person's last name as they typed it",
	},
	{
		Table: TableCustomer, Column: columnPhone, Kind: personaldata.Named,
		Why: "the person's phone number, used to reach them about an order",
	},
	{
		Table: TableCustomer, Column: columnMetadata, Kind: personaldata.Open,
		Why: "free-form context the shop writes about the customer; gobit puts nothing in it and never rewrites it, so whether it holds personal data is the controller's judgement",
	},
	{
		Table: TableGroup, Column: columnMetadata, Kind: personaldata.Open,
		Why: "free-form context the shop writes about a customer segment; the group is not a person, but the blob is the shop's to fill and gobit never looks inside it, so whether it names anybody is the controller's judgement",
	},
	{
		Table: TableAddress, Column: columnFirstName, Kind: personaldata.Named,
		Why: "the first name on a saved address, which may be the customer's or a recipient's",
	},
	{
		Table: TableAddress, Column: columnLastName, Kind: personaldata.Named,
		Why: "the last name on a saved address, which may be the customer's or a recipient's",
	},
	{
		Table: TableAddress, Column: columnCompany, Kind: personaldata.Named,
		Why: "the company the address is delivered to; for a sole trader it names the person",
	},
	{
		Table: TableAddress, Column: columnAddress1, Kind: personaldata.Named,
		Why: "the street line of a saved address — where the person lives or takes deliveries",
	},
	{
		Table: TableAddress, Column: columnAddress2, Kind: personaldata.Named,
		Why: "the second address line: flat, floor or door, which narrows the street line to a household",
	},
	{
		Table: TableAddress, Column: columnCity, Kind: personaldata.Named,
		Why: "the city of a saved address",
	},
	{
		Table: TableAddress, Column: columnPostalCode, Kind: personaldata.Named,
		Why: "the postal code of a saved address; in some countries it reaches a single building",
	},
	{
		Table: TableAddress, Column: columnPhone, Kind: personaldata.Named,
		Why: "the contact phone left on a saved address for the courier",
	},
	{
		Table: TableAddress, Column: columnCountryCode, Kind: personaldata.Named,
		Why: "the country of a saved address; it is declared but deliberately NOT erased, because it names the jurisdiction whose tax and retention rules apply, a two-letter code points at tens of millions of people, and the column's CHECK constraint refuses an empty value",
	},
}

// PersonalDataHoldings returns the module's declared holdings.
//
// The slice is COPIED because the caller receives it. What comes back is handed
// to a controller as gobit's own statement of where a person is kept, and a
// caller that sorted or appended to it would be editing that statement for
// every later reader — the same reason Result.Kept is cloned on the way out of
// [Service.Erase].
//
// It takes no context and returns no error because a declaration is a property
// of the code and not of the data: it is the same sentence on an empty database
// and a full one, and an audit reads it without a connection (ADR 0029). That
// is also why it is a package-level function rather than a method — a service
// whose Register was never called still declares correctly.
func PersonalDataHoldings() []personaldata.Holding {
	return slices.Clone(personalDataHoldings)
}

// holdingsOf returns the declared holdings of one table, in declaration order.
//
// The order is the order of the disclosure's fields, and it being the
// declaration's order is worth keeping: a reader comparing a person's dossier
// against gobit's declaration reads the two lists top to bottom, and a
// disclosure that shuffled the columns would make them do it by search.
//
// A table with no declared column returns nothing, and that is not an error
// here: it is how the group table stays out of a person's records without a
// second rule saying so (see [Service.PersonalDataOf]).
func holdingsOf(table string) []personaldata.Holding {
	out := make([]personaldata.Holding, 0, len(personalDataHoldings))
	for _, holding := range personalDataHoldings {
		if holding.Table == table {
			out = append(out, holding)
		}
	}

	return out
}
