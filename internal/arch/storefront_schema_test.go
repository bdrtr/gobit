package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"path/filepath"
	"slices"
	"sort"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/require"
)

// This file holds the THIRD part of ADR 0051, the part the record itself
// carried as unbuilt: the discriminator constrains the SCHEMA, not only the
// flow.
//
// # What the two gates next door do NOT reach
//
// storefront_boundary_test.go reads the request types and the OpenAPI
// parameters, so it answers "what may the storefront SAY". It never opens a
// migration, and the sentence it cannot hold is the one ADR 0051 states in one
// line: a column that cannot mean what it will be read to mean is worse stored
// than absent. That is a claim about the TABLE, and the record named the join
// it needs — "a route-to-table cross-reference that nothing in this tree
// performs".
//
// The review module is the worked example and its refusals are the decision
// rather than an omission: no order id, no e-mail address, no IP address, and
// the module's PersonalData godoc names A15 as the reason all three were
// refused. Nothing noticed that. The reviews table could have grown any of them
// back and every gate in this repository would have stayed green, because the
// argument lived in a migration header and a comment is not a holding — which
// is the finding ADR 0051 was written on.
//
// # The join, and why it is built the way it is
//
// The population is the storefront WRITE routes, taken from [storefrontSurface]
// so that there is ONE notion of what a storefront route is. That scan resolves
// them from PATHS, and ADR 0051 records why: a scan for handlers called
// store-something misses the payment module's two, and a scan for types named
// storeSomethingRequest finds only the two examples the record already cites.
//
// From the route the join runs handler -> table in three hops:
//
//  1. The route carries the module it was registered in, read from the file
//     rather than from the path — the review module's write is registered under
//     /store/v1/products, so the path names a resource and not an owner.
//  2. Inside that module the handler's reach is walked over the call graph, and
//     it is followed into internal/workflows as well. That second tree is not
//     decoration: the cart's opening, its line pricing and its completion all
//     leave the handler through a narrow interface the api package DECLARES and
//     the composition root binds, so a walk that stopped at the module would
//     resolve four of the cart's eleven storefront writes to no table at all —
//     its creation, its line items, its shipping methods and its completion.
//     Measured on 2026-09-08 by taking the workflows back out of the walk, which
//     is exactly what it did.
//  3. Every SQL body reachable that way is read for the tables it INSERTs into,
//     UPDATEs or DELETEs FROM, and the result is intersected with the tables the
//     module's OWN migrations create. The intersection is what makes the walk
//     safe to over-approximate: a module is the sole writer of its own tables
//     (Principle 2.3) and its SQL may name no other module's
//     ([TestModuleSQLNamesOnlyItsOwnTables]), so a name the walk picked up by
//     accident resolves to nothing.
//
// The walk matches a call by its NAME, which over-approximates inside one
// module and is meant to. Under-matching is the failure that costs something
// here: a route resolved to no table is a route whose columns nobody audits, so
// [TestEveryStorefrontWriteReachesATable] refuses to let one exist quietly.
//
// # What this audit does NOT see, stated rather than discovered
//
//   - plugins/. [storefrontSurface] walks internal/modules, and the webpush
//     plugin additionally registers its three storefront POSTs under a chi
//     Route PREFIX, which that resolver does not follow. The plugin is not
//     silently outside the rule: ADR 0051 records it as its SECOND open defect,
//     a standing authority by construction, and the closing rows are an A15 row
//     in docs/gaps.md and ADR 0029's third obligation. Its declaration half was
//     closed on 2026-09-07 (docs/gaps.md D30); the shape was not.
//   - A write another MODULE performs on this route's behalf. Checkout is the
//     case: POST /store/v1/carts/{id}/complete runs a saga that writes an order,
//     and the order module's tables are outside the intersection above. That is
//     the same boundary ADR 0001 draws and the same one module_sql_test.go
//     enforces; the order module's own storefront route is audited on its own
//     row.
//   - A column reached through SQL this repository builds at run time. The walk
//     reads string literals and constants; a statement assembled from a variable
//     resolves to no table, and the route-reaches-a-table guard is what stops
//     that from being silent.

// claimKind is what an identity-claim column CLAIMS.
//
// The four kinds are not a general list of sensitive column names — that list
// exists already and is a different thing: personaldata.Declaration says what a
// table holds ABOUT A PERSON, and internal/modules/review/erasure_test.go audits
// it against the migration. These four are narrower and they come one for one
// out of ADR 0051's worked example, which refused three columns and kept one.
//
// What unites them is that the storefront cannot CHECK any of them. The
// storefront's only principal is a publishable key naming a sales channel, so a
// party, a contact, a network origin and a prior record of the shop are, on
// arrival, four assertions by a party this framework cannot identify — and each
// will later be read as though somebody had established it. That is what "a
// column that cannot mean what it will be read to mean" is.
//
// The byline the review module KEEPS is deliberately outside all four, and it
// is the case that fixes the boundary. A display name asserts nothing: the
// author typed it in order to have it printed under their own words, so it
// claims to be a label and is read as a label. An e-mail address on the same row
// would claim to be a way of reaching that person, and nothing checked it.
type claimKind string

const (
	// claimParty names a person this framework holds records about elsewhere.
	//
	// It is the clause CONFINED states directly: no subject the client names may
	// be a party the writer does not already hold. The cart's customer_id is
	// ADR 0051's first open defect for exactly this, and the order module's
	// SpendingPolicy godoc says what it costs — the rule is applied to the
	// customer PLACING the order, so a declared id lands a B2B spending limit on
	// somebody else's account.
	claimParty claimKind = "names a PARTY"
	// claimContact holds a way of reaching a person off this system.
	//
	// The review migration's argument is the whole of it: storing one here would
	// build a mailing list the shop never asked for and the author never
	// consented to, with no verification and no unsubscribe — which is the
	// property that makes the back-in-stock waitlist fail A15.
	claimContact claimKind = "holds a CONTACT"
	// claimNetwork holds where the writer came from.
	//
	// The review migration refuses it twice over: it would be the only network
	// identifier of a shopper stored anywhere in this repository, and it would
	// buy nothing, because the quota that would use it already exists one layer
	// up on the whole /store/v1 prefix, keyed by the connection.
	claimNetwork claimKind = "holds a NETWORK ORIGIN"
	// claimRecord names a prior record of the shop.
	//
	// It is the subtlest of the four and the review module is why it is listed.
	// An order id would narrow spam and authenticate nobody — ADR 0008 settles
	// customer identity on the embedding application, and anyone who has seen a
	// confirmation page holds one — but a verified-purchase badge rendered from
	// it would be a false statement made by the schema.
	claimRecord claimKind = "names a PRIOR RECORD"
)

// partyColumns are the column names that name a party.
//
// A name is matched whole or as the last two segments, so company_customer_id
// counts and order_line_item_id does not. The list is short on purpose: a
// column naming the row's OWN parent — cart_id on a cart line, order_return_id
// on a return item — is not a party and belongs to the module that owns both
// rows.
var partyColumns = []string{"customer_id", "user_id", "account_id", "person_id"}

// recordColumns are the column names that name a prior record of the shop.
//
// One entry, and it is the one ADR 0051 refused by name. It is deliberately not
// generalized to "any identifier of another module's row": a review's
// product_id is exactly that and the review migration argues at length that it
// is the SUBJECT — what the review is about — which is the opposite end of the
// question from a badge asserting who wrote it.
var recordColumns = []string{"order_id"}

// networkColumns are the column names that hold a network origin.
//
// Nothing in this repository carries one today, and the list exists so that the
// day one arrives it arrives through a decision. That an empty class is still a
// class is pinned by [TestTheIdentityClaimClassIsNotBlind]; a matcher nobody
// tests is a matcher that quietly stops matching.
//
// Matched the same way [partyColumns] is — whole name or tail after an
// underscore — and not by exact equality. Until 2026-09-08 this one list was
// compared with equality while the other three classes matched loosely, so
// visitor_ip_address, signup_ip and last_remote_addr read as NO CLAIM AT ALL:
// the emptiest class in the audit had the narrowest net, which is the wrong way
// round for a class whose whole purpose is to be waiting when the first such
// column arrives. The asymmetry was invisible because the pinning cases named
// only the bare forms; those three are pinned now.
var networkColumns = []string{
	"ip", "ip_address", "client_ip", "remote_ip", "remote_addr", "remote_address", "user_agent",
}

// contactFragments are the substrings that make a column a contact.
//
// Substrings rather than whole names, because a contact column is named after
// what it holds and the shop decides the rest: billing_email, contact_email and
// phone_number all hold the same thing as email and phone do.
var contactFragments = []string{"email", "phone"}

// identityClaimKind reports what a column claims, and whether it claims
// anything at all.
func identityClaimKind(column string) (claimKind, bool) {
	lowered := strings.ToLower(column)

	switch {
	case matchesColumnName(lowered, partyColumns):
		return claimParty, true
	case matchesColumnName(lowered, recordColumns):
		return claimRecord, true
	case matchesColumnName(lowered, networkColumns):
		return claimNetwork, true
	}

	for _, fragment := range contactFragments {
		if strings.Contains(lowered, fragment) {
			return claimContact, true
		}
	}

	return "", false
}

// matchesColumnName reports whether the column is one of the names, either
// whole or as its tail after an underscore.
//
// The tail rule is what makes the net wide enough to catch the qualified names
// a migration really uses (company_customer_id, visitor_ip_address) without
// matching any occurrence of a name inside another word: order_line_item_id is
// not a party and zip is not a network origin, and both are pinned as refusals
// in [claimNameCases].
func matchesColumnName(column string, names []string) bool {
	for _, name := range names {
		if column == name || strings.HasSuffix(column, "_"+name) {
			return true
		}
	}

	return false
}

// The limbs a stored claim may be judged under.
//
// The first two are ADR 0051's decision; the third is the verdict that record
// invented and the reason it is worth having. A feature is compliant, or it is
// a recorded defect WITH A CLOSING ROW — and a defect with a closing row is a
// promise this repository has to keep, which is heavier than an exemption list
// would have been. There is deliberately no fourth value meaning "allowed
// because it is already here".
const (
	// limbConfined is ADR 0051's first limb: the effect completes inside the
	// writer's own reach and touches nothing else.
	limbConfined = "CONFINED"
	// limbInert is its second: the write moves no stock and no money, and every
	// endpoint that acts on it is admin-only and scoped.
	limbInert = "INERT"
	// limbDefect is neither, recorded rather than exempted.
	limbDefect = "OPEN DEFECT"
)

// claimVerdict is how one identity-claim column was judged.
type claimVerdict struct {
	// limb is one of the three constants above.
	limb string
	// closing is the record that closes the defect. It is REQUIRED when the limb
	// is [limbDefect] and refused otherwise: a defect with no closing row is an
	// exemption wearing a heavier word, and ADR 0051 grants no exemptions.
	closing string
	// why is the argument. An entry with no argument is worse than no entry,
	// because the next reader takes it for a decision somebody made.
	why string
}

// storefrontStoredClaims is the verdict on every identity-claim column an
// unidentified storefront write can store, keyed "<module>.<table>.<column>".
//
// # Why there is a register at all, and what stops it becoming a verdict table
//
// ADR 0051 refuses to name its twenty-five endpoints one by one, because a
// hand-kept verdict table is the thing that record's second half exists to
// forbid. This is not that table. Nothing here decides which routes are in
// scope, which tables they reach or which columns are claims — all three are
// MEASURED, and an entry can only ANSWER a finding the measurement already
// made. An entry for a column that has stopped being reachable fails
// [TestNoStorefrontWriteStoresAnUnjudgedIdentityClaim] as loudly as a missing
// one, so the list cannot outlive the tree it describes.
//
// # The one answer the gate refuses, and the exact edge of that refusal
//
// A [claimParty] column whose name a storefront request BODY carries may be
// recorded under NEITHER passing limb — not [limbConfined] and not [limbInert].
// Its only honest entries are the column's absence and [limbDefect] with a
// closing row.
//
// CONFINED is refused by its own text: no subject the client names may be a
// party the writer does not already hold, so a verdict claiming otherwise
// contradicts the record it cites. INERT is refused for a different reason and
// it is worth stating, because the two are not the same argument. Both limbs
// judge the WRITE — what it moves, and who may act on it afterwards — while the
// clause at issue here judges the COLUMN: a column that cannot mean what it will
// be read to mean is worse stored than absent. ADR 0051 settles that clause
// against a client-declared party itself, twice over. It records the cart's
// customer_id as its FIRST open defect rather than as a passing limb; and its
// worked example refused an order id on the reviews table, whose write is the
// record's own INERT specimen — nothing outside the module's scoped admin routes
// acts on a review — and refused it anyway, on what the column would be READ to
// mean. So a limb licenses a write; no limb licenses storing a claim this
// framework cannot check. For the cart in particular INERT is not merely
// unlicensed but false: ADR 0051 says checkout moves stock and money behind an
// unscoped storefront POST, which is the first clause of INERT failing outright.
//
// Until 2026-09-08 only the CONFINED half was checked, and the sentence here
// claimed the row below could not be quietly downgraded. It could:
// cart.carts.customer_id rewritten as INERT left this package GREEN. Both limbs
// are checked now; the mutation is the test's proof and
// [TestAPartyTheClientNamesPassesNeitherLimb] is where it lives.
//
// What is NOT held, so the next reader knows where the edge is:
//
//   - Only [claimParty] is refused this way. A contact, a network origin or a
//     prior record the body carries may still be recorded under either limb, and
//     one of them is: cart.carts.email is CONFINED and ADR 0051 judges it under
//     CONFINED by name.
//   - The evidence is the request-body scan of internal/modules/*/api. A party
//     column the SERVER derives, or one named in a body that scan cannot see,
//     carries no declaredBy and is judged by its entry's prose alone — which is
//     the right answer for customer.customer_address.customer_id, arriving in
//     the path, and is NOT a check for anything under plugins/.
//   - Nothing here reads either limb's own clauses. No test in this file
//     measures whether a write moves stock or money, or whether every endpoint
//     acting on a row is admin-only and scoped. Every other verdict below is
//     held by its argument and by review, exactly as before.
//
// # The cart, and the distinction the whole register turns on
//
// carts.customer_id and carts.email are two columns of one table, written by
// the same eleven routes, and they are judged differently. ADR 0051's closing
// paragraph is where the line is drawn: what reaches a storefront handler in
// its PATH is an identifier built from a millisecond stamp and eighty bits of
// cryptographic randomness, so naming it is holding it, and that is the footing
// every storefront path identifier in this tree stands on. What arrives in the
// BODY is a claim. The cart is a defect for the customer_id in its body and not
// for the {id} in its path, and the customer module's rows below stand on the
// same footing — its NewID is the same construction.
var storefrontStoredClaims = map[string]claimVerdict{
	"cart.carts.customer_id": {
		limb:    limbDefect,
		closing: "ADR 0008 and ADR 0043",
		why: "ADR 0051's FIRST open defect, quoted rather than restated: the cart's unverified " +
			"customer_id, accepted in the creation body and again on handover, breaks CONFINED " +
			"for the storefront's most consequential write. It is not cosmetic, and the order " +
			"module's SpendingPolicy godoc says why — the rule is applied to the customer " +
			"PLACING the order, inside the transaction that writes it, and the identifier " +
			"reaches there straight from the cart's plan, so a declared customer id lands a B2B " +
			"spending limit on somebody else's account. ADR 0043 binds the identity that would " +
			"make storeCustomerID compare; until it is code this column stays a claim",
	},
	"cart.carts.email": {
		limb: limbConfined,
		why: "the address the shop answers the writer at about the writer's OWN order, and the " +
			"one ADR 0051 judges under CONFINED by name: the order confirmation mail sends to " +
			"a client-supplied address with nobody approving it, and condemning that was the " +
			"first of the three counterexamples that killed the human discriminator. It names " +
			"no party — a contact is a datum about the writer, not a subject the writer claims " +
			"to be — and the effect completes inside the writer's own reach",
	},
	"cart.cart_addresses.phone": {
		limb: limbConfined,
		why: "the delivery contact on the writer's own cart, reached only through the cart id in " +
			"the path. It is the courier's number for this parcel rather than a mailing list " +
			"entry, which is the distinction the review migration draws between data given for " +
			"a purpose the writer chose and data taken for something else",
	},
	"customer.customer.email": {
		limb: limbConfined,
		why: "the guest's own contact on the row their own registration created, and the address " +
			"the shop answers them at. The row is reached afterwards only through an identifier " +
			"NewID builds from a millisecond stamp and eighty bits of cryptographic randomness, " +
			"which is the footing ADR 0051's closing paragraph settles for every storefront " +
			"path identifier in this tree. The identity ADR 0043 has still not issued would " +
			"make that footing an argument rather than a construction, and that record is where " +
			"the customer writes are answered",
	},
	"customer.customer.phone": {
		limb: limbConfined,
		why: "as customer.email: the writer's own contact on their own row, reached through a " +
			"path identifier of the same construction",
	},
	"customer.customer_address.customer_id": {
		limb: limbConfined,
		why: "the parent row this address hangs from, and it arrives in the PATH " +
			"(/store/v1/customers/{id}/addresses) rather than in the body. No storefront request " +
			"type in the customer module carries a customer_id field, so the client names no " +
			"party here; the gate checks that rather than taking this sentence's word for it",
	},
	"customer.customer_address.phone": {
		limb: limbConfined,
		why: "the contact for a delivery to this address, on the writer's own address book " +
			"entry; as cart_addresses.phone",
	},
	"order.order_returns.order_id": {
		limb: limbInert,
		why: "the SUBJECT of the return — what is being returned — and it arrives in the PATH. " +
			"ADR 0051 cites this endpoint as the repository's own worked precedent for the " +
			"INERT limb: a record anyone holding the order id can write, which moves no stock " +
			"and no money until an operator receives it, and the three endpoints that act on it " +
			"(receive, refund, cancel) are registered on the scoped admin router. It is the " +
			"same column the review module REFUSED, and the difference is what it is read to " +
			"mean: here it says what the request is about, where on a review it would have said " +
			"who wrote it",
	},
}

// TestNoStorefrontWriteStoresAnUnjudgedIdentityClaim is the schema boundary.
//
// It fails in three directions and each is a different mistake:
//
//   - A claim column with no verdict. Somebody added an e-mail address, an IP
//     address or a customer id to a table an unidentified party writes, and
//     ADR 0051 says that is a decision rather than a field. The review module's
//     three refusals are held here and nowhere else.
//   - A verdict for a claim that is no longer there. The column was dropped or
//     the route stopped reaching the table, and a dead entry is how a register
//     starts covering the next real finding.
//   - A verdict that contradicts the record it cites — a party column the
//     storefront body carries, recorded under either passing limb. See
//     [storefrontStoredClaims] for why INERT is refused as well as CONFINED, and
//     for the three things this refusal does NOT reach.
func TestNoStorefrontWriteStoresAnUnjudgedIdentityClaim(t *testing.T) {
	t.Parallel()

	reach := storefrontWriteReach(t)

	require.Positive(t, reach.columns,
		"no column of any storefront-written table was read; the migrations moved or the "+
			"replay stopped understanding them, and an audit that reads no column approves "+
			"every claim in the schema")
	require.Equal(t, recountReachedColumns(t, reach), reach.columns,
		"the blindness guard above is running on a number that does not mean what its comment "+
			"says. Until 2026-09-08 the counter was incremented once per ROUTE, so carts "+
			"contributed its eleven routes' worth of columns and the total was 548 where 155 "+
			"were examined — harmless while the guard only asks for a positive number, and "+
			"exactly the kind of figure that gets quoted in a document later")

	judged := map[string]bool{}
	for _, claim := range reach.claims {
		verdict, recorded := storefrontStoredClaims[claim.key]
		if !recorded {
			t.Errorf("%s %s, and no storefront write may store one unjudged.\n"+
				"It is written by %s, whose writer this framework cannot identify.\n"+
				"ADR 0051: content from an unidentified party is accepted only when the write "+
				"is CONFINED or INERT, and the discriminator constrains the SCHEMA — a column "+
				"that cannot mean what it will be read to mean is worse stored than absent. "+
				"The review module refused an order id, an e-mail address and an IP address on "+
				"exactly this reasoning.\n"+
				"Drop the column, or record it in storefrontStoredClaims under the limb it "+
				"passes — and if it passes neither, record it as an OPEN DEFECT with the row "+
				"that closes it.",
				claim.key, claim.kind, claim.writtenBy())

			continue
		}

		judged[claim.key] = true
		requireWellFormedVerdict(t, claim.key, verdict)

		if partyNamedByTheClientIsRefused(claim, verdict) {
			t.Errorf("%s is recorded as %s, and %s decodes a request body carrying a %q "+
				"field.\n"+
				"A party the CLIENT names passes neither limb. CONFINED says in as many words "+
				"that no subject the client names may be a party the writer does not already "+
				"hold. INERT does not rescue it either: both limbs judge the write, while the "+
				"clause at issue judges the COLUMN — one that cannot mean what it will be read "+
				"to mean is worse stored than absent — and ADR 0051 settles that clause against "+
				"a client-declared party, refusing an order id on the reviews table whose write "+
				"IS inert.\n"+
				"Record it as an OPEN DEFECT with its closing row, the way ADR 0051 records the "+
				"cart's, or drop the field from the body.",
				claim.key, verdict.limb, routeList(claim.declaredBy), claim.column)
		}
	}

	for _, key := range sortedClaimKeys(storefrontStoredClaims) {
		if judged[key] {
			continue
		}

		t.Errorf("%s is recorded in storefrontStoredClaims and is NOT a claim any storefront "+
			"write can store any more.\n"+
			"Either the column is gone, or no storefront route reaches its table now. Delete "+
			"the entry: a dead verdict reads as a decision somebody made and covers up the "+
			"next real finding — which is docs/gaps.md D16 word for word.", key)
	}
}

// recountReachedColumns counts the columns of the reached tables independently.
//
// It exists so that [storefrontReach.columns] is checked rather than trusted: it
// is the number the blindness guard runs on, and a counter that quietly means
// something else is how a guard keeps passing on a measurement nobody re-made.
func recountReachedColumns(t *testing.T, reach storefrontReach) int {
	t.Helper()

	schemas := map[string]migrationSchema{}
	counted := 0
	for _, qualified := range sortedMapKeys(reach.tableModules) {
		module, table, _ := strings.Cut(qualified, ".")
		if _, read := schemas[module]; !read {
			schemas[module] = moduleTables(t, module)
		}
		counted += len(schemas[module][table])
	}

	return counted
}

// requireWellFormedVerdict checks an entry says something.
//
// A closing row is demanded of a defect and refused of anything else, because
// the two failure modes are opposite. A defect with no closing row is an
// exemption in heavier words, and ADR 0051 grants no exemptions; a compliant
// column carrying a closing row reads as a defect somebody forgot to work.
func requireWellFormedVerdict(t *testing.T, key string, verdict claimVerdict) {
	t.Helper()

	require.Contains(t, []string{limbConfined, limbInert, limbDefect}, verdict.limb,
		"%s is recorded under %q, which is not one of ADR 0051's two limbs or its defect "+
			"verdict", key, verdict.limb)
	require.NotEmpty(t, verdict.why,
		"%s is recorded with no argument; an entry nobody argued is read as a decision "+
			"somebody made", key)

	if verdict.limb == limbDefect {
		require.NotEmpty(t, verdict.closing,
			"%s is recorded as an OPEN DEFECT with no closing row. ADR 0051 records a "+
				"non-compliant surface as a defect precisely so that it carries the record "+
				"that closes it; without one it is an exemption, and that record grants none",
			key)

		return
	}

	require.Empty(t, verdict.closing,
		"%s passes a limb of ADR 0051 and still names a closing row, which reads as a defect "+
			"nobody worked. Drop the closing row or change the limb", key)
}

// partyNamedByTheClientIsRefused reports whether a verdict may NOT stand.
//
// It is one line and it is a named function anyway, because it is the only
// answer this register refuses outright and it has to be exercised on the limbs
// the tree does not currently show. The argument for it, and the precise edge of
// it, are on [storefrontStoredClaims];
// [TestAPartyTheClientNamesPassesNeitherLimb] is the control.
func partyNamedByTheClientIsRefused(claim identityClaim, verdict claimVerdict) bool {
	return claim.kind == claimParty && len(claim.declaredBy) > 0 && verdict.limb != limbDefect
}

// TestAPartyTheClientNamesPassesNeitherLimb is the control on the one refusal.
//
// The tree cannot show it. cart.carts.customer_id is the only party column a
// storefront body declares and it is recorded as an OPEN DEFECT, so every limb
// the refusal has to cover is a limb no entry currently uses — which is exactly
// how the INERT half went missing: until 2026-09-08 the gate compared against
// CONFINED alone, and rewriting the cart's row as INERT left this package GREEN
// while INERT is the plainly false verdict for it, ADR 0051 having recorded that
// checkout moves stock and money behind an unscoped storefront POST.
//
// So both limbs are refused here on a planted claim, and the three cases that
// must still PASS are planted beside them: a defect verdict, a party column the
// client does not name, and a contact column it does. Without those the fix
// would be a matcher that refuses everything, which passes this file and fails
// the tree.
func TestAPartyTheClientNamesPassesNeitherLimb(t *testing.T) {
	t.Parallel()

	route := storefrontRoute{
		verb: "POST", path: "/store/v1/carts", handler: "storeCreateCart", module: "cart",
	}
	declared := identityClaim{
		key:        "planted.rows.customer_id",
		column:     "customer_id",
		kind:       claimParty,
		routes:     []storefrontRoute{route},
		declaredBy: []storefrontRoute{route},
	}

	require.True(t, partyNamedByTheClientIsRefused(declared, claimVerdict{limb: limbConfined}),
		"a party column the storefront BODY carries was accepted as CONFINED. That limb says "+
			"in as many words that no subject the client names may be a party the writer does "+
			"not already hold")
	require.True(t, partyNamedByTheClientIsRefused(declared, claimVerdict{limb: limbInert}),
		"a party column the storefront BODY carries was accepted as INERT, which is the half "+
			"of this refusal that was missing until 2026-09-08. Both limbs judge the WRITE; "+
			"the clause at issue judges the COLUMN, and ADR 0051 settles it against a "+
			"client-declared party — it refused an order id on the reviews table, whose write "+
			"IS inert, on what the column would be READ to mean")
	require.False(t,
		partyNamedByTheClientIsRefused(declared, claimVerdict{limb: limbDefect, closing: "x"}),
		"an OPEN DEFECT with a closing row was refused. That is the entry ADR 0051 asks for "+
			"here, and refusing it would leave the register no honest answer at all")

	derived := declared
	derived.declaredBy = nil
	require.False(t, partyNamedByTheClientIsRefused(derived, claimVerdict{limb: limbConfined}),
		"a party column NO storefront body carries was refused. It is the case "+
			"customer.customer_address.customer_id is: the parent arrives in the PATH, and a "+
			"gate that refused it would be arguing with ADR 0051's closing paragraph rather "+
			"than holding it")

	contact := declared
	contact.kind = claimContact
	contact.column = "email"
	require.False(t, partyNamedByTheClientIsRefused(contact, claimVerdict{limb: limbConfined}),
		"a CONTACT the storefront body carries was refused as though it named a party. "+
			"cart.carts.email is that column and ADR 0051 judges it under CONFINED by name")
}

// TestTheBodyScanIsKeyedByModule holds the qualifier on [structJSONFields].
//
// It lives beside the gate that would break rather than beside the scan, because
// the loss is silent here: the CONFINED-and-INERT refusal above runs on
// declaredBy, and declaredBy is read out of the decoded-body map. A struct name
// shadowed across two api packages would hand one handler the other module's
// field list, and the refusal would simply stop firing.
//
// addressRequest is the real collision the tree already has — nine struct names
// occur in two module api packages each, measured 2026-09-08 — and the two
// versions differ in a field apiece, which is what makes them separable here.
func TestTheBodyScanIsKeyedByModule(t *testing.T) {
	t.Parallel()

	surface := storefrontSurface(t)

	cart, found := surface.decoded["cart.storeSetShippingAddress"]
	require.True(t, found,
		"the cart's shipping-address handler decodes no body the scan can see; it is one of "+
			"the two sides of this pin and the scan has stopped finding it")
	customer, found := surface.decoded["customer.createAddress"]
	require.True(t, found,
		"the customer module's address decode is no longer visible to the scan; the other "+
			"side of this pin is gone")

	require.Equal(t, "addressRequest", cart.name)
	require.Equal(t, "addressRequest", customer.name,
		"the two api packages no longer share this name, so this test proves nothing about "+
			"shadowing any more. Point it at a name they DO share — bare-name indexing is "+
			"still the hazard, whichever struct exhibits it")

	require.Contains(t, cart.jsonFields, "source_address_id",
		"the cart's addressRequest resolved to a struct without its own field, so the body "+
			"scan is indexed by BARE struct name and one api package is shadowing the other")
	require.NotContains(t, cart.jsonFields, "is_default_shipping",
		"the cart's handler resolved to the CUSTOMER module's addressRequest")
	require.Contains(t, customer.jsonFields, "is_default_shipping",
		"the customer's addressRequest resolved to a struct without its own field, so the "+
			"shadowing runs the other way")
	require.NotContains(t, customer.jsonFields, "source_address_id",
		"the customer module's decode resolved to the CART's addressRequest")
}

// TestTheHandlerMapsAreKeyedByModule holds the OTHER half of the keying, which
// is the one no fixture can currently exhibit.
//
// Three maps are looked up per handler — the resolved table set here, and the
// decoded body and query parameters next door — and all twenty-two storefront
// write handler names are distinct today, measured 2026-09-08. That is a fact
// about this tree and not a property of it: two modules naming a handler
// storeCreate is ordinary Go, and under a bare-name key one route's table set
// would overwrite the other's. The gate would then pass on the wrong module's
// evidence while the losing route's columns went unaudited — silently, because
// every route would still resolve to something.
//
// There is no tree state that shows that, so what is checked is the SHAPE of the
// keys: every one is "<module>.<handler>" with a module this scan actually
// walked. A call site reverting to the bare handler name fails here on the first
// key without a dot in it.
func TestTheHandlerMapsAreKeyedByModule(t *testing.T) {
	t.Parallel()

	require.NotEqual(t,
		storefrontRoute{module: "cart", handler: "storeCreate"}.key(),
		storefrontRoute{module: "review", handler: "storeCreate"}.key(),
		"two modules' identically named handlers share one key, so whichever is scanned last "+
			"silently replaces the other in all three per-handler maps")

	surface := storefrontSurface(t)
	reach := storefrontWriteReach(t)

	// Every module directory, not only the ones with a storefront route: the
	// decoded and queried maps carry every handler of every api package, and the
	// point of this check is the KEY SHAPE rather than the population.
	modules := map[string]bool{}
	for _, name := range moduleNames(t) {
		modules[name] = true
	}

	for _, indexed := range []struct {
		what string
		keys []string
	}{
		{"the route-to-table map", sortedMapKeys(reach.tables)},
		{"the decoded-body map", sortedMapKeys(surface.decoded)},
		{"the query-parameter map", sortedMapKeys(surface.queried)},
	} {
		for _, key := range indexed.keys {
			module, handler, qualified := strings.Cut(key, ".")
			require.True(t, qualified,
				"%s is keyed %q, which is a bare handler name. Key it by "+
					"storefrontRoute.key() instead: two modules may name a handler the same, "+
					"and one entry would then overwrite the other with nothing noticing",
				indexed.what, key)
			require.True(t, modules[module],
				"%s is keyed %q, whose %q prefix is no module this scan walked. The key must "+
					"be \"<module>.<handler>\"", indexed.what, key, module)
			require.NotEmpty(t, handler, "%s is keyed %q with an empty handler",
				indexed.what, key)
		}
	}
}

// TestEveryStorefrontWriteReachesATable is the blindness guard of the join, and
// it is the one that earns its keep.
//
// A route resolved to no table is not a clean route — it is a route whose
// columns this audit never looks at, and the whole gate would go green on it.
// The failure is not hypothetical: the first version of this walk stopped at the
// module boundary and resolved FOUR of the cart's eleven storefront writes to
// nothing at all, because the cart opens, prices and completes through narrow
// interfaces the composition root binds. Adding internal/workflows to the walk
// is what fixed it, and taking them back out is how this test was proved: it
// names those four routes and nothing else.
func TestEveryStorefrontWriteReachesATable(t *testing.T) {
	t.Parallel()

	reach := storefrontWriteReach(t)

	require.NotEmpty(t, reach.routes,
		"no storefront write route was resolved anywhere in the module tree, which cannot be "+
			"true while the cart alone registers eleven; the route scan has gone BLIND")

	for _, route := range reach.routes {
		tables := reach.tables[route.key()]
		if len(tables) > 0 {
			continue
		}

		reason, recorded := storefrontWritesNoRow[route.path]
		if recorded {
			require.NotEmpty(t, reason, "%s is recorded as storing nothing with no reason",
				route.path)

			continue
		}

		t.Errorf("%s %s (%s in the %s module) writes NO table this audit can find.\n"+
			"Either it stores nothing — record it in storefrontWritesNoRow with the reason — "+
			"or the walk from the handler to the SQL has broken, and every identity-claim "+
			"column this route can store is now outside the gate while the gate stays green.",
			route.verb, route.path, route.handler, route.module)
	}

	require.Contains(t, reach.tableModules, "review.reviews",
		"the reviews table is not reachable from any storefront write, and it is ADR 0051's "+
			"worked example: POST /store/v1/products/{product_id}/reviews stores one. If the "+
			"walk has lost the single table this decision argues about, it has lost the others "+
			"silently")
}

// storefrontWritesNoRow names a storefront write that stores nothing at all,
// with the reason.
//
// EMPTY, and measured so on 2026-09-08: all twenty-two storefront writes under
// internal/modules resolve to at least one table. An entry here is a claim that
// a write is durable-row-free, which is a strong statement about a route and
// deserves to be written down rather than inferred from a walk coming back
// empty-handed.
var storefrontWritesNoRow = map[string]string{}

// columnClaim is one column and what it claims.
type columnClaim struct {
	column string
	kind   claimKind
}

// identityClaimsOf returns the claim columns of one table, in column order.
//
// It is separated from the walk so that it can be exercised on a planted table:
// the review module's three refusals are columns that do not exist, and the only
// way to prove a gate would catch them coming back is to hand it a table that
// has them ([TestTheRouteToTableAuditCatchesAViolation]).
func identityClaimsOf(columns map[string]bool) []columnClaim {
	var out []columnClaim
	for _, column := range sortedColumnNames(columns) {
		if kind, claims := identityClaimKind(column); claims {
			out = append(out, columnClaim{column: column, kind: kind})
		}
	}

	return out
}

// identityClaim is one claim column a storefront write can store.
type identityClaim struct {
	// key is "<module>.<table>.<column>", the register's key.
	key string
	// column is the bare column name, used in the failure message.
	column string
	kind   claimKind
	// routes are the storefront writes that reach the table.
	routes []storefrontRoute
	// declaredBy are the routes that decode a request field with this column's
	// name. They are the evidence that turns a claim into one the CLIENT makes
	// rather than one the server derives, and CONFINED's second clause is read
	// against them.
	declaredBy []storefrontRoute
}

// writtenBy renders the routes that reach this claim's table.
func (c identityClaim) writtenBy() string { return routeList(c.routes) }

// routeList renders routes for a failure message.
//
// The verb and the path, and never the handler alone: a handler name sends the
// reader grepping, where a path is what the reader can try.
func routeList(routes []storefrontRoute) string {
	parts := make([]string, 0, len(routes))
	for _, route := range routes {
		parts = append(parts, route.verb+" "+route.path)
	}
	sort.Strings(parts)

	return strings.Join(parts, ", ")
}

// storefrontReach is the join, read once.
type storefrontReach struct {
	routes []storefrontRoute
	// tables maps a route's [storefrontRoute.key] — "<module>.<handler>" — to
	// the tables that route writes. Module-qualified for the reason that method
	// gives: two modules may name a handler the same, and keyed by the bare name
	// one route's table set would overwrite the other's and the losing route's
	// columns would go unaudited while this gate stayed green.
	tables map[string][]string
	// tableModules is the set of reached tables as "<module>.<table>", so the
	// worked example can be asserted present.
	tableModules map[string]bool
	claims       []identityClaim
	// columns is how many columns this audit examined, counting each reached
	// table ONCE however many routes reach it; zero means the schema side of the
	// join read nothing. Measured 2026-09-08: 155 columns over 12 distinct
	// tables.
	columns int
}

// storefrontWriteReach resolves every storefront write route to the tables it
// writes, and every reached table to the identity claims it can store.
func storefrontWriteReach(t *testing.T) storefrontReach {
	t.Helper()

	surface := storefrontSurface(t)
	reach := storefrontReach{
		routes:       surface.writes,
		tables:       map[string][]string{},
		tableModules: map[string]bool{},
	}

	byTable := map[string]*identityClaim{}
	schemas := map[string]migrationSchema{}
	graphs := map[string]moduleWriteGraph{}

	for _, route := range surface.writes {
		module := route.module
		if _, read := schemas[module]; !read {
			schemas[module] = moduleTables(t, module)
			graphs[module] = readModuleWriteGraph(t, module)
		}

		tables := graphs[module].tablesWrittenFrom(route.handler, schemas[module])
		reach.tables[route.key()] = tables

		for _, table := range tables {
			qualified := module + "." + table
			if !reach.tableModules[qualified] {
				reach.tableModules[qualified] = true
				reach.columns += len(schemas[module][table])
			}

			for _, found := range identityClaimsOf(schemas[module][table]) {
				key := module + "." + table + "." + found.column
				claim, seen := byTable[key]
				if !seen {
					claim = &identityClaim{key: key, column: found.column, kind: found.kind}
					byTable[key] = claim
				}
				claim.routes = append(claim.routes, route)

				if slices.Contains(surface.decoded[route.key()].jsonFields, found.column) {
					claim.declaredBy = append(claim.declaredBy, route)
				}
			}
		}
	}

	for _, key := range sortedClaimNames(byTable) {
		reach.claims = append(reach.claims, *byTable[key])
	}

	return reach
}

// moduleTables replays a module's migrations and returns the schema they leave.
//
// It is the same replay [TestEveryColumnIsWrittenBySomething] uses, and reusing
// it is deliberate: a second reader of the same migrations would be a second
// answer to "which columns does this table have", and the two would drift.
func moduleTables(t *testing.T, module string) migrationSchema {
	t.Helper()

	dir := filepath.Join(repoRoot, modulesDir, module, migrationsDirName)
	replay := newSchemaReplay()
	replay.apply(readSQL(t, dir, upMigrationSuffix))

	require.Empty(t, replay.unreadable,
		"the %s module's migrations contain statements this replay cannot read, so the schema "+
			"it believes in is not the schema the database has: %v", module, replay.unreadable)

	return replay.tables
}

// moduleWriteGraph is a module's call graph with the SQL hanging off it.
//
// calls maps a function's NAME to every name its body mentions, and sql maps a
// name to the string literals declared under it — a package-level constant, or a
// literal written inside a function body. Both are keyed by bare name, which is
// what makes the walk cheap and what makes it over-approximate; the
// intersection with the module's own schema is where the over-approximation is
// paid back.
type moduleWriteGraph struct {
	calls map[string][]string
	sql   map[string][]string
}

// readModuleWriteGraph parses a module's production Go, together with
// internal/workflows, and builds the graph.
//
// # Why the workflows are in here
//
// A cart is not opened by the cart's handler. The handler resolves CartOpening,
// an interface its own api package declares, and the composition root binds it
// to a workflow that derives the region from the country before the cart module's
// service ever runs. ADR 0001 is why it looks like that — a cross-module surface
// is a narrow interface the CONSUMER defines — and the consequence for a reader
// of the call graph is that the handler's reach ends at an interface method with
// no body. The workflows tree holds the bodies, and the names match because the
// implementing method carries the interface's name.
//
// The SQL is taken from both trees and filtered afterwards by ownership, so a
// workflow that ever grew a statement of its own would be read rather than
// skipped — and a statement naming a table the module does not own would fall
// out of the intersection, where module_sql_test.go is the gate that reports it.
func readModuleWriteGraph(t *testing.T, module string) moduleWriteGraph {
	t.Helper()

	graph := moduleWriteGraph{calls: map[string][]string{}, sql: map[string][]string{}}
	fset := token.NewFileSet()
	parsed := 0

	for _, root := range []string{
		filepath.Join(repoRoot, modulesDir, module),
		filepath.Join(repoRoot, workflowsDirName),
	} {
		err := filepath.WalkDir(root, func(path string, entry fs.DirEntry, err error) error {
			if err != nil {
				return err
			}
			if entry.IsDir() || !strings.HasSuffix(path, ".go") ||
				strings.HasSuffix(path, "_test.go") {
				return nil
			}

			file, parseErr := parser.ParseFile(fset, path, nil, parser.SkipObjectResolution)
			if parseErr != nil {
				return parseErr
			}
			parsed++
			graph.collect(file)

			return nil
		})
		require.NoError(t, err, "%s could not be walked", root)
	}

	require.Positive(t, parsed,
		"no production Go was parsed for the %s module or for the workflows beside it; the "+
			"call graph is empty and every route would resolve to no table", module)

	return graph
}

// collect adds one file's functions, constants and string literals to the graph.
func (g moduleWriteGraph) collect(file *ast.File) {
	ast.Inspect(file, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncDecl:
			g.collectFunc(typed)
		case *ast.ValueSpec:
			// A package-level `const createReview = "INSERT INTO ..."`, which is
			// how sqlc emits every statement it generates.
			for i, name := range typed.Names {
				if i >= len(typed.Values) {
					continue
				}
				if literal, ok := goStringLiteral(typed.Values[i]); ok {
					g.sql[name.Name] = append(g.sql[name.Name], literal)
				}
			}
		}

		return true
	})
}

// collectFunc records the names one function mentions and the SQL written
// inside it.
//
// Every mentioned name is recorded rather than only the called ones, and that is
// what makes sqlc reachable: its generated method does not CALL its statement,
// it passes the constant as an argument.
func (g moduleWriteGraph) collectFunc(fn *ast.FuncDecl) {
	if fn.Body == nil {
		return
	}

	name := fn.Name.Name
	ast.Inspect(fn.Body, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.Ident:
			g.calls[name] = append(g.calls[name], typed.Name)
		case *ast.SelectorExpr:
			g.calls[name] = append(g.calls[name], typed.Sel.Name)
		case *ast.BasicLit:
			if literal, ok := goStringLiteral(typed); ok {
				g.sql[name] = append(g.sql[name], literal)
			}
		}

		return true
	})
}

// goStringLiteral unquotes a Go string literal expression.
func goStringLiteral(expr ast.Expr) (string, bool) {
	literal, ok := expr.(*ast.BasicLit)
	if !ok || literal.Kind != token.STRING {
		return "", false
	}

	value, err := strconv.Unquote(literal.Value)

	return value, err == nil
}

// tablesWrittenFrom walks the graph from one handler and returns the tables of
// the given schema its SQL writes.
func (g moduleWriteGraph) tablesWrittenFrom(handler string, schema migrationSchema) []string {
	seen := map[string]bool{}
	written := map[string]bool{}

	for queue := []string{handler}; len(queue) > 0; {
		name := queue[0]
		queue = queue[1:]
		if seen[name] {
			continue
		}
		seen[name] = true

		for _, body := range g.sql[name] {
			for _, table := range sqlWriteTargets(body) {
				if _, owned := schema[table]; owned {
					written[table] = true
				}
			}
		}

		queue = append(queue, g.calls[name]...)
	}

	out := make([]string, 0, len(written))
	for table := range written {
		out = append(out, table)
	}
	sort.Strings(out)

	return out
}

// sqlWriteTargets returns the tables an SQL body writes.
//
// INSERT, UPDATE and DELETE, and no SELECT: this audit asks what a route can
// STORE. A read reaching a table says nothing about the columns that table may
// carry, and following reads would pull most of a module's schema behind every
// route that looks something up before writing it.
func sqlWriteTargets(body string) []string {
	var out []string

	for _, statement := range sqlStatements(body) {
		for i := 0; i < len(statement); i++ {
			if !statement[i].word {
				continue
			}

			switch statement[i].text {
			case "insert":
				if !wordAt(statement, i+1, "into") {
					continue
				}
				name, next := qualifiedSQLName(statement, i+2)
				if name != "" {
					out = append(out, name)
				}
				i = next - 1
			case "update":
				// "FOR UPDATE" is a lock and "ON CONFLICT DO UPDATE" belongs to
				// the INSERT above it; neither introduces a target.
				if wordAt(statement, i-1, "for") || wordAt(statement, i-1, "key") ||
					wordAt(statement, i-1, "do") {
					continue
				}
				name, set := updateTarget(statement, i+1)
				if name != "" {
					out = append(out, name)
				}
				i = set - 1
			case "delete":
				if !wordAt(statement, i+1, "from") {
					continue
				}
				name, next := qualifiedSQLName(statement, i+2)
				if name != "" {
					out = append(out, name)
				}
				i = next - 1
			}
		}
	}

	return out
}

// sortedMapKeys returns any map's keys in order, so a finding names the same key
// every run.
func sortedMapKeys[V any](indexed map[string]V) []string {
	out := make([]string, 0, len(indexed))
	for key := range indexed {
		out = append(out, key)
	}
	sort.Strings(out)

	return out
}

// sortedClaimKeys returns the register's keys in order.
func sortedClaimKeys(register map[string]claimVerdict) []string {
	out := make([]string, 0, len(register))
	for key := range register {
		out = append(out, key)
	}
	sort.Strings(out)

	return out
}

// sortedClaimNames returns the found claims' keys in order, so the findings do
// not shuffle between runs.
func sortedClaimNames(found map[string]*identityClaim) []string {
	out := make([]string, 0, len(found))
	for key := range found {
		out = append(out, key)
	}
	sort.Strings(out)

	return out
}

// claimNameCase is one column name with the kind it must be read as.
type claimNameCase struct {
	column string
	kind   claimKind
	// why says what the case is defending, so a future reader deleting one has
	// to argue with the sentence rather than with a list.
	why string
}

// claimNameCases pin the matcher in BOTH directions.
//
// The refusals matter as much as the matches. A class that grew until it
// matched product_id would report the review module's SUBJECT as a claim about
// its author, and a gate that cries wolf is a gate somebody deletes — which
// would take the three real refusals with it.
var claimNameCases = []claimNameCase{
	{"order_id", claimRecord, "ADR 0051's first refusal: it would narrow spam, authenticate " +
		"nobody, and be rendered as a verified-purchase badge the schema cannot support"},
	{"email", claimContact, "ADR 0051's second refusal, and the review migration's mailing " +
		"list the shop never asked for"},
	{"ip_address", claimNetwork, "ADR 0051's third refusal: it would be the only network " +
		"identifier of a shopper stored anywhere in this repository"},
	{"ip", claimNetwork, "the same column under the name a migration is likelier to use"},
	{"user_agent", claimNetwork, "a browser fingerprint is where the writer came from by " +
		"another route"},
	{"visitor_ip_address", claimNetwork, "the qualified form a migration is likelier to write " +
		"than the bare one, and the form that read as NO CLAIM until 2026-09-08 because this " +
		"class alone was matched by exact equality"},
	{"signup_ip", claimNetwork, "as visitor_ip_address, on the shortest of the names"},
	{"last_remote_addr", claimNetwork, "as visitor_ip_address: the network class matches its " +
		"tail the way the party class does"},
	{"customer_id", claimParty, "the cart's open defect, and the clause CONFINED states " +
		"directly"},
	{"user_id", claimParty, "the same claim in the auth module's vocabulary"},
	{"company_customer_id", claimParty, "a qualified name still names a party; the match is " +
		"on the tail after an underscore"},
	{"billing_email", claimContact, "a contact column is named after what it holds and the " +
		"shop decides the rest"},
	{"phone", claimContact, "the third thing the review migration says the table does not " +
		"hold, beside the address and the network address"},
	{"phone_number", claimContact, "as phone"},

	{"author_name", "", "the byline the review module KEEPS: the author typed it in order to " +
		"have it printed under their own words, so it claims to be a label and is read as one"},
	{"product_id", "", "the SUBJECT of a review — what it is about — which is the opposite " +
		"end of the question from a badge asserting who wrote it"},
	{"cart_id", "", "the row's own parent inside one module, not a party"},
	{"order_line_item_id", "", "a line of an order, and the reason the party match is on the " +
		"tail rather than on any occurrence of a name"},
	{"order_return_id", "", "as order_line_item_id: the return this item belongs to"},
	{"status", "", "the operator-controlled field, which is the OTHER gate's business " +
		"entirely (storefront_boundary_test.go); this one is about identity"},
	{"title", "", "free text, and declared as personal data by the review module — which is a " +
		"different question from whether it makes a claim"},
	{"body", "", "as title"},
	{"rating", "", "a number about the product"},
	{"created_at", "", "a timestamp the database supplies"},
	{"idempotency_key", "", "a payment session's replay guard; it names nobody"},
	{"external_id", "", "the provider's own reference for a session"},
	{"received_location_id", "", "a warehouse, which is the shop's own and not a party"},
	{"zip", "", "a postal code, and the reason the network match is on the tail after an " +
		"underscore rather than on any occurrence of a name"},
}

// TestTheIdentityClaimClassIsNotBlind pins what the matcher reads as a claim.
//
// Without it the class is a list of strings nobody exercises, and the failure
// mode is silent in the direction that costs: a matcher that stops matching
// makes the whole audit pass by finding nothing, which is the shape this
// repository has been bitten by three times.
func TestTheIdentityClaimClassIsNotBlind(t *testing.T) {
	t.Parallel()

	for _, testCase := range claimNameCases {
		t.Run(testCase.column, func(t *testing.T) {
			t.Parallel()

			kind, claims := identityClaimKind(testCase.column)
			if testCase.kind == "" {
				require.False(t, claims,
					"%q is read as a claim (%s) and must not be: %s",
					testCase.column, kind, testCase.why)

				return
			}

			require.True(t, claims, "%q is read as no claim at all: %s", testCase.column,
				testCase.why)
			require.Equal(t, testCase.kind, kind, "%q is read as %s: %s", testCase.column, kind,
				testCase.why)
		})
	}
}

// TestTheRouteToTableWalkIsNotBlind exercises the walk on a planted module.
//
// It is the half of the audit that cannot be checked against the tree, because
// the tree only ever shows the walk succeeding. What has to be proven here is
// that the walk WALKS: that it crosses the four hops a real write crosses
// (handler, service, repository, generated statement), that it stops where the
// module's ownership stops, and that it does not simply return every table the
// module writes from anywhere — which is what a broken walk would look like
// while every route still resolved to something.
func TestTheRouteToTableWalkIsNotBlind(t *testing.T) {
	t.Parallel()

	schema := migrationSchema{
		"reviews":      {"id": false, "product_id": false},
		"review_notes": {"id": false},
	}

	graph := moduleWriteGraph{
		calls: map[string][]string{
			// The chain a submission really runs down.
			"storeSubmit":  {"decode", "Submit", "toStoreReviewDTO"},
			"Submit":       {"NewID", "Create"},
			"Create":       {"queries", "CreateReview"},
			"CreateReview": {"createReview", "QueryRow"},
			// A read-only chain hanging off the same handler.
			"toStoreReviewDTO": {"GetReview"},
			"GetReview":        {"getReview"},
			// Reached, and its statement names a table this module does not own.
			"Submitted": {"insertElsewhere"},
			// NOT reached from the handler, and it writes an owned table. If the
			// walk returns review_notes, it is reading the module rather than
			// following the route.
			"adminAnnotate": {"createNote"},
		},
		sql: map[string][]string{
			"createReview": {
				"-- name: CreateReview :one\n" +
					"INSERT INTO reviews (id, product_id) VALUES ($1, $2)\n" +
					"ON CONFLICT (id) DO UPDATE SET product_id = EXCLUDED.product_id\n" +
					"RETURNING id, product_id",
			},
			"getReview": {"SELECT id, product_id FROM reviews WHERE id = $1 FOR UPDATE"},
			"createNote": {
				"INSERT INTO review_notes (id) VALUES ($1)",
			},
			"insertElsewhere": {"INSERT INTO orders (id) VALUES ($1)"},
		},
	}

	require.Equal(t, []string{"reviews"}, graph.tablesWrittenFrom("storeSubmit", schema),
		"the walk did not resolve the submission to the one table it writes.\n"+
			"reviews missing means it stopped before the generated statement; review_notes "+
			"present means it read the module instead of following the route; orders present "+
			"means the ownership intersection is not being applied.")

	require.Equal(t, []string{"review_notes"}, graph.tablesWrittenFrom("adminAnnotate", schema),
		"the walk does not reach a statement one hop away, so its finding for the storefront "+
			"route above proves nothing")

	require.Empty(t, graph.tablesWrittenFrom("getReview", schema),
		"a SELECT ... FOR UPDATE was read as a write. This audit asks what a route can STORE, "+
			"and following reads would pull most of a module's schema behind every route that "+
			"looks something up first")

	require.Empty(t, graph.tablesWrittenFrom("nothingAtAll", schema),
		"an unknown name resolved to a table, so the walk is not reading its own graph")
}

// TestTheRouteToTableAuditCatchesAViolation is the positive control.
//
// The review module's three refused columns do not exist, so the tree cannot
// show that their return would be caught — a gate whose subject is an ABSENCE
// can only be proven on a table that has it. This plants the reviews table as it
// would look if the three came back, requires each to be read as the claim
// ADR 0051 refused it for, and requires the register to hold no verdict for any
// of them, which is what makes the finding a failure rather than an entry.
func TestTheRouteToTableAuditCatchesAViolation(t *testing.T) {
	t.Parallel()

	// The reviews table with the three refusals put back, beside the columns the
	// module really has.
	planted := map[string]bool{
		"id": false, "product_id": false, "rating": false, "title": false, "body": false,
		"author_name": false, "status": false, "moderated_at": false, "moderation_note": false,
		"created_at": false, "updated_at": false,
		"order_id": false, "email": false, "ip_address": false,
	}

	require.Equal(t, []columnClaim{
		{column: "email", kind: claimContact},
		{column: "ip_address", kind: claimNetwork},
		{column: "order_id", kind: claimRecord},
	}, identityClaimsOf(planted),
		"the three columns ADR 0051 refused were not all read as claims, and nothing else on "+
			"the row was. The byline is the case that fixes the boundary: it is the one "+
			"identifying column the module KEEPS, so a matcher that flags it would be a "+
			"matcher arguing with the decision instead of holding it.")

	for _, column := range []string{"order_id", "email", "ip_address"} {
		key := "review.reviews." + column
		_, recorded := storefrontStoredClaims[key]
		require.False(t, recorded,
			"%s carries a verdict in storefrontStoredClaims while the column does not exist.\n"+
				"ADR 0051's worked example rests on all three being ABSENT; a standing verdict "+
				"for one would let it be added under an entry somebody wrote before the column "+
				"did.", key)
	}
}
