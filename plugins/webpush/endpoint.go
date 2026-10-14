package webpush

import (
	"context"
	"errors"
	"fmt"
	"net"
	"net/http"
	"net/netip"
	"net/url"
	"slices"
	"strings"
	"sync"
	"time"

	coreerrors "github.com/bdrtr/gobit/core/errors"
)

// Where the sender may send (ADR 0440).
//
// The endpoint a device is stored with is a URL its storefront request names,
// and that request needs no secret: the publishable key is printed into every
// page. Before ADR 0440 any absolute http(s) URL was stored, and an operator's
// broadcast or a customer's order made gobit POST to it — to a loopback
// service, to a cloud's metadata address, to anything on the shop's private
// network — and told the requester how it went, since a 404 or 410 deletes the
// device and a broadcast answers with counts (D292).
//
// A browser does not choose its push service; its vendor does. So an endpoint
// is admitted when it is an https URL at one of the vendors' push services
// below, or at an origin the operator lists in [settingPushServices]. The rule
// is asked when a device subscribes and again before each push, so a row
// stored before it, or under a list the operator has since shortened, is
// removed rather than sent to. A built-in host is also reached only at a
// public address, checked when the connection is made, and the sender follows
// no redirect.

// settingPushServices names the push services the operator adds to the
// built-in ones.
const settingPushServices = "WEBPUSH_PUSH_SERVICES"

// The schemes an endpoint or a listed origin is written with.
const (
	schemeHTTPS = "https"
	schemeHTTP  = "http"
)

// rangeDocumentation is the reason an address set aside for examples is
// refused.
const rangeDocumentation = "documentation"

// codeEndpointRefused is the code of a subscription whose endpoint is not at a
// push service the installation sends to.
const codeEndpointRefused = "webpush_endpoint_refused"

// builtInPushServices are the hosts the four browsers' push services answer
// at: Chrome's (and the browsers built on Chromium that keep its push
// service), Firefox's, Safari's and Edge's.
//
// They are written as the vendors publish them for a server's allow-list:
// Google's two hosts by name, and Mozilla's, Apple's and Microsoft's as every
// host under a domain, since each hands out endpoints on more than one.
var builtInPushServices = []hostPattern{
	{name: "fcm.googleapis.com"},
	{name: "android.googleapis.com"},
	{suffix: ".push.services.mozilla.com"},
	{suffix: ".push.apple.com"},
	{suffix: ".notify.windows.com"},
}

// builtInNames is the list as a sentence names it, for a refusal.
func builtInNames() string {
	names := make([]string, 0, len(builtInPushServices))
	for _, p := range builtInPushServices {
		if p.name != "" {
			names = append(names, p.name)

			continue
		}
		names = append(names, "*"+p.suffix)
	}

	return strings.Join(names, ", ")
}

// hostPattern is one built-in push service: a host name, or every host under
// a domain.
type hostPattern struct {
	name   string
	suffix string
}

// matches reports whether host, lower-cased, is the pattern's.
//
// A suffix needs at least one label in front of it, and no empty one: the
// domain itself hands out no endpoint, and "a..push.apple.com" is not a host.
func (p hostPattern) matches(host string) bool {
	if p.name != "" {
		return host == p.name
	}

	labels, found := strings.CutSuffix(host, p.suffix)

	return found && !slices.Contains(strings.Split(labels, "."), "")
}

// origin is one push service the operator listed: a scheme, a lower-cased
// host and a port, the default one written out.
type origin struct {
	scheme string
	host   string
	port   string
}

// addr is the origin as the transport dials it.
func (o origin) addr() string { return net.JoinHostPort(o.host, o.port) }

// pushServices is the rule an endpoint is held to.
type pushServices struct {
	// listed are the operator's origins, matched exactly. An origin the
	// operator names is reached wherever its name resolves: a push service
	// on the shop's own network is the reason to list one.
	listed []origin
}

// parsePushServices reads the operator's list.
//
// Each entry is an origin, https://host or https://host:port, and nothing
// else: no path, no credentials, no wildcard. Plain http is accepted for a
// loopback host only, the rule webhook-out holds a receiver's address to
// (ADR 0435), so a development push service can listen without a
// certificate. An entry that fails stops startup, since a list read wrongly is
// a push service nobody's device can subscribe at, or one more than meant.
func parsePushServices(raw string) (pushServices, error) {
	var services pushServices
	for entry := range strings.SplitSeq(raw, ",") {
		entry = strings.TrimSpace(entry)
		if entry == "" {
			continue
		}

		listed, err := parseOrigin(entry)
		if err != nil {
			return pushServices{}, err
		}
		services.listed = append(services.listed, listed)
	}

	return services, nil
}

// parseOrigin reads one entry of the operator's list.
func parseOrigin(entry string) (origin, error) {
	refuse := func(why string) (origin, error) {
		return origin{}, coreerrors.Invalid(codeInvalidSetting,
			"%s: %q is not a push service's origin: %s", settingPushServices, entry, why)
	}

	u, err := url.Parse(entry)
	switch {
	case err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "":
		return refuse("write it as https://host or https://host:port")
	case u.Scheme != schemeHTTPS && u.Scheme != schemeHTTP:
		return refuse("it has to be https")
	case u.User != nil:
		return refuse("it carries credentials")
	case u.Path != "" && u.Path != "/", u.RawQuery != "", u.Fragment != "":
		return refuse("an origin has no path, query or fragment")
	case strings.Contains(u.Host, "*"):
		return refuse("a wildcard would admit hosts nobody listed")
	}

	host := strings.ToLower(u.Hostname())
	if u.Scheme == schemeHTTP && !isLoopbackHost(host) {
		return refuse("plain http is accepted for a loopback host only")
	}

	return origin{scheme: u.Scheme, host: host, port: portOf(u.Scheme, u.Port())}, nil
}

// isLoopbackHost reports whether host is localhost or a loopback address.
func isLoopbackHost(host string) bool {
	if host == "localhost" {
		return true
	}
	addr, err := netip.ParseAddr(host)

	return err == nil && addr.Unmap().IsLoopback()
}

// portOf writes out a URL's port, the scheme's default when it names none.
func portOf(scheme, port string) string {
	if port != "" {
		return port
	}
	if scheme == schemeHTTP {
		return "80"
	}

	return "443"
}

// refusal says why a device may not be stored with endpoint, or "" when it
// may. The reason names the host and never the rest of the URL, which is the
// device's capability.
func (s pushServices) refusal(endpoint string) string {
	u, err := url.Parse(endpoint)
	if err != nil || !u.IsAbs() || u.Host == "" || u.Opaque != "" {
		return "it is not an absolute URL"
	}

	host := strings.ToLower(u.Hostname())
	if u.User != nil {
		return fmt.Sprintf("the address at %q carries credentials", host)
	}

	for _, listed := range s.listed {
		if listed.scheme == u.Scheme && listed.host == host && listed.port == portOf(u.Scheme, u.Port()) {
			return ""
		}
	}

	switch {
	case u.Scheme != schemeHTTPS:
		return fmt.Sprintf("the address at %q is not https", host)
	case u.Port() != "" && u.Port() != "443":
		return fmt.Sprintf("the address at %q names port %s", host, u.Port())
	}

	for _, p := range builtInPushServices {
		if p.matches(host) {
			return ""
		}
	}

	return fmt.Sprintf("%q is not a push service this installation sends to; the browsers' own "+
		"are %s, and %s lists more", host, builtInNames(), settingPushServices)
}

// trustsAddr reports whether a connection to addr goes to an origin the
// operator listed, which is reached at whatever address its name resolves to.
func (s pushServices) trustsAddr(addr string) bool {
	addr = strings.ToLower(addr)
	for _, listed := range s.listed {
		if listed.addr() == addr {
			return true
		}
	}

	return false
}

// specialPurpose are the ranges, beyond the ones netip names, that reach no
// public host: each is reserved, shared inside a provider's network, or
// carries an IPv4 address a translator would reach on the sender's behalf.
var specialPurpose = []struct {
	prefix netip.Prefix
	why    string
}{
	{netip.MustParsePrefix("0.0.0.0/8"), "this-network"},                            // RFC 1122
	{netip.MustParsePrefix("100.64.0.0/10"), "shared, inside a provider's network"}, // RFC 6598
	{netip.MustParsePrefix("192.0.0.0/24"), "protocol-assignment"},                  // RFC 6890
	{netip.MustParsePrefix("192.0.2.0/24"), rangeDocumentation},                     // RFC 5737
	{netip.MustParsePrefix("198.18.0.0/15"), "benchmarking"},                        // RFC 2544
	{netip.MustParsePrefix("198.51.100.0/24"), rangeDocumentation},                  // RFC 5737
	{netip.MustParsePrefix("203.0.113.0/24"), rangeDocumentation},                   // RFC 5737
	{netip.MustParsePrefix("240.0.0.0/4"), "reserved"},                              // RFC 1112
	{netip.MustParsePrefix("::/96"), "IPv4-compatible"},                             // RFC 4291
	{netip.MustParsePrefix("::ffff:0:0:0/96"), "IPv4-translated"},                   // RFC 2765
	{netip.MustParsePrefix("64:ff9b::/96"), "NAT64, carrying an IPv4 address"},      // RFC 6052
	{netip.MustParsePrefix("64:ff9b:1::/48"), "NAT64, carrying an IPv4 address"},    // RFC 8215
	{netip.MustParsePrefix("100::/64"), "discard-only"},                             // RFC 6666
	{netip.MustParsePrefix("2001::/23"), "protocol-assignment"},                     // RFC 6890
	{netip.MustParsePrefix("2001:db8::/32"), rangeDocumentation},                    // RFC 3849
	{netip.MustParsePrefix("2002::/16"), "6to4, carrying an IPv4 address"},          // RFC 3056
	{netip.MustParsePrefix("3fff::/20"), rangeDocumentation},                        // RFC 9637
	{netip.MustParsePrefix("fec0::/10"), "site-local"},                              // RFC 3879
}

// forbiddenAddress says why the sender does not connect to addr, or "" when
// it does.
//
// An IPv4 address written as IPv6 (::ffff:a.b.c.d) is judged as the IPv4
// address it is: a connection to it reaches the same host.
func forbiddenAddress(addr netip.Addr) string {
	addr = addr.Unmap()

	switch {
	case !addr.IsValid():
		return "not an address"
	case addr.IsUnspecified():
		return "unspecified"
	case addr.IsLoopback():
		return "loopback"
	case addr.IsPrivate():
		return "private"
	case addr.IsLinkLocalUnicast():
		return "link-local"
	case addr.IsMulticast():
		return "multicast"
	case !addr.IsGlobalUnicast():
		return "not a unicast"
	}

	for _, r := range specialPurpose {
		if r.prefix.Contains(addr) {
			return r.why
		}
	}

	return ""
}

// errAddressRefused marks a connection the guard would not make.
var errAddressRefused = errors.New("the sender does not connect to that address")

// dialGuard makes the sender's connections, and makes none to an address
// [forbiddenAddress] refuses.
//
// The name is resolved HERE and the connection made to the address that was
// checked, so a name that answers a public address to one lookup and a
// private one to the next cannot slip between the check and the dial. Every
// address the name resolves to has to pass: a name that also points inside the
// network is not a push service's.
type dialGuard struct {
	// lookup resolves a host; net.DefaultResolver.LookupNetIP in the sender.
	lookup func(ctx context.Context, network, host string) ([]netip.Addr, error)
	// dial connects to an address.
	dial func(ctx context.Context, network, addr string) (net.Conn, error)
	// trusted reports a host:port the operator listed.
	trusted func(addr string) bool
	// proxy is the environment's proxy for a request.
	proxy func(*http.Request) (*url.URL, error)

	// proxies are the proxies the transport was told to use, as it dials
	// them. A connection to one is the operator's configuration rather than
	// an endpoint's, and the proxy, not this process, resolves the push
	// service behind it.
	proxies sync.Map
}

// proxyFor is the transport's Proxy: the environment's, remembered so the
// guard lets the transport reach it.
func (g *dialGuard) proxyFor(req *http.Request) (*url.URL, error) {
	proxy, err := g.proxy(req)
	if proxy != nil {
		port := proxy.Port()
		if port == "" {
			switch proxy.Scheme {
			case schemeHTTPS:
				port = "443"
			case "socks5", "socks5h":
				port = "1080"
			default:
				port = "80"
			}
		}
		g.proxies.Store(strings.ToLower(net.JoinHostPort(proxy.Hostname(), port)), true)
	}

	return proxy, err
}

// dialContext is the transport's DialContext.
func (g *dialGuard) dialContext(ctx context.Context, network, addr string) (net.Conn, error) {
	if _, isProxy := g.proxies.Load(strings.ToLower(addr)); isProxy || g.trusted(addr) {
		return g.dial(ctx, network, addr)
	}

	host, port, err := net.SplitHostPort(addr)
	if err != nil {
		return nil, err
	}

	resolved, err := g.lookup(ctx, lookupNetwork(network), host)
	if err != nil {
		return nil, err
	}
	if len(resolved) == 0 {
		return nil, fmt.Errorf("%q resolves to no address", host)
	}
	for _, a := range resolved {
		if why := forbiddenAddress(a); why != "" {
			return nil, fmt.Errorf("%w: %q resolves to %s, a %s address", errAddressRefused, host, a, why)
		}
	}

	var firstErr error
	for _, a := range resolved {
		conn, err := g.dial(ctx, network, net.JoinHostPort(a.Unmap().String(), port))
		if err == nil {
			return conn, nil
		}
		if firstErr == nil {
			firstErr = err
		}
	}

	return nil, firstErr
}

// lookupNetwork is the resolver's network for a dial's.
func lookupNetwork(network string) string {
	switch network {
	case "tcp4":
		return "ip4"
	case "tcp6":
		return "ip6"
	}

	return "ip"
}

// refuseRedirect is the client's CheckRedirect: a redirect is handed back as
// the answer it is, and [sender.send] counts it as a failure. A push service
// answers where the browser was told it would; a redirect would send the
// encrypted message and its signed token to an address no rule here was asked
// about.
func refuseRedirect(*http.Request, []*http.Request) error { return http.ErrUseLastResponse }

// newPushClient builds the sender's client: its own transport, which dials
// through a [dialGuard], and no redirect followed.
//
// The transport is built rather than cloned from http.DefaultTransport, which
// a program embedding gobit may have replaced with a type of its own. Its
// settings are the default transport's.
func newPushClient(services pushServices) *http.Client {
	return newGuardedClient(&dialGuard{
		lookup:  net.DefaultResolver.LookupNetIP,
		dial:    (&net.Dialer{Timeout: 30 * time.Second, KeepAlive: 30 * time.Second}).DialContext,
		trusted: services.trustsAddr,
		proxy:   http.ProxyFromEnvironment,
	})
}

// newGuardedClient builds a client over the guard.
func newGuardedClient(guard *dialGuard) *http.Client {
	return &http.Client{
		Transport: &http.Transport{
			Proxy:                 guard.proxyFor,
			DialContext:           guard.dialContext,
			ForceAttemptHTTP2:     true,
			MaxIdleConns:          100,
			IdleConnTimeout:       90 * time.Second,
			TLSHandshakeTimeout:   10 * time.Second,
			ExpectContinueTimeout: time.Second,
		},
		CheckRedirect: refuseRedirect,
	}
}
