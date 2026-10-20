package http

import (
	"fmt"
	"net"
	"net/http"
	"slices"
	"strconv"
	"strings"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// RouteQuota is a limit on how often one client may call a set of routes,
// counted together (ADR 0452).
//
// It stands beside the whole-API limit rather than in its place: that one
// bounds what a client costs the installation, a route quota bounds what a
// client may try at one door. Signing in is the case it was built for: the
// account lock bounds the guesses at one account, and nothing bounded one
// client spreading its guesses over many.
type RouteQuota struct {
	// Name keeps the quota's counts apart from every other quota's; it is
	// part of the counters' key, so [CheckRouteQuotaName] holds it to the
	// characters a key prefix takes.
	Name string
	// Routes are the routes the quota counts, each its method and its path
	// pattern as the router binds it and /openapi.json prints it:
	// "POST /admin/v1/auth/login", "POST /store/v1/orders/{id}/returns".
	// [ParseQuotaRoute] says which spellings are taken.
	Routes []string
	// Limiter counts the quota's requests by client key.
	Limiter RateLimiter
}

// CodeRouteQuotaInvalid refuses a route quota the installation cannot hold:
// a name or a route [CheckRouteQuotaName] or [ParseQuotaRoute] refuses.
const CodeRouteQuotaInvalid = "route_quota_invalid"

// quotaMethods are the methods a quota may name: the ones the API surfaces
// bind. A route answering HEAD or OPTIONS costs nothing a quota is for.
var quotaMethods = []string{
	http.MethodGet, http.MethodPost, http.MethodPut, http.MethodPatch, http.MethodDelete,
}

// ParseQuotaRoute splits a quota's route into its method and its pattern, or
// says why it cannot.
//
// The route is "METHOD /path", one space between them, the method upper case.
// A segment in braces is a parameter and matches any one non-empty segment.
// A catch-all is refused, since it is not one route, and so is a
// percent-encoded byte, which the router matches against the raw path while
// the quota reads the decoded one (ADR 0434 refuses such a pattern for the
// same reason).
func ParseQuotaRoute(route string) (method, pattern string, err error) {
	method, pattern, ok := strings.Cut(route, " ")
	switch {
	case !ok:
		return "", "", fmt.Errorf("%q is not \"METHOD /path\"", route)
	case !slices.Contains(quotaMethods, method):
		return "", "", fmt.Errorf("%q names the method %q; a quota names one of %s",
			route, method, strings.Join(quotaMethods, ", "))
	case !strings.HasPrefix(pattern, "/"):
		return "", "", fmt.Errorf("%q has a path that does not begin with /", route)
	case strings.ContainsAny(pattern, "* %?#"):
		return "", "", fmt.Errorf("%q carries a catch-all, a space, a query or a percent-encoded byte", route)
	}

	return method, pattern, nil
}

// CheckRouteQuotaName says why a name cannot be a quota's, or nil when it can:
// one or more ASCII letters, digits, '-', '_' or '.', the characters a shared
// backend's key prefix takes.
func CheckRouteQuotaName(name string) error {
	if name == "" {
		return fmt.Errorf("a route quota needs a name")
	}
	for _, c := range []byte(name) {
		switch {
		case c >= 'a' && c <= 'z', c >= 'A' && c <= 'Z', c >= '0' && c <= '9', c == '-', c == '_', c == '.':
		default:
			return fmt.Errorf("the route quota name %q carries %q; only ASCII letters, digits, '-', '_' and '.' are taken",
				name, c)
		}
	}

	return nil
}

// quotaRoute is one route of a quota, split into its segments.
type quotaRoute struct {
	method   string
	segments []string
	quota    int
}

// matches reports whether the request's method and decoded path are the
// route's.
func (q quotaRoute) matches(method string, segments []string) bool {
	if method != q.method || len(segments) != len(q.segments) {
		return false
	}
	for i, seg := range q.segments {
		if strings.HasPrefix(seg, "{") && strings.HasSuffix(seg, "}") {
			if segments[i] == "" {
				return false
			}

			continue
		}
		if seg != segments[i] {
			return false
		}
	}

	return true
}

// RouteQuotas spends, before the handler runs, one unit of every quota that
// names the route a request is for, keyed by the client, and answers 429
// [CodeRateLimited] with Retry-After when one of them is spent.
//
// It matches the decoded path, as [Scoped] does, so an encoded request either
// meets the quota or is a route the router does not find. A quota is spent at
// most once per request, whatever number of its routes match; quotas are
// spent in their order, and the first that refuses answers. A limiter that
// cannot answer lets the request through, as [RateLimit] does (ADR 0007), and
// an empty client key is no client to count.
//
// A route or name the parsers refuse is a programming error the composition
// root reports at startup; here it panics rather than leave a quota that
// counts nothing.
func RouteQuotas(quotas []RouteQuota, key KeyFunc) func(http.Handler) http.Handler {
	if key == nil {
		key = ClientIPKey
	}
	var routes []quotaRoute
	for i, q := range quotas {
		if q.Limiter == nil {
			continue
		}
		if err := CheckRouteQuotaName(q.Name); err != nil {
			panic(err)
		}
		for _, route := range q.Routes {
			method, pattern, err := ParseQuotaRoute(route)
			if err != nil {
				panic(err)
			}
			routes = append(routes, quotaRoute{method: method, segments: strings.Split(pattern, "/"), quota: i})
		}
	}

	return func(next http.Handler) http.Handler {
		if len(routes) == 0 {
			return next
		}

		return http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
			segments := strings.Split(r.URL.Path, "/")
			spent := make([]bool, len(quotas))
			client := ""
			for _, route := range routes {
				if spent[route.quota] || !route.matches(r.Method, segments) {
					continue
				}
				spent[route.quota] = true
				if client == "" {
					if client = quotaClient(key(r)); client == "" {
						break
					}
				}
				d, err := quotas[route.quota].Limiter.Allow(r.Context(), client)
				if err != nil {
					LoggerFromContext(r.Context()).WarnContext(r.Context(),
						"a route quota could not be counted, letting the request through",
						"quota", quotas[route.quota].Name, "error", err)

					continue
				}
				if !d.Allowed {
					w.Header().Set("Retry-After", strconv.Itoa(retryAfterSeconds(d.RetryAfter)))
					WriteError(r.Context(), w, coreerrors.TooManyRequests(CodeRateLimited,
						"too many requests to this route, please try again later"))

					return
				}
			}
			next.ServeHTTP(w, r)
		})
	}
}

// ipv6ClientBits is how much of an IPv6 address names one client to a quota:
// the /64 a host, a home line or a server is given whole.
const ipv6ClientBits = 64

// quotaClient is the client a quota counts for the key: an IPv6 address is
// counted by its /64, any other key as it is.
//
// A quota bounds what one client may try at one door (ADR 0452), and a host
// holding a /64 can send every request from an address of its own; counted
// per address, it would meet a fresh quota on each. An IPv4 address, or a key
// that is not an address at all, is one client already.
func quotaClient(key string) string {
	ip := net.ParseIP(key)
	if ip == nil || ip.To4() != nil {
		return key
	}

	return ip.Mask(net.CIDRMask(ipv6ClientBits, 8*net.IPv6len)).String() + "/64"
}
