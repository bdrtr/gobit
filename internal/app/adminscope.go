// The admin surfaces' privilege gate (ADR 0434): every route bound under
// /admin/v1 demands a privilege, or is one of the few that act on the caller
// alone and is served by the module that owns them; every route under the
// panel's address is one the panel bound; and gobit owns both prefixes, so a
// path under either that no route of theirs takes is answered by gobit and by
// no pattern bound elsewhere. An installation in which a route breaks those
// rules does not start. It is its own file because the exemptions are a
// security decision that has to be read in one place, beside the walk that
// holds them.

package app

import (
	"fmt"
	"maps"
	"net/http"
	"reflect"
	"runtime"
	"slices"
	"strings"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/errors"
	corehttp "github.com/bdrtr/gobit/core/http"
	"github.com/bdrtr/gobit/internal/adminui"
	authapi "github.com/bdrtr/gobit/internal/modules/auth/api"
)

// codeAdminRouteUnscoped is the code an installation refuses to start with
// when a route on the admin surfaces is not held to a privilege (ADR 0434).
const codeAdminRouteUnscoped = "admin_route_unscoped"

// codeRouteNotFound is the code gobit answers a path under /admin/v1 with when
// no route matches it.
const codeRouteNotFound = "route_not_found"

// AdminRoute names a route by its method and its path pattern, as chi binds it.
type AdminRoute struct {
	// Method is the HTTP method, upper case.
	Method string
	// Path is the pattern the route is bound under, parameters in braces.
	Path string
}

// ownAdminRoutes are the routes under the admin prefix that demand no
// privilege, each with the reason it is the caller's own (ADR 0434).
//
// The admin ring proves who is calling and nothing else; a privilege says what
// the caller may do to the records of others. Each route here acts on nobody
// yet, or on the caller alone, so a privilege on it would only lock a person
// out of their own account: an operator whose privileges were taken away must
// still be able to sign out and close the session a stolen laptop holds.
//
// A route is its method and its path, as ADR 0255 keys the panel's table, so a
// second verb on one of these paths, or a new path under /admin/v1/auth,
// arrives with a decision rather than inheriting the exemption. The exemption
// is the auth module's handler's and not the path's: chi replaces a route
// bound twice, and a module bound later that served one of these paths would
// otherwise serve every admin principal under it ([authPackage]).
var ownAdminRoutes = map[AdminRoute]string{
	{http.MethodPost, authapi.LoginPath}: "signs a person in: there is nobody yet to hold a privilege",
	{http.MethodPost, authapi.AcceptInvitationPath}: "sets an invited person's first password: " +
		"they have no account to authenticate with yet (ADR 0137)",
	{http.MethodGet, "/admin/v1/auth/me"}:      "reads back the caller's own identity and privileges",
	{http.MethodPost, "/admin/v1/auth/logout"}: "ends the caller's own sessions",
	{http.MethodPost, authapi.MFAEnrolPath}:    "enrolls a second factor on the caller's own account (ADR 0264)",
	{http.MethodPost, authapi.MFAConfirmPath}:  "confirms the caller's own second factor (ADR 0264)",
	{http.MethodPost, authapi.MFARemovePath}: "removes the caller's own second factor, given the code " +
		"it shows (ADR 0264)",
	{http.MethodGet, authapi.SessionsPath}:              "lists the caller's own sessions (ADR 0267)",
	{http.MethodPost, authapi.SessionsRevokeOthersPath}: "closes the caller's other sessions (ADR 0267)",
	{http.MethodPost, authapi.SessionRevokePath}:        "closes one of the caller's own sessions (ADR 0267)",
}

// The packages whose handlers the gate accepts where a privilege is not the
// question: the auth module's on [ownAdminRoutes], the panel's under its
// address. They are read off a type of each package rather than spelled, so a
// moved package moves them.
var (
	authPackage  = reflect.TypeFor[authapi.Handler]().PkgPath()
	panelPackage = reflect.TypeFor[adminui.UI]().PkgPath()
)

// OwnAdminRoutes answers the routes under the admin prefix that demand no
// privilege and why, for the end-to-end walk that holds every other admin
// route to a refusal. It is a copy: the gate reads [ownAdminRoutes] alone.
func OwnAdminRoutes() map[AdminRoute]string { return maps.Clone(ownAdminRoutes) }

// panelRoutes is what the gate asks the panel: whether its scope table lists a
// route, which is how the panel binds every route it serves (ADR 0255).
type panelRoutes interface {
	Binds(method, path string) bool
}

// refuseUnscopedAdminRoutes stops an installation in which a route on the
// admin surfaces is not held to a privilege (ADR 0434).
//
// It walks the router once everything is bound, modules, plugins and the
// composition root's own routes alike. A route is judged by the prefix its
// pattern is bound under: a pattern bound anywhere else answers no admin path,
// because gobit owns both prefixes ([ownAdminSurfaces]) and chi matches their
// static segments before any parameter or catch-all at the root, so a
// storefront's page router or a single-page app at the root starts as it is.
//
//   - A route bound under /admin/v1 needs a middleware
//     [corehttp.ScopeDemandedBy] recognizes, which is how the document names the
//     privilege (ADR 0263), unless it is one of [ownAdminRoutes] served by the
//     auth module. chi hands the walk every middleware a route passes through,
//     so a guard on a group, a sub-router or a mounted router counts.
//   - A route bound under the panel's address has to be one the panel bound:
//     listed in its scope table and served by its own code. Its ring proves
//     who is calling and the privilege is the table's, so a route anybody else
//     bound there would answer every operator whatever it demands.
//   - A pattern that carries a percent-encoded byte is refused wherever it is
//     bound: chi matches it against the raw path, while the guard stack's
//     rings read the decoded one, so /admin%2Fv1/… is under no prefix to this
//     walk and under /admin/v1 to the identity ring.
//
// Every refused route is named, sorted, with the handler that serves it: the
// handler's package is the module or plugin that bound it, which the router
// does not otherwise record.
func refuseUnscopedAdminRoutes(r chi.Routes, panel panelRoutes) error {
	var refused []string

	err := chi.Walk(r, func(
		method, pattern string, handler http.Handler, middlewares ...func(http.Handler) http.Handler,
	) error {
		if reason := refusal(AdminRoute{Method: method, Path: pattern}, handler, middlewares, panel); reason != "" {
			refused = append(refused, fmt.Sprintf("%s %s (served by %s) %s",
				method, pattern, handlerName(handler), reason))
		}

		return nil
	})
	if err != nil {
		return errors.Wrap(err, errors.KindInternal, codeAdminRouteUnscoped,
			"the router could not be walked for the admin routes' privileges")
	}
	if len(refused) == 0 {
		return nil
	}

	slices.Sort(refused)

	return errors.Invalid(codeAdminRouteUnscoped,
		"the installation does not start: %d route(s) on the admin surfaces are not held to a "+
			"privilege, so any operator or API key could call them: %s. Wrap a route bound under "+
			"%s in corehttp.RequireScope with the privilege its power needs, leave %s to the "+
			"panel, and spell a pattern without percent-encoding (ADR 0434)",
		len(refused), strings.Join(refused, "; "), adminPrefix, adminui.URLPrefix)
}

// refusal answers why the gate refuses a route, or nothing when it passes.
func refusal(
	route AdminRoute, handler http.Handler, middlewares []func(http.Handler) http.Handler, panel panelRoutes,
) string {
	if strings.Contains(route.Path, "%") {
		return "carries a percent-encoded byte, which chi matches against the raw path " +
			"while the guard stack reads the decoded one"
	}
	if under(route.Path, adminui.URLPrefix) {
		if panel != nil && panel.Binds(route.Method, route.Path) && servedBy(handler, panelPackage) {
			return ""
		}

		return "is under the panel's address and the panel did not bind it"
	}
	if !under(route.Path, adminPrefix) {
		return ""
	}
	if _, own := ownAdminRoutes[route]; own && servedBy(handler, authPackage) {
		return ""
	}
	if demandsPrivilege(middlewares) {
		return ""
	}

	return "demands no privilege"
}

// under reports whether a pattern or a request path is the prefix or lies below
// it, at a segment boundary, the way the guard stack's rings scope themselves.
func under(pattern, prefix string) bool {
	return pattern == prefix || strings.HasPrefix(pattern, prefix+"/")
}

// demandsPrivilege reports whether one of a route's middlewares is a guard
// [corehttp.RequireScope] returned.
func demandsPrivilege(middlewares []func(http.Handler) http.Handler) bool {
	for _, mw := range middlewares {
		if _, ok := corehttp.ScopeDemandedBy(mw); ok {
			return true
		}
	}

	return false
}

// servedBy reports whether a route's handler is a function of the given
// package.
func servedBy(h http.Handler, pkg string) bool {
	return strings.HasPrefix(handlerName(h), pkg+".")
}

// handlerName names the function or type that serves a route.
//
// A route bound with a function carries its full name, package and receiver
// included; anything else is named by its type.
func handlerName(h http.Handler) string {
	if fn, ok := h.(http.HandlerFunc); ok {
		if f := runtime.FuncForPC(reflect.ValueOf(fn).Pointer()); f != nil {
			return f.Name()
		}
	}

	return fmt.Sprintf("%T", h)
}

// ownedSurfaces are the prefixes gobit owns: every path under one that no
// route of the surface's own takes is gobit's to answer (ADR 0434).
var ownedSurfaces = []string{adminPrefix, adminui.URLPrefix}

// routeMethods are the methods gobit binds its own answer for, and asks the
// router about when it says which a path takes: net/http's, and QUERY, which
// chi routes and a catch-all bound for every method takes.
var routeMethods = []string{
	http.MethodGet, http.MethodHead, http.MethodPost, http.MethodPut, http.MethodPatch,
	http.MethodDelete, http.MethodOptions, http.MethodConnect, http.MethodTrace, "QUERY",
}

// ownAdminSurfaces makes gobit the answer to every path on the admin surfaces
// that no route of theirs takes (ADR 0434).
//
// It binds each prefix and the prefix's catch-all on the root, for every
// method nothing else binds there. chi matches a static segment before a
// parameter or a catch-all and does not leave a static subtree that ends in a
// catch-all, so /{category}/{product}, a single-page app at /* and
// /{surface}/v1/… answer no path under a prefix: they reach it only when
// nothing there matches, and now something always does. A router's not-found
// and method-not-allowed handlers are not reached there either, and a router
// mounted under a prefix has its own replaced by gobit's, since every path it
// sees is an admin path.
//
// It refuses what ownership would silently break: a route bound on a prefix's
// catch-all, which gobit's would replace; a router mounted above a prefix that
// carries routes under it, which gobit's would shadow; and a router mounted
// under a prefix that is not chi's own type, whose fallbacks gobit cannot
// replace.
func ownAdminSurfaces(router chi.Router) error {
	root, ok := router.(*chi.Mux)
	if !ok {
		return errors.Internal(codeAdminRouteUnscoped,
			"the router is %T, so the admin surfaces cannot be owned", router)
	}

	owner := &surfaceOwner{root: root, exact: map[string]map[string]bool{}}

	var refused []string
	for _, route := range root.Routes() {
		for _, prefix := range ownedSurfaces {
			// A router mounted there is claimMounted's to name.
			if route.Pattern == prefix+"/*" && route.SubRoutes == nil {
				refused = append(refused, fmt.Sprintf("%s is bound by a module, and the prefix's "+
					"catch-all is gobit's", route.Pattern))
			}
		}
	}
	refused = append(refused, owner.claimMounted(root, "")...)
	if len(refused) > 0 {
		slices.Sort(refused)

		return errors.Invalid(codeAdminRouteUnscoped,
			"the installation does not start: gobit owns %s and everything below them that no "+
				"route of theirs takes, and %d binding(s) would be lost to it or answer there: %s "+
				"(ADR 0434)",
			strings.Join(ownedSurfaces, " and "), len(refused), strings.Join(refused, "; "))
	}

	// Each binding names its prefix as a constant, so the source audit of
	// every state-changing route can read which prefix it lands under.
	owner.exact[adminPrefix] = unboundMethods(root, adminPrefix)
	for method := range owner.exact[adminPrefix] {
		root.Method(method, adminPrefix, http.HandlerFunc(owner.answer))
	}
	root.Handle(adminPrefix+"/*", http.HandlerFunc(owner.answer))

	owner.exact[adminui.URLPrefix] = unboundMethods(root, adminui.URLPrefix)
	for method := range owner.exact[adminui.URLPrefix] {
		root.Method(method, adminui.URLPrefix, http.HandlerFunc(owner.answer))
	}
	root.Handle(adminui.URLPrefix+"/*", http.HandlerFunc(owner.answer))

	return nil
}

// surfaceOwner is gobit's answer on the admin surfaces where no route of
// theirs answers.
type surfaceOwner struct {
	root *chi.Mux
	// exact holds, per prefix, the methods gobit bound on the prefix itself.
	exact map[string]map[string]bool
}

// answer is 405 carrying Allow when a route takes the path with another
// method, the answer chi gives every other path, and otherwise a 404 through
// corehttp.WriteError.
func (o *surfaceOwner) answer(w http.ResponseWriter, r *http.Request) {
	if o.allow(w, r.URL.Path) {
		chiMethodNotAllowed(w, r)

		return
	}

	corehttp.WriteError(r.Context(), w, errors.NotFound(codeRouteNotFound,
		"no route answers %s %s", r.Method, r.URL.Path))
}

// allow writes the Allow header for the methods a route other than gobit's own
// answer takes at the path, and reports whether there was one.
func (o *surfaceOwner) allow(w http.ResponseWriter, path string) bool {
	found := false
	for _, method := range routeMethods {
		rctx := chi.NewRouteContext()
		if !o.root.Match(rctx, method, path) || o.owns(method, rctx.RoutePattern()) {
			continue
		}
		w.Header().Add("Allow", method)
		found = true
	}

	return found
}

// owns reports whether a matched route is gobit's own answer.
func (o *surfaceOwner) owns(method, pattern string) bool {
	for _, prefix := range ownedSurfaces {
		if pattern == prefix+"/*" || (pattern == prefix && o.exact[prefix][method]) {
			return true
		}
	}

	return false
}

// claimMounted replaces the fallbacks of every router mounted under a prefix
// and answers what it cannot take: a router there that is not chi's own type,
// and a router mounted above a prefix that carries a route under it.
func (o *surfaceOwner) claimMounted(mux chi.Routes, base string) []string {
	var refused []string
	for _, route := range mux.Routes() {
		if route.SubRoutes == nil {
			continue
		}
		at := base + strings.TrimSuffix(route.Pattern, "/*")

		var surface string
		for _, prefix := range ownedSurfaces {
			if under(at, prefix) {
				surface = prefix
			}
		}

		switch sub, ok := route.SubRoutes.(*chi.Mux); {
		case surface != "" && at == surface:
			refused = append(refused, fmt.Sprintf("a router is mounted on %s itself, which is gobit's", at))
		case surface != "" && !ok:
			refused = append(refused, fmt.Sprintf("a router of type %T is mounted at %s, under %s, "+
				"and gobit cannot answer its unbound paths", route.SubRoutes, at, surface))
		case surface != "":
			sub.NotFound(o.answer)
			sub.MethodNotAllowed(o.answer)
			refused = append(refused, o.claimMounted(sub, at)...)
		default:
			refused = append(refused, shadowed(route.SubRoutes, at)...)
		}
	}

	return refused
}

// shadowed names the routes under a prefix that a router mounted above it
// carries, which gobit's own answer would shadow.
func shadowed(sub chi.Routes, at string) []string {
	var lost []string
	_ = chi.Walk(sub, func(method, pattern string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		full := at + pattern
		for _, prefix := range ownedSurfaces {
			if under(full, prefix) {
				lost = append(lost, fmt.Sprintf("%s %s is in a router mounted at %s, above %s, "+
					"where gobit's answer would shadow it; mount it at its own path", method, full, at, prefix))
			}
		}

		return nil
	})

	return lost
}

// unboundMethods answers the methods no route binds on exactly the given
// pattern, which are the ones gobit answers there.
func unboundMethods(root chi.Routes, pattern string) map[string]bool {
	free := map[string]bool{}
	for _, method := range routeMethods {
		free[method] = true
	}
	_ = chi.Walk(root, func(method, route string, _ http.Handler, _ ...func(http.Handler) http.Handler) error {
		if route == pattern {
			delete(free, method)
		}

		return nil
	})

	return free
}

// chiMethodNotAllowed is chi's own answer to a method a route does not take, 405
// with no body, as a router nobody configured gives it. It writes no error
// text, so there is nothing for corehttp.WriteError to mask.
var chiMethodNotAllowed = chi.NewRouter().MethodNotAllowedHandler()

// IsOwnedAnswer reports whether a walked route is gobit's own answer on an admin
// surface rather than a route of the surface's, for a walk that holds the
// surface's routes to a refusal.
func IsOwnedAnswer(h http.Handler) bool {
	return strings.HasSuffix(handlerName(h), ".(*surfaceOwner).answer-fm")
}

// endpointRoutes is the router as the served document reads it: gobit's own
// answer on the admin surfaces is not an endpoint, and a client generated from
// the document must not offer a call to it. The bindings are on the root
// router, so the view filters the root's routes and leaves mounted routers as
// they are.
type endpointRoutes struct{ root chi.Routes }

// Middlewares answers the root's middlewares.
func (e endpointRoutes) Middlewares() chi.Middlewares { return e.root.Middlewares() }

// Match asks the root.
func (e endpointRoutes) Match(rctx *chi.Context, method, path string) bool {
	return e.root.Match(rctx, method, path)
}

// Find asks the root.
func (e endpointRoutes) Find(rctx *chi.Context, method, path string) string {
	return e.root.Find(rctx, method, path)
}

// Routes answers the root's routes without gobit's own answer.
func (e endpointRoutes) Routes() []chi.Route {
	var out []chi.Route
	for _, route := range e.root.Routes() {
		handlers := make(map[string]http.Handler, len(route.Handlers))
		for method, h := range route.Handlers {
			if !IsOwnedAnswer(h) {
				handlers[method] = h
			}
		}
		if len(handlers) == 0 && route.SubRoutes == nil {
			continue
		}
		route.Handlers = handlers
		out = append(out, route)
	}

	return out
}
