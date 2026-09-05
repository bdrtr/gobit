package adminui

import (
	"context"
	"net/http"
	"strconv"
	"strings"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/core/query"
)

// The entity and link names the catalog screens spell BY HAND.
//
// The panel imports no module (ADR 0011), so it reaches the read layer the same
// way every other consumer does: by name (ADR 0004). A hand-repeated name is a
// silent-divergence risk of the worst kind — rename a link and the screen still
// compiles, still answers 200 and simply shows no price — so these are
// EXPORTED and pinned against the modules' own constants at COMPILE time by
// [TestThePanelCatalogNamesAgree] in internal/arch, the only package allowed to
// import both sides.
//
// # What is NOT pinned
//
// The filter and field names below are unexported in the modules that own them,
// so no compile-time comparison is possible for them. They are covered only by
// the read layer's own answer: an unknown filter or field is rejected with
// errors.Invalid (ADR 0004), which [UI.catalogFailure] turns into a 500 saying
// the screen asked for something the provider does not offer. That is a
// LOUD failure rather than a silent one, but it arrives on the first request
// instead of at compile time.
const (
	// EntityProduct is the product provider's entity name.
	EntityProduct = "product"
	// EntityVariant is the variant provider's entity name.
	EntityVariant = "variant"
	// EntityRegion is the region provider's entity name; the catalog reads it
	// only to learn each currency's decimal digits.
	EntityRegion = "region"
	// EntityCategory is the product module's category provider's entity name.
	//
	// The panel reads it for ONE purpose: to turn the category identifiers the
	// product filter takes into names an operator can choose between. Without
	// it the filter would exist and be unusable — an operator does not know
	// "pcat_01J…" and would have to fetch it from somewhere else, which is the
	// same as not having the filter.
	//
	// It is EXPORTED for the reason the three above it are: it is the string
	// the whole request is addressed to, a rename in the module leaves this
	// package compiling and answering 200 with an empty dropdown, and exporting
	// it is what lets [TestThePanelCatalogNamesAgree] in internal/arch bind the
	// two at compile time. That test is in a package this one may not edit; the
	// line it needs is reported with the change rather than written here.
	EntityCategory = "category"

	// LinkVariantPriceSet joins a variant to its price set.
	LinkVariantPriceSet = "product_variant_price_set"
	// LinkVariantInventory joins a variant to its inventory item.
	LinkVariantInventory = "product_variant_inventory"

	// FieldAvailableQuantity is the inventory item's sellable total.
	FieldAvailableQuantity = "available_quantity"
)

// The filter names the catalog passes to the providers; see the note above.
const (
	// filterID selects records by identity.
	filterID = "id"
	// filterProductID selects variants belonging to a product.
	filterProductID = "product_id"
	// filterCategoryID narrows a product listing to one category.
	//
	// The spelling is the STOREFRONT's, deliberately: the shop's own listing
	// reads "category_id" from its query string, and one vocabulary across the
	// two surfaces is what keeps an operator's URL and a shopper's URL
	// describing the same narrowing. It is unexported like the two above it
	// because the module does not export its counterpart, so the pair cannot be
	// bound at compile time; the protection is the read layer's refusal of an
	// unknown filter, which [UI.catalogFailure] turns into a loud 500.
	filterCategoryID = "category_id"

	// filterSearch narrows a product listing to the ones whose title matches
	// free text.
	//
	// The spelling is the STOREFRONT's for the same reason [filterCategoryID]'s
	// is, and here it is the harder sell: the shop reads "q" off its own query
	// string (internal/modules/product/api/store.go), the provider answers to
	// that word, and "search" — which reads far better on its own — would mean
	// the two surfaces of one installation asking one question under two names,
	// with a translation table nothing verifies. The panel's own address bar is
	// where the readable word belongs instead; see [paramSearch].
	//
	// It is unexported like its neighbors, with the same consequence: no
	// compile-time pair, and the protection is the read layer's refusal of an
	// unknown filter turned into a loud 500 by [UI.catalogFailure]. The
	// refusal is weaker HERE than for the category, and the difference is worth
	// naming rather than assuming: a filter is only sent when it is applied, so
	// a rename of this word is invisible until somebody actually types in the
	// box. It is loud on the first SEARCH, not on the first request.
	filterSearch = "q"
)

// paramCategory is the query parameter carrying the chosen category.
//
// It is the PANEL's name and not the provider's, following [paramFrom] and
// [paramTo] on the sales report: the address bar is read and edited by people,
// a bookmark outlives a release, and tying the URL to the read layer's
// vocabulary would mean a provider rename silently invalidating every saved
// link. The two names meet in exactly one place, [UI.listProducts], where the
// parameter becomes [filterCategoryID].
const paramCategory = "category"

// paramSearch is the query parameter carrying the text typed in the search box.
//
// It is the PANEL's name and not the provider's, exactly like [paramCategory]:
// the address bar is read, edited and pasted into a message by people, and
// "?search=coffee" says what it does while "?q=coffee" is a thing to look up.
// The provider's own spelling stays in [filterSearch] and the two meet in
// [UI.listProducts] and nowhere else, so the day the read layer renames its
// filter, every bookmark an operator holds keeps working.
//
// The word is repeated BY HAND in products.gohtml, on the box's name attribute
// and in the paging links, because a template is handed data and not constants.
// That repetition is the same silent-divergence risk [paramCategory] already
// carries and it is covered the same way: the tests drive the screen through
// its own query string and read the spec that came out, so a box named one
// thing and read as another fails rather than quietly filtering nothing.
const paramSearch = "search"

// The record fields the catalog reads.
const (
	fieldID          = "id"
	fieldName        = "name"
	fieldTitle       = "title"
	fieldHandle      = "handle"
	fieldStatus      = "status"
	fieldThumbnail   = "thumbnail"
	fieldUpdatedAt   = "updated_at"
	fieldSKU         = "sku"
	fieldPrices      = "prices"
	fieldAmount      = "amount"
	fieldCurrency    = "currency"
	fieldCurrencyCod = "currency_code"
	fieldDecimals    = "decimal_digits"
	fieldAvailable   = FieldAvailableQuantity
)

// The keys the expansions are written under. They are the panel's own choice
// and are never seen outside this package.
const (
	keyPriceSet  = "price_set"
	keyInventory = "inventory"
)

// catalogLabel is what the section is called on screen.
//
// It is a constant for the reason [ordersLabel] is: the menu and the screens
// print the same word, and three copies are three places to rename it in.
const catalogLabel = "Catalog"

// productsPerPage is the product list's page size.
//
// The list is paged rather than unbounded because the read layer's limit is a
// number the CALLER writes: asking for everything makes the panel's first page
// cost grow with the catalog, and a catalog is the one table that always grows.
const productsPerPage = 25

// categoriesInFilter is how many categories the filter's dropdown offers.
//
// It is a CAP and not a page: the control has no "next", so whatever is not in
// these entries cannot be picked. The number is therefore chosen against the
// two failures a dropdown has. Asking for everything — Limit 0, which the read
// layer reads as unlimited — would let one screen pull an unbounded table into
// a <select> nobody can scroll; capping too low would hide categories that
// exist. Two hundred is above any hand-maintained taxonomy this panel has seen
// and small enough to render.
//
// One record MORE than this is requested, the same trick the product list uses
// for its "next" link, and here it answers a different question: whether the
// vocabulary in hand is the WHOLE vocabulary. That answer is what stops the
// screen from telling an operator that a category which merely sits past the
// cap does not exist — see [categoryFilterOf].
const categoriesInFilter = 200

// categoryFilterKey is the data key the product list's filter control is read
// from.
//
// It is a constant for the reason [titleKey] is: the template looks it up BY
// NAME, and a typo would not fail. It would render the list with no dropdown
// and no notice, which is precisely the silent state this control is built to
// avoid.
const categoryFilterKey = "CategoryFilter"

// searchFilterKey is the data key the product list's search box is read from.
//
// It is a constant for the reason [categoryFilterKey] is: the template looks it
// up BY NAME and a typo does not fail. It renders the box EMPTY, and an empty
// box over a narrowed list is exactly the screen this control exists to
// prevent — the operator retypes what they already searched for, or reads the
// short list as the whole catalog.
const searchFilterKey = "SearchFilter"

// searchFilter is everything the product list's search box needs.
//
// One field behind a struct is more than a bare string under a data key, and
// the bare string was rejected on purpose. The template asks the same question
// of BOTH narrowings in four places — the notice above the table, the two
// paging links and the empty-list sentence — and a screen where one of them
// reads ".CategoryFilter.Applied" while the other reads ".Search" invites the
// next editor to treat the two as different kinds of state. That is how one of
// them ends up carried into a paging link and the other does not, which is the
// bug this whole screen keeps being about.
//
// It carries no "unavailable" or "unknown" fact and could not: unlike a
// category, a search box has no vocabulary behind it, so nothing can be read to
// fill it and nothing about the typed text can be checked. The only thing that
// can be wrong with a search is that it matched nothing, and that is a fact of
// the LIST — the empty-list sentence tells it — not of the control.
type searchFilter struct {
	// Term is the text the operator typed, trimmed. Empty means the list is not
	// searched at all; see [productFilters] for why an empty term never becomes
	// a filter.
	Term string
}

// Applied reports whether the list is narrowed by a search.
//
// The template asks this rather than testing Term for emptiness, for the reason
// [categoryFilter.Applied] exists: the one question the box, the notice, the
// paging links and the empty-list message all turn on is answered in ONE place.
func (f searchFilter) Applied() bool {
	return f.Term != ""
}

// categoryOption is one entry of the category dropdown.
type categoryOption struct {
	// ID is what travels in the address and becomes the filter's value.
	ID string
	// Name is what the operator reads. For an identifier the vocabulary does
	// not contain it is the RAW ID: showing the id is ugly and true, while
	// leaving the entry out would make the control disagree with the list under
	// it.
	Name string
	// Selected marks the entry the list is currently narrowed by.
	Selected bool
}

// categoryFilter is everything the product list's category control needs.
//
// It is one struct under one data key rather than five keys, because five keys
// are five chances for the template to look one up under a name nothing checks.
//
// The three "something is wrong" fields are MUTUALLY EXCLUSIVE by construction
// (see [categoryFilterOf]) and they are three rather than one because they are
// three different facts and each deserves a different sentence: the vocabulary
// could not be read at all, the vocabulary was read in full and does not
// contain the chosen identifier, or the vocabulary was cut at
// [categoriesInFilter] so the chosen identifier could not be checked against
// it. Collapsing them into one "bad" flag would make the screen say
// "no such category" about a category that exists.
type categoryFilter struct {
	// ID is the category the address asked for, empty when the list is
	// unfiltered.
	ID string
	// Name is the chosen category's name, empty when it is not known.
	Name string
	// Options are the entries of the dropdown; nil when the vocabulary could
	// not be read, which is what the template keys the control's absence on.
	Options []categoryOption
	// Unavailable reports that the vocabulary read FAILED while the product
	// read succeeded.
	Unavailable bool
	// Unknown reports that the complete vocabulary does not contain [ID].
	Unknown bool
	// Unverified reports that [ID] is not among the entries in hand AND the
	// vocabulary was truncated, so nothing here can say whether it exists.
	Unverified bool
}

// Applied reports whether the list is narrowed by a category.
//
// The template asks this rather than testing ID for emptiness, so that the one
// question the paging links, the notices and the empty-list message all turn on
// is answered in ONE place.
func (f categoryFilter) Applied() bool {
	return f.ID != ""
}

// categoryList is the category vocabulary as it was read, with how complete it
// is.
type categoryList struct {
	Options []categoryOption
	// Truncated reports that the read layer had MORE categories than the cap.
	Truncated bool
	// Unavailable reports that the read failed. Options is then nil, which is
	// NOT the same fact as a shop with no categories at all.
	Unavailable bool
}

// Error codes the catalog can produce.
const (
	// CodeCatalogUnavailable reports that the read layer could not answer.
	CodeCatalogUnavailable = "adminui_catalog_unavailable"
	// CodeProductNotFound reports that the requested product does not exist.
	CodeProductNotFound = "adminui_product_not_found"
)

// productRow is one line of the product list.
type productRow struct {
	ID        string
	Title     string
	Handle    string
	Status    string
	Thumbnail string
	UpdatedAt time.Time
}

// priceView is one price of a variant, ready to print.
type priceView struct {
	// Amount is the formatted amount, or the raw minor-unit integer when the
	// currency's scale is unknown.
	Amount string
	// Currency is the ISO 4217 code.
	Currency string
	// Minor reports that Amount is a RAW minor-unit integer rather than a
	// scaled amount. The template says so next to the number; see
	// [formatAmount] for why an unknown scale is never guessed.
	Minor bool
}

// variantRow is one line of the variant table on the product page.
type variantRow struct {
	ID     string
	Title  string
	SKU    string
	Prices []priceView
	// Stock is the sellable quantity across all locations, or an empty string
	// when the variant has no inventory item.
	Stock string
}

// listProducts renders the product list, optionally narrowed by a category, by
// a search, or by both.
//
// It reads through the cross-module read layer and NOT through a module's
// service (ADR 0011): the panel knows no module, so a Graph call addressed by
// entity name is the only surface it is allowed to use.
//
// # This is NOT the call the storefront listing makes
//
// An older version of this comment claimed it was, and drew comfort from it —
// "the same Graph call the storefront listing uses, so the screen cannot
// drift". That was false. The storefront goes to the product module's own store
// listing in internal/modules/product/service/store.go; this screen goes to the
// read layer's "product" provider, and the two are separate surfaces that have
// to be taught the same filter twice. The gap that measurement found — the shop
// could be narrowed by category and the panel could not — is what this screen
// closes; the provider learned category_id on the read layer, and the two
// surfaces now spell it the same way (see [filterCategoryID]).
//
// The text search closed the second half of that gap the same way: the shop
// could be searched and the panel could only be paged through, so an operator
// looking for one product in a catalog of tens of thousands walked it
// [productsPerPage] rows at a time. The provider learned the storefront's own
// "q" (see [filterSearch]) and this screen spells it identically.
//
// The remaining asymmetry is honest and named: the storefront also takes a TAG
// and this screen does not offer one. A category is a tree an operator
// maintains and can be offered as a list of names, and a search needs no
// vocabulary at all; a tag is free text with neither property — a dropdown of
// every tag in the shop is not a control, and a tag typed by hand would be an
// identifier an operator does not know, which is the same as not having the
// filter.
//
// # Two reads, and why the second one cannot fail the page
//
// The products are one call and the category vocabulary is another, the same
// two-call shape [UI.showProduct] and [UI.listSales] use. The reads are made in
// THIS ORDER on purpose: the product list is the page, the vocabulary is only
// the control that narrows it. A failure of the first is a page that cannot be
// built and is reported as one. A failure of the SECOND is not: the rows are
// already in hand and correct, and answering a request for the catalog with
// "Catalog unavailable" because a dropdown could not be filled would take the
// whole screen away over the loss of a convenience.
//
// What is NOT acceptable is dropping the control silently while a category is
// still applied. The operator would see a short list under a heading that says
// "Products", with nothing on the page saying it had been narrowed — the list
// would then read as the whole catalog, which is the confident-wrong-answer
// class this repository treats as a defect. So the failure degrades the CONTROL
// and is stated on the page: [categoryFilterOf] carries the reason through and
// products.gohtml prints it, together with a link that clears the filter, since
// without the dropdown there is otherwise no way back to the full list but
// editing the address.
//
// # The filters travel in the address, in ONE form
//
// Both narrowings are a GET form writing [paramCategory] and [paramSearch] into
// the query string, following the sales report: a narrowed catalog is a view an
// operator bookmarks and sends to somebody else, and state kept in a session or
// a POST body cannot be sent anywhere. The paging links carry both, because a
// "next page" that dropped either one would move the reader into a wider
// catalog without saying so.
//
// They are ONE form and not two, which is what makes them compose. Two forms
// would each submit only their own control, so choosing a category would
// discard the search the operator had just typed and searching would discard
// the category — and it would discard them by SUBMITTING, that is, the screen
// would answer 200 with a wider list and nothing on it to say what had been
// dropped. Searching inside a category is the request that made this box worth
// building, so the two live in one form and reach [productFilters] together.
//
// # What the two narrowings cost, measured
//
// They are not the same price, and neither price is visible on the screen.
// Measured on a 52,004-product catalog through the same read layer this handler
// calls, with no count query and no sales channel — that is, this screen's exact
// shape (docs/catalog-search-cost.md):
//
//	unnarrowed page                             0.03 ms
//	a term matching almost every product        0.03 ms
//	a term matching ONE product                  9.1 ms
//	a broad term on the LAST page of paging     12.4 ms
//	any term composed with a category           29 ms or more
//
// The order of that list is the finding, and it is the reverse of the intuition
// the missing index invites. The listing is ordered by (created_at DESC,
// id DESC) and asks for [productsPerPage] rows plus one, so a term that matches
// nearly everything is answered by walking that index and stopping after 26
// matches — the search is free. A term that matches one product has nothing to
// stop the walk, so the database reads the whole table. The operator typing a
// product name to find it is therefore in the EXPENSIVE case every time, and
// the operator typing two letters is in the cheap one.
//
// The fourth row is the OFFSET's cost and not the search's: the same last page
// with no term at all is 5.2 ms, because paging by offset makes the database
// walk and discard everything it skips, and the term is then evaluated on each
// row walked past. There is no cheaper option on this road: the read layer's
// list options carry a limit and an offset and NOTHING ELSE (see
// [query.ListOptions]), so the keyset cursor the product module offers its own
// callers is not reachable from here at all — reaching it would mean importing
// the module, which ADR 0011 forbids. That is a real cost of the fourth tree
// and it is worth naming rather than discovering: the deep page is the one
// place where the panel's read is structurally more expensive than the
// storefront's, and no filter this screen sends can change it.
//
// Nothing on this screen has to change for that. Ten milliseconds is a page an
// operator waits on once per form submission, and the count that would dominate
// it is not run here at all (see below). The number is written down because the
// next question asked of this screen — a type-ahead that searches per keystroke,
// or a shop ten times this size — is answered by it and not by intuition. The
// last row is the one to watch: composing a category with the search costs at
// least 29 ms because the category predicate cannot narrow the scan and runs
// once per catalog row, and it is a FLOOR rather than a measurement, since the
// measured catalog carries no category memberships at all.
//
// An EMPTY value is not a filter, for either of them. The parameters are
// trimmed and an empty result is left OUT of the map rather than passed through
// (see [productFilters]); the module measured this exact class on its own list
// road (TestEmptyTextArgumentBuildsNoFilter in internal/modules/product/graph)
// and the cost is silent. Neither empty is a theoretical shape here: the "All
// categories" entry of the dropdown submits an empty category, and an untouched
// box submits an empty term on every single use of the form.
func (u *UI) listProducts(w http.ResponseWriter, r *http.Request) {
	page := pageNumber(r.URL.Query().Get("page"))
	chosen := strings.TrimSpace(r.URL.Query().Get(paramCategory))
	typed := strings.TrimSpace(r.URL.Query().Get(paramSearch))

	records, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityProduct,
		Fields:  []string{fieldID, fieldTitle, fieldHandle, fieldStatus, fieldThumbnail, fieldUpdatedAt},
		Filters: productFilters(chosen, typed),
		Limit:   productsPerPage + 1,
		Offset:  (page - 1) * productsPerPage,
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The product list could not be read.")
		return
	}

	// One extra record is requested so "is there a next page" is answered
	// without a second, counting query — a count over a growing catalog is the
	// expensive half of pagination and this screen does not need the total.
	//
	// Measured at 52,004 products, this screen's own shape: the page above is
	// 0.03 ms and the count it does not run is 3.3 ms, so the extra row buys
	// back roughly a hundredfold. The count is the half that cannot stop early,
	// which is why it grows with the catalog while the page does not
	// (docs/catalog-search-cost.md).
	hasNext := len(records) > productsPerPage
	if hasNext {
		records = records[:productsPerPage]
	}

	rows := make([]productRow, 0, len(records))
	for _, rec := range records {
		rows = append(rows, productRowOf(rec))
	}

	data := map[string]any{
		titleKey:          "Products",
		"Products":        rows,
		categoryFilterKey: categoryFilterOf(chosen, u.categoryList(r.Context())),
		// The TRIMMED term goes to the screen, not the raw parameter: the box
		// is refilled from this value, and refilling it with the spaces the
		// operator happened to type would show a box whose contents no longer
		// match the filter that was applied.
		searchFilterKey: searchFilter{Term: typed},
	}
	addPaging(data, page, hasNext, ProductsPath)

	u.templates.render(w, r, http.StatusOK, "products.gohtml", data)
}

// productFilters builds the read layer's filter map out of the two narrowings
// the address can carry.
//
// Both values arrive TRIMMED from [UI.listProducts]; what is decided here is
// the empty case, and it is decided identically for the two of them — an empty
// value is left OUT of the map rather than passed through. What that costs is
// different for each, and neither cost is visible on the screen that pays it:
//
//   - An empty category filters by an identity nothing has, so the answer is an
//     empty table that reads as an empty shop.
//   - An empty term is the opposite and no better: it narrows NOTHING. The
//     provider normalizes a blank one away, and the predicate underneath it is
//     a "title ILIKE '%' || term || '%'" that every title satisfies anyway
//     (internal/modules/product/repository/saleschannel.go), so by either route
//     the rows are the whole catalog. The screen is what would lie about them —
//     the notice above the table would say the list was narrowed, every paging
//     link would carry a filter nobody asked for, and an empty page would be
//     blamed on a search nobody typed.
//
// The provider's own normalization is not what this decision rests on, and the
// panel does not lean on it. The screen's copy of the rule exists because the
// screen has its own state to keep honest: the read layer could drop a blank
// term and the panel would still be showing a search box, a notice and two
// paging links that all claim a narrowing that never reached the query.
//
// The map is nil when neither is applied rather than empty. The read layer
// treats the two the same, but the specs this package builds are read back by
// its own tests, and "this screen sent no filters at all" is the fact those
// tests assert on.
//
// It is a function rather than four lines inside the handler because the rule
// has to hold for each key ALONE and for the two TOGETHER, which is a table a
// test can walk, and because the composition is the part that breaks: a search
// written into the spec by REPLACING Filters instead of adding to it would drop
// the category silently and show a wider catalog than the address describes.
func productFilters(category, search string) map[string]any {
	filters := map[string]any{}
	if category != "" {
		filters[filterCategoryID] = category
	}
	if search != "" {
		filters[filterSearch] = search
	}
	if len(filters) == 0 {
		return nil
	}

	return filters
}

// categoryList reads the category vocabulary the product filter is built from.
//
// A failure is REPORTED IN THE RESULT rather than returned, the way
// [UI.currencyScales] reports a missing region module: both are reads that
// enrich a page rather than produce it, and both have exactly one caller, which
// would otherwise write the same "log it and carry on" branch. The real error
// goes to the log, because the operator cannot act on it and the page has
// somewhere better to put its words than a provider's message.
//
// The absent vocabulary is deliberately NOT the same value as an empty one. A
// shop with no categories should show a dropdown holding only "All categories";
// a shop whose category provider is not registered should show no dropdown at
// all and say why. Returning an empty slice for both would silently turn the
// second into the first, and the operator would conclude their taxonomy had
// been deleted.
func (u *UI) categoryList(ctx context.Context) categoryList {
	records, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityCategory,
		Fields: []string{fieldID, fieldName},
		// One more than the cap, so the answer to "was there more" comes out of
		// this read instead of a counting one; see [categoriesInFilter].
		Limit: categoriesInFilter + 1,
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the category vocabulary; the catalog filter will be absent",
			"error", err)

		return categoryList{Unavailable: true}
	}

	truncated := len(records) > categoriesInFilter
	if truncated {
		records = records[:categoriesInFilter]
	}

	options := make([]categoryOption, 0, len(records))
	for _, rec := range records {
		id := recordString(rec, fieldID)
		if id == "" {
			// A record with no identity cannot be chosen: submitting it would
			// send an empty parameter, which this screen reads as "no filter",
			// so the entry would silently clear the operator's choice.
			continue
		}
		options = append(options, categoryOption{ID: id, Name: recordString(rec, fieldName)})
	}

	return categoryList{Options: options, Truncated: truncated}
}

// categoryFilterOf builds the control's view from the chosen id and what was
// read.
//
// # Why an identifier nobody recognizes is still applied
//
// The list has ALREADY been read with the filter by the time this runs, and
// re-reading it without would cost a second query to show a different catalog
// than the address asked for. More importantly the vocabulary is not the
// authority on which categories exist — it can be truncated, it can be
// unavailable — so silently widening the view to the whole catalog would mean
// showing every product on a screen whose address says one category. An
// operator reading that would take the rows for members of the category.
//
// The applied filter therefore stands and the SCREEN explains the emptiness.
// The three failing states are kept apart because each of them warrants a
// different sentence, and the pure function is where they are made exclusive so
// that no template branch can ever show two of them at once.
func categoryFilterOf(chosen string, list categoryList) categoryFilter {
	filter := categoryFilter{ID: chosen, Options: list.Options}

	if list.Unavailable {
		// Nothing here can be said about the identifier: with no vocabulary in
		// hand, "unknown" would be a claim about data that was never read.
		filter.Unavailable = true

		return filter
	}

	for i := range filter.Options {
		if filter.Options[i].ID != chosen {
			continue
		}
		filter.Options[i].Selected = true
		filter.Name = filter.Options[i].Name

		return filter
	}

	if chosen == "" {
		return filter
	}

	// The chosen identifier is not among the entries in hand, and the control
	// must still show it as the selected one. A <select> whose value matches no
	// option renders its FIRST option as selected, which here is "All
	// categories" — a control claiming the list is unfiltered while it is
	// filtered, which is worse than an ugly entry showing a raw identifier.
	filter.Options = append(filter.Options, categoryOption{ID: chosen, Name: chosen, Selected: true})
	filter.Unknown = !list.Truncated
	filter.Unverified = list.Truncated

	return filter
}

// showProduct renders one product together with its variants, prices and stock.
//
// Two Graph calls are made, not one, and that is a property of the data rather
// than a shortcut: variants live in the SAME module as products, so there is no
// link between them and the read layer joins only across links. Asking for
// variants is therefore a root query of its own, filtered by product_id.
//
// The prices and the stock DO come through links, in the same call: one
// expansion each, both batched by the read layer. A screen that fetched them
// per variant would issue a query per row, which is exactly what the read
// layer's no-N+1 rule exists to prevent.
func (u *UI) showProduct(w http.ResponseWriter, r *http.Request) {
	id := chi.URLParam(r, "id")
	if strings.TrimSpace(id) == "" {
		u.errorPage(w, r, http.StatusNotFound, "Not found", "No product was named.")
		return
	}

	products, err := u.catalog.Graph(r.Context(), productByID(id))
	if err != nil {
		u.catalogFailure(w, r, err, "The product could not be read.")
		return
	}
	if len(products) == 0 {
		u.errorPage(w, r, http.StatusNotFound, "Not found",
			"There is no product with that id.")
		return
	}

	variants, err := u.catalog.Graph(r.Context(), query.GraphSpec{
		Entity:  EntityVariant,
		Fields:  []string{fieldID, fieldTitle, fieldSKU},
		Filters: map[string]any{filterProductID: []string{id}},
		Expand: []query.Expansion{
			{Link: LinkVariantPriceSet, As: keyPriceSet, Fields: []string{fieldID, fieldPrices}},
			{Link: LinkVariantInventory, As: keyInventory, Fields: []string{fieldID, fieldAvailable}},
		},
	})
	if err != nil {
		u.catalogFailure(w, r, err, "The variants could not be read.")
		return
	}

	scales := u.currencyScales(r.Context())
	rows := make([]variantRow, 0, len(variants))
	for _, rec := range variants {
		rows = append(rows, variantRow{
			ID:     recordString(rec, fieldID),
			Title:  recordString(rec, fieldTitle),
			SKU:    recordString(rec, fieldSKU),
			Prices: pricesOf(rec, scales),
			Stock:  stockOf(rec),
		})
	}

	product := productRowOf(products[0])
	u.templates.render(w, r, http.StatusOK, "product.gohtml", map[string]any{
		titleKey:       product.Title,
		"Product":      product,
		"Variants":     rows,
		"ProductsPath": ProductsPath,
		"EditPath":     ProductsPath + "/" + product.ID + "/edit",
	})
}

// unexpectedFailure renders the panel's own error page for a failure the
// operator cannot act on, and logs the real cause.
//
// # Why not corehttp.WriteError
//
// That writer produces the framework's JSON envelope, which is right for an API
// endpoint and wrong here: the panel's client is a BROWSER that navigated to
// this path, and answering it with JSON makes the failure unreadable to the one
// person who could do something about it. The split is the same one
// error.gohtml describes — the envelope stands beside the panel's page, it is
// not replaced by it.
//
// The underlying message is NOT shown. The framework treats a non-Internal
// message as client-safe because a service author wrote it, but that promise was
// made about API clients; a panel page is read by an operator who cannot tell a
// leaked connection string from a diagnosis. The real error goes to the log.
func (u *UI) unexpectedFailure(w http.ResponseWriter, r *http.Request, err error, title string) {
	corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
		"the panel could not complete the request",
		"error", err, "path", r.URL.Path)

	u.errorPage(w, r, corehttp.StatusFor(err), title,
		"The request could not be completed. The reason is in the server log.")
}

// catalogFailure turns a read-layer failure into a page the operator can act
// on.
//
// The underlying error is NOT shown: it can carry a provider name, a query or a
// database address. What is shown is what the operator can do about it, and the
// real error goes to the log through the core's error path — the same split
// [corehttp.WriteError] makes for the API.
func (u *UI) catalogFailure(w http.ResponseWriter, r *http.Request, err error, message string) {
	corehttp.LoggerFromContext(r.Context()).ErrorContext(r.Context(),
		"the panel could not read the catalog",
		"error", err, "path", r.URL.Path)

	status := http.StatusServiceUnavailable
	hint := message + " The read layer did not answer; a module may not be registered."
	if errors.IsInvalid(err) {
		// An invalid spec is OUR bug, not an outage: the field, filter or link
		// name this package spells no longer matches the provider. Saying
		// "temporarily unavailable" would send the operator to look at the
		// database while the fix is in this file.
		status = http.StatusInternalServerError
		hint = message + " The screen asked the read layer for something it does not offer."
	}

	u.errorPage(w, r, status, "Catalog unavailable", hint)
}

// currencyScales maps a currency code to its number of decimal digits.
//
// # Why the panel reads regions to print a price
//
// An amount is an INTEGER in minor units. Turning it into something a human
// reads needs the currency's scale, and that scale is NOT two for every
// currency: ISO 4217 has 0-digit currencies (JPY, KRW), 2-digit ones (most) and
// 3-digit ones (KWD, BHD). A presentation layer that assumed 100 would show the
// wrong amount for two of the three classes — and show it confidently.
//
// The scale lives in the region module's currency table and reaches the panel
// through the same read layer as everything else. When it cannot be read the
// map comes back EMPTY rather than defaulting to two: [formatAmount] then
// prints the raw minor-unit integer and says so. A missing region module
// degrades the display, it does not corrupt it.
func (u *UI) currencyScales(ctx context.Context) map[string]int {
	scales := map[string]int{}

	regions, err := u.catalog.Graph(ctx, query.GraphSpec{
		Entity: EntityRegion,
		Fields: []string{fieldID, fieldCurrencyCod, fieldCurrency},
	})
	if err != nil {
		corehttp.LoggerFromContext(ctx).WarnContext(ctx,
			"the panel could not read the currency scales; amounts will be shown in minor units",
			"error", err)

		return scales
	}

	for _, rec := range regions {
		currency, ok := rec[fieldCurrency].(map[string]any)
		if !ok {
			continue
		}
		code := stringValue(currency[fieldCurrencyCod])
		if code == "" {
			code = stringValue(currency["code"])
		}
		digits, ok := intValue(currency[fieldDecimals])
		if code == "" || !ok {
			continue
		}
		scales[strings.ToUpper(code)] = digits
	}

	return scales
}

// productByID is the spec that reads one product.
//
// The list, the detail page and the edit form share it so the three screens
// cannot start reading the same product differently.
func productByID(id string) query.GraphSpec {
	return query.GraphSpec{
		Entity:  EntityProduct,
		Filters: map[string]any{filterID: []string{id}},
		Limit:   1,
	}
}

// productRowOf turns a product record into a row.
//
// The list and the detail page share it so the two screens cannot start reading
// the same product differently — a field renamed in one and not the other would
// show a title on one screen and a blank on the other.
func productRowOf(rec query.Record) productRow {
	return productRow{
		ID:        recordString(rec, fieldID),
		Title:     recordString(rec, fieldTitle),
		Handle:    recordString(rec, fieldHandle),
		Status:    recordString(rec, fieldStatus),
		Thumbnail: recordString(rec, fieldThumbnail),
		UpdatedAt: recordTime(rec, fieldUpdatedAt),
	}
}

// pricesOf reads a variant's prices out of the price-set expansion.
func pricesOf(rec query.Record, scales map[string]int) []priceView {
	set, ok := rec[keyPriceSet].(query.Record)
	if !ok {
		return nil
	}

	raw, ok := set[fieldPrices].([]map[string]any)
	if !ok {
		return nil
	}

	out := make([]priceView, 0, len(raw))
	for _, price := range raw {
		amount, ok := intValue(price[fieldAmount])
		if !ok {
			continue
		}
		code := strings.ToUpper(stringValue(price[fieldCurrencyCod]))
		text, exact := formatAmount(int64(amount), code, scales)
		out = append(out, priceView{Amount: text, Currency: code, Minor: !exact})
	}

	return out
}

// stockOf reads the sellable quantity out of the inventory expansion.
//
// An empty string means "this variant has no inventory item", which is NOT the
// same as zero stock: printing 0 would tell the operator the product is sold
// out when nothing tracks it at all.
func stockOf(rec query.Record) string {
	item, ok := rec[keyInventory].(query.Record)
	if !ok {
		return ""
	}
	quantity, ok := intValue(item[fieldAvailable])
	if !ok {
		return ""
	}

	return strconv.FormatInt(int64(quantity), 10)
}

// formatAmount turns a minor-unit integer into a readable amount.
//
// The second result reports whether the currency's scale was KNOWN. When it was
// not, the raw integer is returned unchanged and the caller marks it as minor
// units; guessing two digits would print 1000 JPY as "10.00" and 1000 KWD as
// "10.00" while the right answers are "1000" and "1.000".
//
// The arithmetic stays in integers throughout. Dividing by a power of ten in
// floating point is exactly the operation plan Section 8 forbids for money: at
// large amounts the result is no longer the number that was stored.
func formatAmount(minor int64, code string, scales map[string]int) (string, bool) {
	digits, ok := scales[code]
	if !ok || digits < 0 {
		return strconv.FormatInt(minor, 10), false
	}
	if digits == 0 {
		return strconv.FormatInt(minor, 10), true
	}

	negative := minor < 0
	if negative {
		minor = -minor
	}

	text := strconv.FormatInt(minor, 10)
	if len(text) <= digits {
		text = strings.Repeat("0", digits-len(text)+1) + text
	}

	split := len(text) - digits
	out := text[:split] + "." + text[split:]
	if negative {
		out = "-" + out
	}

	return out, true
}

// pageNumber reads the page parameter; anything unreadable is page one.
//
// A bad page number is not an error the operator needs to see: the address bar
// is edited by hand and "?page=abc" answering with an error page would be
// louder than the mistake. Falling back to the first page is both harmless and
// obvious on screen.
func pageNumber(raw string) int {
	page, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || page < 1 {
		return 1
	}

	return page
}

// recordString reads a string field, or "" when it is absent or not a string.
func recordString(rec query.Record, field string) string {
	return stringValue(rec[field])
}

// recordTime reads a time field, or the zero time.
func recordTime(rec query.Record, field string) time.Time {
	t, _ := rec[field].(time.Time)

	return t
}

// stringValue narrows any to a string without panicking.
func stringValue(v any) string {
	s, _ := v.(string)

	return s
}

// intValue narrows any to an int, accepting the integer shapes a provider may
// return.
//
// float64 is DELIBERATELY not accepted. The read layer runs in-process and
// hands the provider's own values through, so an amount arrives as an integer;
// a float64 here would mean the value had passed through a JSON round trip and
// lost precision, and rendering it would print a wrong price confidently
// (plan Section 8: money is never a float).
func intValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}
