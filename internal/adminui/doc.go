// Package adminui is gobit's admin panel: server-rendered HTML.
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
// It does NOT know modules and imports none of them. Data comes from the Query
// layer through a narrow interface resolved from the container BY NAME (ADR
// 0001/0004/0006); the cart workflow is the proven example of the same pattern.
//
// Today it only READS. Catalog writes are deliberately deferred: no module
// exposes an admin-facing narrow surface, and opening one means adding
// compiler-unchecked contracts to three modules (ADR 0011, Decision 6).
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
