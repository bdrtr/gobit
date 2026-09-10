// Package adminui is gobit's admin panel: server-rendered HTML.
//
// # A DECIDED FUTURE that is not this package
//
// Read this before adding a screen. ADR 0030 decided on 2026-09-06 that the
// panel becomes a single-page application served as static assets, where "every
// read and every write goes over the same admin API an external client would
// use". That decision stands and it is NOT BUILT: what follows describes today's
// package, twelve server-rendered templates reading the Query layer in process.
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
// It reads through that interface and writes through THREE narrow surfaces, each
// published by its owning module and registered under a name of its own:
// "product.admin" (product basics), "pricing.admin" (a variant's price) and
// "inventory.admin" (a variant's stock). Only primitives cross those
// boundaries, and every one goes through the owning SERVICE rather than its
// repository, so the uniqueness checks run and the module's events are
// published (ADR 0013).
//
// The write surfaces are resolved OPTIONALLY. An installation without the
// product module still gets a panel; the edit form answers 503 with a sentence
// naming the reason. A name that IS registered but whose surface does not match
// fails at STARTUP, because that is a wiring mistake rather than a missing
// module.
//
// The panel's read and write surface together cover ONE module, out of the
// eighteen modules of internal/modules. Nothing here is a general admin
// surface, and no module gets an admin-facing contract until a panel screen
// needs it: an unused compiler-unchecked contract is the error class ADR 0009
// names.
//
// The count sits on ONE LINE with the noun and the path, and that is not
// formatting. It used to read "covers ONE of the fifteen / modules", wrapped —
// where it was both WRONG (there were seventeen) and invisible to
// TestTheCountsInTheProseAreTrue, which reads a LINE and admits a sentence that
// names its population by path. Written this way the gate holds it true.
//
// # The sections, and what the sales report does not print
//
// The menu holds the catalog, the orders, the sales report, the customers, the
// inventory and the reviews, in that order. The list lives in one place next to
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
// # Response bodies go through core's writer
//
// HTML is never STREAMED to the writer. The template is rendered into memory
// first; on failure corehttp.WriteError is called, and only on success does the
// buffer reach corehttp.WriteHTML. Streaming would leave a HALF-written page
// carrying a 200 status when a template fails midway.
//
// # The session stays inside this tree
//
// The panel session travels in an HttpOnly cookie scoped to this tree only. The
// admin API does not accept it: that API's CSRF immunity comes not from a
// defense but from the token living in a header browsers never attach
// automatically, and admitting the cookie there would destroy it (ADR 0011,
// Decision 3).
package adminui
