// Package adminui is gobit's admin panel: server-rendered HTML.
//
// # A DECIDED FUTURE that is not this package
//
// Read this before adding a screen. ADR 0030 decided on 2026-09-06 that the
// panel becomes a single-page application served as static assets, where "every
// read and every write goes over the same admin API an external client would
// use". That decision stands and it is NOT BUILT: what follows describes today's
// package, server-rendered templates reading the Query layer in process.
//
// So a new section built the way the existing ones are built is more of the
// thing that was decided away — which is a reason to weigh it, not a refusal.
// ADR 0031 already writes the same fact in the future tense; a gate's godoc had
// written it in the present, and D34 records what that cost.
//
// # Neither core nor module — a FOURTH tree
//
// This package is a sibling of internal/workflows and lives here for the same
// reason (ADR 0011). Placed under the module tree it would hit three walls, all
// of them measured: it could not import any other module, it could not hand the
// writer to a template inside an api package, and the natural Go spelling that
// runs a template through a field CANNOT EVEN BE EXEMPTED — the call target is
// unresolvable. It cannot live under core (core does not know modules) nor under
// the composition root (that place is wiring only).
//
// The tree's cost is the one ADR 0006 already paid for internal/workflows: since
// rules are written against tree names, this tree is covered by no wiring rule
// by default. The cost is paid by [FromContainer] and by extending the
// registration invariant in internal/arch to reach this tree.
//
// # What it does not know
//
// It does NOT know modules and imports none of them. The server-rendered screens
// take their data from the Query layer through a narrow interface resolved from
// the container BY NAME (ADR 0001/0004/0006); the cart workflow is the proven
// example of the same pattern.
//
// The review screen takes its data from `/admin/v1` instead, in the browser,
// which is the shape ADR 0030 decided and ADR 0076 started. It knows no module
// either — it reads an API — so the sentence above it stays true and the
// mechanism under it does not.
//
// It reads through that interface and writes through narrow surfaces, one per
// module it writes to, each published by its owning module under the module's
// name and ".admin"; the Service...Admin constants name them, [FromContainer]
// resolves them, and internal/arch pins each to its module. Only primitives
// cross those boundaries, and every one goes through the owning SERVICE rather
// than its repository, so the uniqueness checks run and the module's events
// are published (ADR 0013).
//
// The write surfaces are resolved OPTIONALLY. An installation without the
// product module still gets a panel; the edit form answers 503 with a sentence
// naming the reason. A name that IS registered but whose surface does not match
// fails at STARTUP, because that is a wiring mistake rather than a missing
// module.
//
// Of the eighteen modules of internal/modules, the panel writes to the ones a
// screen needed. Nothing here is a general admin surface, and no module gets
// an admin-facing contract until a panel screen needs it: an unused
// compiler-unchecked contract is the error class ADR 0009 names.
//
// The count sits on ONE LINE with the noun and the path, and that is not
// formatting. It used to read "covers ONE of the fifteen / modules", wrapped —
// where it was both WRONG (there were seventeen) and invisible to
// TestTheCountsInTheProseAreTrue, which reads a LINE and admits a sentence that
// names its population by path. Written this way the gate holds it true.
//
// The surface paragraph above, this one and the package's opening used to
// count as well: three surfaces, ONE module, twelve templates. Each count was
// true when written and none had a gate, and by ADR 0290 the panel wrote
// through seven surfaces, each a different module's (D198). The surfaces are
// named by constants a test pins, so the prose no longer repeats their number.
//
// # The sections, and what the sales report does not print
//
// The menu holds the catalog, the price lists, the orders, the
// telephone order, the sales report, the promotions, the campaigns, the
// customers, the customer groups, the notifications, the inventory, the
// shipping options, the store profile, the
// reviews, and the person's own second factor and sessions, in that order;
// the last two are open to everybody who can sign in (ADR 0266,
// ADR 0268). The list lives in one
// place next to
// the routes that serve it, so a section enters the menu by being added there
// rather than by being written into a template nobody edits when adding a
// handler.
//
// This heading used to open "Five sections" and the paragraph named five of
// them. Adding the sixth falsified both in the same commit that added it, which
// is the class this repository keeps paying for — a document its own change
// makes wrong. The count is GONE rather than corrected: it told a reader nothing
// the list beneath it does not, so it was a surface that could only rot. The
// LIST is the substance and [TestTheDocNamesEverySection] holds it.
//
// The two decisions are not the same one, and the difference is worth stating:
// the measurements index keeps its line counts and got a gate (ADR 0075),
// because that number tells a reader something they cannot see — how long the
// file is. A number that only restates the sentence under it is not worth a
// gate, it is worth deleting.
//
// The sales report ([UI.listSales]) is the panel's first consumer of the order
// module's line entity, and it is the screen that most obviously LOOKS like it
// should end in a total. It does not, and the omission is the deliberate half
// of the screen. The read layer offers no aggregation: a provider returns
// records of an entity, and a GROUP BY behind that contract would return
// records of nothing. What a page holds is therefore at most 25 lines out of a
// limit the provider clamps to 100, and a sum over them would print beneath a
// heading that says "Sales" while being the takings of whichever lines sorted
// first. That is a wrong number an operator cannot see is wrong, which is worse
// than no number: a missing total sends somebody to write the query, a wrong
// one ends the question. The report waits for a read surface that can
// aggregate, and that surface belongs to the module rather than to a loop in a
// handler.
//
// The Promotions screen ([UI.listPromotions]) lists the shop's promotions in
// one status at a time with their usage, and it reads them through the
// promotion module's panel surface rather than the read layer: the read
// provider returns only active promotions and leaves the usage out, because it
// cannot tell a storefront from an operator (ADR 0311). Each row offers its
// one move, publish, pause or resume ([UI.switchPromotion]), carrying the
// status it was drawn in so that a promotion another operator moved first is
// refused rather than moved back (ADR 0312), and links to the promotion's
// page ([UI.showPromotion]): its discount, rules, campaign and latest uses,
// read through the same surface (ADR 0313), where the discount's value is
// changed from the one it was drawn with ([UI.reviseDiscount], ADR 0338). Its
// form writes a draft coupon
// with its discount ([UI.createCoupon]), which the module writes in one
// transaction (ADR 0314). The page limits a discount on items to categories
// and removes a rule ([UI.addCategoryRule], [UI.removeRule]); the categories
// are the product module's, read only for an operator who may (ADR 0315).
// It limits a promotion to customer groups as well ([UI.addGroupRule]), the
// groups read through the customer module's group entity only for an operator
// who may read the customers (ADR 0321). A customer's page names their groups
// and puts them into one or takes them out ([UI.addToGroup],
// [UI.removeFromGroup]) through the customer module's surface (ADR 0322), and
// lists their newest orders for an operator who may read the orders, the
// order list listing every one of them (ADR 0358), the list narrowing to one
// status too (ADR 0361), and
// corrects their name and phone from the ones it was drawn with
// ([UI.reviseContact], ADR 0337) and each address's printed fields the same
// way ([UI.reviseAddress], ADR 0342), and adds an address ([UI.addAddress],
// ADR 0359), moves a default to one or removes one ([UI.makeAddressDefault],
// [UI.removeAddress], ADR 0360), and
// the Customer groups screen ([UI.listCustomerGroups]) lists the groups with
// their rank and writes one ([UI.createCustomerGroup], ADR 0323); each row
// renames and re-ranks its group from what it was drawn with
// ([UI.reviseCustomerGroup], ADR 0329).
//
// The Price lists screen ([UI.listPriceLists]) lists the pricing module's
// lists with their type, status and window, and its form writes one
// ([UI.createPriceList]) through the module's panel surface (ADR 0326); each
// row publishes, ends or reopens its list from the status it was drawn in
// ([UI.switchPriceList], ADR 0328), and revises its title, description and
// window from the ones it was drawn with ([UI.revisePriceList], ADR 0330). A
// variant's page lists its prices on those lists and puts one on a list, for
// every customer or for some customer groups ([UI.addListPrice],
// [UI.removeListPrice], ADR 0327).
//
// The Campaigns screen ([UI.listCampaigns]) lists the windows and budgets the
// promotions share, with how much of each budget is used, and its form writes
// a campaign ([UI.createCampaign]) through the same surface (ADR 0319); each
// row revises its campaign's name, description, window and budget limit from
// the ones it was drawn with ([UI.reviseCampaign], ADR 0331). A
// promotion's page puts the promotion into one of them, or out of any, from
// the campaign it was read in ([UI.placeInCampaign], ADR 0320).
//
// The Notifications screen ([UI.listNotifications]) lists the notification
// module's delivery log, the failed ones first or one order's, and sends a
// failed order mail again ([UI.resendNotification]), the confirmation or the
// completion notice, through the module's panel surface (ADR 0317, 0386). The order's page lists what was sent for
// it, for an operator who may read the log, and links to that screen on the
// order (ADR 0318).
//
// The Shipping options screen ([UI.listShippingOptions]) lists the
// fulfillment module's options with their provider, profile, region and fee
// through the module's option entity, and each row renames its option, sets
// a flat option's fee and says whether the storefront offers it, from what it
// was drawn with ([UI.reviseShippingOption], ADR 0333). Its form writes an
// option on a registered provider and one of the newest profiles, in a
// region's currency or a typed one ([UI.createShippingOption], ADR 0334).
// The Regions screen beside it ([UI.listRegions]) lists the region module's
// regions with their currency, tax rate and countries (ADR 0354), each row
// correcting its region's name, taxes and rate ([UI.reviseRegion], ADR
// 0362), and the
// Taxes screen ([UI.listTaxes]) the tax module's tax regions with the rates
// each charges (ADR 0355), each rate correcting its name and rate
// ([UI.reviseTaxRate], ADR 0378).
//
// The Payments screen ([UI.listPayments]) lists the payment module's
// collections across every order, one status at a time, the ones authorized
// first, naming each one's order for an operator who may read the orders
// (ADR 0357).
//
// The Parcels screen ([UI.listParcels]) lists the fulfillment module's
// parcels across every order, one status at a time, the ones still to be
// shipped first, naming each one's order for an operator who may read the
// orders (ADR 0356).
//
// The Store profile screen ([UI.showStoreProfile]) shows who the shop is,
// which every invoice is issued under, and writes it from the profile the
// page was drawn with ([UI.writeStoreProfile]) through the settings module's
// panel surface (ADR 0336). The Invoices screen ([UI.listInvoices]) beside it
// lists the invoice module's documents, the latest first, in one status or in
// all of them, through that module's panel surface (ADR 0343). A document's
// page ([UI.showInvoice]) shows its parties, rows and totals, and moves its
// status from the one it was drawn in ([UI.moveInvoice], ADR 0344); the
// order's page links to it.
//
// The Users screen ([UI.listUsers]) lists the auth module's users with the
// privileges they hold and whether they have proven an authenticator, those
// without one on a tab of their own, through that module's panel surface
// (ADR 0345). A user's page ([UI.showUser]) changes their privileges from the
// ones it was drawn with for an operator who holds admin
// ([UI.reviseUserScopes], ADR 0347). The Users screen invites a user, opened
// without a password with the privileges ticked ([UI.inviteUser]), and their
// page sends the invitation again ([UI.resendInvitation], ADR 0348) and
// removes them ([UI.removeUser], ADR 0349), though not the operator
// themselves. The API keys screen ([UI.listAPIKeys]) beside it lists the
// keys integrations call the API with, their tokens only as redacted, and
// revokes one for an operator holding admin ([UI.revokeAPIKey], ADR 0350),
// and makes one, showing its token once ([UI.makeAPIKey], ADR 0351). The
// Sales channels screen ([UI.listSalesChannels]) lists the channels through
// the auth module's channel entity and corrects each one's name, description
// and whether it is in use from what its row was drawn with
// ([UI.reviseSalesChannel], ADR 0352), and makes one ([UI.makeSalesChannel],
// ADR 0353).
//
// The order's page opens a parcel ([UI.openParcel]) through the order
// module's surface and the fulfilling flow, on the delivery the operator
// chooses when the order was sold several (ADR 0332), and moves one
// ([UI.moveParcel]) —
// shipped, delivered, back undelivered, canceled — through the fulfillment
// module's surface (ADR 0324). A pending order's page cancels it
// ([UI.cancelOrder], ADR 0339), its units written off so their stock comes
// back, or marks it completed, and a completed one's archives it
// ([UI.completeOrder], [UI.archiveOrder], ADR 0340); each of its lines
// writes off units from the count spoken for when the page was drawn
// ([UI.cancelOrderLine], ADR 0341). It lists the order's credits and writes
// one from the credited total the page was drawn with ([UI.creditOrder]),
// puts a pending order's delivery on another quoted option at the price drawn
// ([UI.changeDelivery]), and corrects where it ships from the address row the
// page was drawn with ([UI.correctShippingAddress], ADR 0388). It names the
// order's invoice and issues one
// ([UI.issueInvoice]) through the order module's surface and the invoicing
// flow, on a series the invoice module's surface lists (ADR 0335). A claim on
// it lists its evidence and takes a
// file ([UI.attachEvidence]), stored by the file module's surface and bound by
// the order module's, each under its own write privilege (ADR 0325).
//
// A product's history ([UI.showRevisions]) lists the revisions the product
// module records for its writes, and restores an older one at the version the
// page was read at ([UI.restoreRevision]), refused as an edit is when the
// product moved since (ADR 0316).
//
// # Response bodies go through core's writer
//
// HTML is never STREAMED to the writer. The template is rendered into memory
// first; on failure corehttp.WriteError is called, and only on success does the
// buffer reach corehttp.WriteHTML. Streaming would leave a HALF-written page
// carrying a 200 status when a template fails midway.
//
// # The session reaches the admin API only with the panel's own origin
//
// The panel session travels in an HttpOnly cookie under [CookiePath], which
// covers this tree and the admin API's. On `/admin/v1` [UI.APISession] moves it
// into the header the core reads, and a state change authenticated that way
// must carry the panel's own Origin. The API's CSRF immunity used to be the
// header a browser never attaches by itself; ADR 0030 spent it for a single
// admin surface and ADR 0076 replaced it with that check.
//
// This section used to say the admin API does not accept the cookie, which
// was ADR 0011's rule and stopped being true when ADR 0076 widened the cookie
// (D196). [CookiePath] carries the reasoning.
package adminui
