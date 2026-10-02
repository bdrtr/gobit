package adminui

import (
	"bytes"
	"embed"
	"html/template"
	"net/http"
	"slices"
	"strings"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
)

// templateFiles holds the panel's templates and is EMBEDDED IN THE BINARY.
//
// Embedding is not a convenience but a requirement of the repository's delivery
// promise: "run the binary, it works". Templates read from disk are a second
// artifact that has to travel next to the binary, and a failure that only
// appears on the first request when the working directory is wrong.
//
//go:embed templates/*.gohtml
var templateFiles embed.FS

// layoutFile is the outer frame every page is rendered into.
const layoutFile = "templates/layout.gohtml"

// titleKey is the data key the layout reads the page title from.
//
// It is a constant because the layout looks it up BY NAME: a typo in one page's
// data map would render that page with an empty <title> and nothing would fail.
const titleKey = "Title"

// refusedKey is the template data key carrying a refused write's or search's
// reason, printed as an alert.
const refusedKey = "Refused"

// typedKey is the template data key carrying what a refused form was sent
// with, to draw it again.
const typedKey = "Typed"

// emailKey is the template data key carrying an e-mail a screen was asked
// for or a form was sent with.
const emailKey = "Email"

// statusesKey, statusKey and totalKey carry a list screen's status tabs, the
// chosen one and the count in it, and canCreateKey whether the screen offers
// its form.
const (
	statusesKey  = "Statuses"
	statusKey    = "Status"
	totalKey     = "Total"
	canCreateKey = "CanCreate"
	// canReviseKey says whether a list's rows offer the form that revises
	// what each row names (ADR 0329, ADR 0330, ADR 0331).
	canReviseKey = "CanRevise"
	// writtenKey says a page's form just wrote what it shows, named in the
	// address the form landed on (ADR 0336, ADR 0337, ADR 0338).
	writtenKey = "Written"
	// createdKey carries what a screen's form just wrote, named in the
	// address the form landed on.
	createdKey = "Created"
)

// productsPathKey is the template data key carrying the product list's path,
// which every catalog page links back to.
const productsPathKey = "ProductsPath"

// errorKey is the template key carrying a message meant for the operator.
//
// It is a constant for the same reason titleKey is: three pages fill it and a
// typo in one of them would not fail, it would silently stop showing the
// operator why their edit was refused.
const errorKey = "Error"

// productKey is the template key carrying the product a page is about.
//
// It is a constant for errorKey's reason: the product page, the edit form and
// the related-products form fill it, and a typo in one would render that page
// with a blank product rather than fail.
const productKey = "Product"

// limitKey carries the longest list a form's module keeps, which the form
// prints. It is a constant for errorKey's reason: the related products, the
// add-ons and the bundle forms fill it.
const limitKey = "Limit"

// actionPathKey and cancelPathKey carry where a product form posts and where
// its cancel link leads. They are constants for errorKey's reason: the edit,
// the related-products and the add-ons forms fill them, and a typo in one would
// render a form that posts nowhere rather than fail.
const (
	actionPathKey = "ActionPath"
	cancelPathKey = "CancelPath"
)

// pages lists the panel pages that are looked up BY NAME at runtime.
//
// The list is maintained by hand, deliberately. A page name is a STRING: a typo
// compiles, lints clean, and blows up only when that page is opened — in front
// of a user. [loadTemplates] verifies at startup that every listed page is
// really embedded, which pulls the failure back to startup.
//
// The list is also checked for STALENESS in the other direction: a file that is
// embedded but listed nowhere is dead weight, and that fails too. A one-way
// check would miss the change that forgot to delete a file.
var pages = []string{
	"login.gohtml",
	"error.gohtml",
	"products.gohtml",
	"product.gohtml",
	"product_edit.gohtml",
	"product_new.gohtml",
	"promotions.gohtml",
	"promotion.gohtml",
	"product_revisions.gohtml",
	"notifications.gohtml",
	"campaigns.gohtml",
	"customer_groups.gohtml",
	"price_lists.gohtml",
	"shipping_options.gohtml",
	"store_profile.gohtml",
	"invoices.gohtml",
	"invoice.gohtml",
	"users.gohtml",
	"user.gohtml",
	"api_keys.gohtml",
	"product_relations.gohtml",
	"product_add_ons.gohtml",
	"variant.gohtml",
	"variant_bundle.gohtml",
	"orders.gohtml",
	"order.gohtml",
	"carts.gohtml",
	"cart.gohtml",
	"customers.gohtml",
	"customer.gohtml",
	"inventory.gohtml",
	"sales.gohtml",
	"second_factor.gohtml",
	"sessions.gohtml",
	"reviews.gohtml",
	"plugin_page.gohtml",
}

// templateSet maps a page name to that page's parsed template set.
//
// # Why one set PER PAGE
//
// Each page is parsed TOGETHER with the layout and defines its own "body"
// block. Collected into a single set, only the last block of a given name would
// survive and every page would render the same body — silently.
//
// The alternative was passing the body into the layout as a VALUE, which
// requires converting it to template.HTML, and template.HTML DISABLES escaping.
// In a panel that runs inside an administrator's session, disabling escaping is
// the shortest path to handing an attacker admin privileges.
type templateSet struct {
	sets map[string]*template.Template
	// extra are the navigation entries of the screens plugins registered
	// (ADR 0155). They live here because the frame is filled in here, and a page
	// that had to add its own menu entry would be a page that can forget to.
	extra []navItem
	// scopes is the panel's scope table, the SAME map the routes were bound
	// from. The frame needs it to drop an entry the operator cannot open; taking
	// it from anywhere else would let the menu offer a link the router refuses.
	scopes map[string]string
}

// loadTemplates parses the embedded templates and verifies their names.
//
// It is called AT STARTUP and returns an error; it never panics. The
// composition root turns the error into an exit code, so a broken template
// keeps the server from starting at all. The alternative — panicking via
// template.Must — reaches the same outcome but routes the failure through the
// runtime's panic path instead of the composition root's error path.
func loadTemplates() (*templateSet, error) {
	embedded, err := embeddedPages()
	if err != nil {
		return nil, err
	}

	var missing []string
	for _, name := range pages {
		if !slices.Contains(embedded, name) {
			missing = append(missing, name)
		}
	}
	if len(missing) > 0 {
		return nil, errors.Internal(CodeTemplateInvalid,
			"page(s) the panel expects are not embedded: %s (embedded: %s)",
			strings.Join(missing, ", "), strings.Join(embedded, ", "))
	}

	var extra []string
	for _, name := range embedded {
		if !slices.Contains(pages, name) {
			extra = append(extra, name)
		}
	}
	if len(extra) > 0 {
		return nil, errors.Internal(CodeTemplateInvalid,
			"page(s) embedded but referenced nowhere: %s; the pages list must be stale",
			strings.Join(extra, ", "))
	}

	sets := make(map[string]*template.Template, len(pages))
	for _, name := range pages {
		set, parseErr := template.New(name).ParseFS(templateFiles, layoutFile, "templates/"+name)
		if parseErr != nil {
			return nil, errors.Wrap(parseErr, errors.KindInternal, CodeTemplateInvalid,
				"panel page could not be parsed: %s", name)
		}
		sets[name] = set
	}

	return &templateSet{sets: sets}, nil
}

// embeddedPages returns the embedded template files other than the layout.
func embeddedPages() ([]string, error) {
	entries, err := templateFiles.ReadDir("templates")
	if err != nil {
		return nil, errors.Wrap(err, errors.KindInternal, CodeTemplateInvalid,
			"embedded template directory could not be read")
	}

	layout := strings.TrimPrefix(layoutFile, "templates/")
	out := make([]string, 0, len(entries))
	for _, entry := range entries {
		if entry.IsDir() || entry.Name() == layout {
			continue
		}
		out = append(out, entry.Name())
	}
	slices.Sort(out)

	return out, nil
}

// render builds the page IN MEMORY first and only then writes the response.
//
// The order is mandatory and the reason is measured: streamed straight to the
// writer, an error arising midway through a template would leave a HALF-written
// page carrying a 200 status — once the header is out, neither the panic
// recoverer nor the error writer can do anything and the failure goes silent on
// the client. The buffer keeps the error somewhere it can still become a 500.
//
// The entry point is the LAYOUT, not the page: a page only defines its "body"
// block and the layout draws the frame.
func (t *templateSet) render(
	w http.ResponseWriter, r *http.Request, status int, page string, data map[string]any,
) {
	if data == nil {
		data = map[string]any{}
	}

	// The frame's own fields are filled in HERE rather than by each page. A page
	// that forgot one would render without a stylesheet or without its menu, and
	// nothing would fail — the template would simply see an empty value. Putting
	// them in one place makes forgetting impossible instead of unlikely.
	t.decorateFrame(r, data)

	set, ok := t.sets[page]
	if !ok {
		corehttp.WriteError(r.Context(), w, errors.Internal(CodeTemplateInvalid,
			"the panel has no such page: %s", page))
		return
	}

	var buf bytes.Buffer
	if err := set.ExecuteTemplate(&buf, strings.TrimPrefix(layoutFile, "templates/"), data); err != nil {
		corehttp.WriteError(r.Context(), w, errors.Wrap(err, errors.KindInternal,
			CodeTemplateInvalid, "panel page could not be rendered: %s", page))
		return
	}
	corehttp.WriteHTML(r.Context(), w, status, buf.Bytes())
}

// The data keys the LAYOUT reads for its frame.
//
// They are constants for the reason titleKey is: the layout looks them up by
// name, so a typo would not fail — it would silently render a page with no
// stylesheet or no menu.
const (
	stylesheetKey = "StylesheetPath"
	logoutKey     = "LogoutPath"
	signedInKey   = "SignedIn"
	navKey        = "Nav"
)

// navItem is one entry of the panel's menu.
type navItem struct {
	// Label is what the operator reads.
	Label string
	// Path is where it goes.
	Path string
	// Current marks the section the request is in; the layout turns it into
	// aria-current, which is what the stylesheet keys on AND what a screen
	// reader announces — one fact rather than two that can drift.
	Current bool
}

// sections are the panel's menu, in the order they are shown.
//
// It is a list rather than markup in the template so that a section added to
// the panel enters the menu by being added HERE, next to the route that serves
// it, rather than in a file nobody edits when adding a handler.
func sections() []navItem {
	return []navItem{
		{Label: catalogLabel, Path: ProductsPath},
		// The price lists sit beside the catalog whose prices they hold (ADR
		// 0326).
		{Label: priceListsLabel, Path: PriceListsPath},
		{Label: ordersLabel, Path: OrdersPath},
		// The telephone order sits under the orders it makes (ADR 0290).
		{Label: telephoneLabel, Path: CartsPath},
		// The report sits next to the orders it is made of rather than at the
		// end of the menu: an operator who has just looked at one order and now
		// wants the period around it should not have to cross the whole menu to
		// get there.
		{Label: salesLabel, Path: SalesPath},
		// The promotions sit beside the sales they discount (ADR 0311).
		{Label: promotionsLabel, Path: PromotionsPath},
		// The campaigns hold the promotions' windows and budgets (ADR 0319).
		{Label: campaignsLabel, Path: CampaignsPath},
		{Label: customersLabel, Path: CustomersPath},
		// The groups sit beside the customers they hold (ADR 0323).
		{Label: customerGroupsLabel, Path: CustomerGroupListPath},
		// The notifications sit beside the customers they were sent to (ADR
		// 0317).
		{Label: notificationsLabel, Path: NotificationsPath},
		{Label: inventoryLabel, Path: InventoryPath},
		// The shipping options sit beside the stock they send (ADR 0333).
		{Label: shippingOptionsLabel, Path: ShippingOptionsPath},
		// The shop's identity, which its invoices are issued under (ADR
		// 0336).
		{Label: storeProfileLabel, Path: StoreProfilePath},
		// The invoices sit beside the identity they are issued under (ADR
		// 0343).
		{Label: invoicesLabel, Path: InvoicesPath},
		// The users who operate the shop come after what they operate (ADR
		// 0345).
		{Label: usersLabel, Path: UsersPath},
		// The keys integrations call the API with sit beside the users (ADR
		// 0350).
		{Label: apiKeysLabel, Path: APIKeysPath},
		// The reviews sit LAST, and not because they matter least: they are
		// the panel's first screen of the shape ADR 0030 decided on, so an
		// operator meeting a section that behaves differently meets it at the
		// end of a menu whose other five behave alike.
		{Label: reviewsLabel, Path: ReviewsPath},
		// The person's own second factor comes after every section a privilege
		// opens: it is open to everybody, so the door reaches it only when no
		// section is (ADR 0266).
		{Label: secondFactorLabel, Path: SecondFactorPath},
		{Label: sessionsLabel, Path: SessionsPath},
	}
}

// decorateFrame fills in the fields the layout draws around every page.
func (t *templateSet) decorateFrame(r *http.Request, data map[string]any) {
	data[stylesheetKey] = assetURL(StylesheetPath, stylesheetStamp)
	data[logoutKey] = LogoutPath

	// The sign-out control appears only when there is a session to end. On the
	// login page there is none, and a button that logs nobody out would be an
	// invitation to a confusing click.
	principal, signedIn := corehttp.PrincipalFromContext(r.Context())
	data[signedInKey] = signedIn

	if !signedIn {
		return
	}

	items := sections()
	// A registered screen's entry comes after the six the panel ships, and it is
	// added HERE rather than inside sections() because sections() is also what
	// [validatePages] compares a registration against — a plugin's path must not
	// be able to look like a built-in one to that check.
	items = append(items, t.extra...)
	// An entry the operator's grants do not open is dropped. The route refuses
	// the request either way — that is the check; this is about not offering a
	// link whose only possible answer is a refusal.
	items = allowedItems(items, t.scopes, principal)
	for i := range items {
		// The section is current when the request is inside it, so a product's
		// own page keeps "Catalog" marked rather than leaving the menu blank on
		// every detail screen.
		items[i].Current = r.URL.Path == items[i].Path ||
			strings.HasPrefix(r.URL.Path, items[i].Path+"/")
	}

	data[navKey] = items
}

// The data keys the LIST templates read their paging from.
//
// They are constants for the reason [titleKey] is: the templates look them up
// by name, so a typo would not fail — the page would simply lose its "next"
// link and nobody would be told.
const (
	pageKey     = "Page"
	hasNextKey  = "HasNext"
	hasPrevKey  = "HasPrev"
	nextPageKey = "NextPage"
	prevPageKey = "PrevPage"
	pathKey     = "Path"
)

// addPaging writes the paging fields a list template reads.
//
// It exists because five screens page identically, and five copies of
// "page - 1" are five places for one of them to become "page + 1" without
// anything failing. The screens supply what only they know — which page they
// are on, whether the read returned one row more than the page holds, and their
// own path — and the arithmetic happens once.
//
// What it does NOT write is the rest of the query string. The sales report
// carries a date range in the address and appends it to its own paging links,
// because a "next page" that dropped the period would move the reader to a
// different report without saying so; a helper that guessed which parameters to
// carry would be guessing for every screen at once.
func addPaging(data map[string]any, page int, hasNext bool, path string) {
	data[pageKey] = page
	data[hasNextKey] = hasNext
	data[hasPrevKey] = page > 1
	data[nextPageKey] = page + 1
	data[prevPageKey] = page - 1
	data[pathKey] = path
}
