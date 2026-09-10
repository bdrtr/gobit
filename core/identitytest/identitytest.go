// Package identitytest is the compliance suite a customer identity passes.
//
// # Who it is for
//
// Anybody writing a [corehttp.Identity] — the one interface gobit requires and
// does not implement (ADR 0008, ADR 0043). Since ADR 0125 every storefront
// route naming a customer refuses until one is bound, so binding one is the
// only way to serve those routes, and what gets bound is code gobit never sees.
//
// Call it from an ordinary test:
//
//	func TestCompliance(t *testing.T) {
//		identitytest.Contract(t, mySessions)
//	}
//
// # What it checks and what it CANNOT
//
// It checks the rules that hold with no session store, no key and no upstream:
// that an identifier is never invented, never taken from the request, never
// returned beside an error, and that reading it leaves the request usable.
//
// It does NOT check that a verifier's proof is sound. Nothing here can open a
// signature, and a suite that pretended to would be worse than none — a green
// run would read as "this session scheme is secure". What it can do is refuse
// the shapes that are wrong WITHOUT looking at any crypto, and the commonest of
// those is the one a first implementation reaches for: read a header, hand it
// back.
//
// # Why an implementation may DECLARE that it trusts a header
//
// [corehttp.Identity]'s own contract names "a header written by an upstream
// proxy" as a source a verifier may read, and it is right: behind a gateway
// that authenticates and STRIPS, that header is proof. This suite cannot see
// the gateway, so it cannot tell that verifier from the naive one by looking at
// the request.
//
// It does not have to. An implementation that reads such a header says so, by
// implementing [UpstreamTrust], and the suite then probes every OTHER surface
// and leaves that header alone. Declaring is cheap and forgetting is loud,
// which is the point: an author who writes the declaration has looked at the
// question, and one who has not gets a failure naming it.
//
// # Why it takes an interface rather than *testing.T
//
// core/providertest's reason, and this package is published for the same
// audience: a package that imports `testing` puts the testing flags into every
// binary that links it. [T] is the two methods the suite needs, and *testing.T
// satisfies it without an adapter. There is no assertion library here either —
// a published package's dependencies are the embedder's.
package identitytest

import (
	"bytes"
	"context"
	"io"
	"net/http"
	"net/http/httptest"
	"strings"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// T is the part of *testing.T this suite uses.
//
// Errorf rather than Fatalf: a run should report every rule an implementation
// breaks. A verifier that echoes the request usually breaks more than one, and
// stopping at the first turns one run into three.
type T interface {
	// Helper marks the calling function as a test helper.
	Helper()
	// Errorf reports a failure and continues.
	Errorf(format string, args ...any)
}

// UpstreamTrust is implemented by a verifier whose proof arrives in a request
// header that the DEPLOYMENT is responsible for stripping.
//
// It is not a capability and grants nothing. It is a declaration, and its only
// reader is this suite: the header it names is left out of the spoofing probe,
// because a value the edge strips is not a value a caller controls.
//
// An implementation reading more than one such header names the one the suite
// must not spoof; the others are still probed, and a verifier that accepts
// those is accepting a claim nobody stripped.
type UpstreamTrust interface {
	// TrustedUpstreamHeader returns the header name the deployment strips.
	TrustedUpstreamHeader() string
}

// probeCustomerID is the identifier the spoofing probe plants.
//
// It looks like an identifier this framework could have minted, because a
// verifier that rejects it for its SHAPE would pass the probe while still
// trusting whatever the request said.
const probeCustomerID = "cust_06G8SPOOFEDBYTHESUITE00"

// probeBody is a body carrying the probe the way a storefront request would.
const probeBody = `{"customer_id":"` + probeCustomerID + `","country_code":"TR"}`

// spoofedCookies is the Cookie header the probe sends.
//
// Three names, because a verifier reading a cookie reads the one IT chose and
// the probe cannot know which: two obvious ones and the name this repository's
// own reference fixture uses. A signing verifier rejects all three for the same
// reason — nothing here is signed.
const spoofedCookies = "session=" + probeCustomerID +
	"; customer_id=" + probeCustomerID +
	"; gobit_session=" + probeCustomerID

// spoofedHeaders are the request headers the probe plants the identifier in.
//
// It is a NAMED list rather than a derived population, because a request has no
// schema that says where a claim may ride. What the list has to be is honest
// about itself: these are the places a first implementation reaches for, and a
// verifier reading a header not on it is not caught by this check. The bare
// request check next door is what catches an identifier that came from nowhere
// at all.
var spoofedHeaders = []string{
	"Authorization",
	"Cookie",
	"X-Customer-Id",
	"X-Customer",
	"X-User-Id",
	"X-User",
	"X-Auth-User",
	"X-Authenticated-User",
	"X-Forwarded-User",
	"X-Gobit-Customer",
	"Customer-Id",
}

// Contract runs every rule this suite can hold an implementation to.
//
// A failure names the rule and what the implementation answered. It never
// stops at the first: an implementation is usually wrong in more than one way
// and the second failure is the cheaper one to learn now.
func Contract(t T, identity corehttp.Identity) {
	t.Helper()

	if identity == nil {
		t.Errorf("identitytest: the identity is nil, so there is nothing to check")

		return
	}

	anEmptyRequestProvesNobody(t, identity)
	aRequestCannotNameItsOwnCustomer(t, identity)
	anErrorComesAlone(t, identity)
	theRequestSurvivesBeingRead(t, identity)
}

// anEmptyRequestProvesNobody refuses an identifier that came from nowhere.
//
// A request with no headers, no cookies and no body carries no proof of
// anything. A verifier answering it with an identifier has invented one, and
// every storefront route naming a customer would then serve that customer to
// an anonymous caller.
func anEmptyRequestProvesNobody(t T, identity corehttp.Identity) {
	t.Helper()

	id, err := identity.CustomerID(bareRequest())
	if err != nil {
		return
	}
	if id != "" {
		t.Errorf("identitytest: a request carrying no credentials at all proved customer %q.\n"+
			"An identifier that came from nowhere is invented, and every storefront route "+
			"naming a customer would serve that person to an anonymous caller.", id)
	}
}

// aRequestCannotNameItsOwnCustomer is the rule the naive implementation breaks.
//
// The probe plants one identifier in every header on [spoofedHeaders], in the
// query string, in a cookie and in a JSON body, and requires that the verifier
// does not hand that identifier back. Returning an error is a pass; returning a
// DIFFERENT identifier is a pass too, because a verifier reading a real session
// out of a cookie the probe happened to overwrite may still find one.
//
// A declared upstream header ([UpstreamTrust]) is left out of the probe: a
// value the edge strips is not a value a caller controls.
func aRequestCannotNameItsOwnCustomer(t T, identity corehttp.Identity) {
	t.Helper()

	trusted := ""
	if declaring, ok := identity.(UpstreamTrust); ok {
		trusted = http.CanonicalHeaderKey(strings.TrimSpace(declaring.TrustedUpstreamHeader()))
	}

	id, err := identity.CustomerID(spoofedRequest(trusted))
	if err != nil {
		return
	}
	if id == probeCustomerID {
		t.Errorf("identitytest: a request that NAMED customer %q was believed.\n"+
			"The claim was planted in the headers, the query string, a cookie and the body, "+
			"and the verifier handed it back — so any caller can act as any customer whose "+
			"identifier they know, and an identifier travels in every order response.\n"+
			"If the proof really does arrive in a header a gateway strips, implement "+
			"identitytest.UpstreamTrust and name it; the suite will leave that one alone.",
			id)
	}
}

// anErrorComesAlone keeps a disowned identifier out of a careless caller.
//
// gobit's own caller reads the error first, so this costs it nothing. It is
// held all the same: an identifier returned beside an error is a value the
// verifier itself refused to stand behind, and the next caller is not
// necessarily gobit.
func anErrorComesAlone(t T, identity corehttp.Identity) {
	t.Helper()

	for name, r := range map[string]*http.Request{
		"a bare request":    bareRequest(),
		"a spoofed request": spoofedRequest(""),
	} {
		id, err := identity.CustomerID(r)
		if err != nil && id != "" {
			t.Errorf("identitytest: %s was refused with an error AND answered with %q.\n"+
				"An identifier returned beside an error is one the verifier will not stand "+
				"behind; a caller reading the value first acts on it anyway.", name, id)
		}
	}
}

// theRequestSurvivesBeingRead holds the sentence [corehttp.Identity] already
// writes: the request must not be modified.
//
// The one way an implementation breaks it by accident is by reading the BODY —
// looking for a session field, say. The body is a stream and reading it to the
// end leaves nothing for the handler, so every POST on the storefront would
// then fail to parse, in a way whose cause is nowhere near the message.
func theRequestSurvivesBeingRead(t T, identity corehttp.Identity) {
	t.Helper()

	r := spoofedRequest("")
	_, _ = identity.CustomerID(r)

	rest, err := io.ReadAll(r.Body)
	if err != nil {
		t.Errorf("identitytest: the request body could not be read after the verifier ran: %v.\n"+
			"The handler reads the same body afterwards.", err)

		return
	}
	if string(rest) != probeBody {
		t.Errorf("identitytest: the verifier consumed the request body.\n"+
			"%d of %d bytes were left. The handler parses that body after the verifier "+
			"runs, so every storefront POST would fail on a body that was there.",
			len(rest), len(probeBody))
	}
}

// bareRequest is a request carrying nothing at all.
func bareRequest() *http.Request {
	return httptest.NewRequestWithContext(context.Background(),
		http.MethodGet, "/store/v1/customers/cust_1", http.NoBody)
}

// spoofedRequest is a request that names [probeCustomerID] everywhere a claim
// can ride, except the header the implementation declared.
//
// The cookies are written as a raw Cookie HEADER rather than built with
// [http.Request.AddCookie], and that is faithful rather than lazy: what a caller
// sends is a header, and the attributes AddCookie would carry — Secure,
// HttpOnly, SameSite — are what a SERVER sets on a cookie it issues. Nobody
// spoofing a session sets HttpOnly on it.
func spoofedRequest(trustedHeader string) *http.Request {
	r := httptest.NewRequestWithContext(context.Background(), http.MethodPost,
		"/store/v1/carts?customer_id="+probeCustomerID+"&user_id="+probeCustomerID,
		bytes.NewBufferString(probeBody))
	r.Header.Set("Content-Type", "application/json")

	for _, name := range spoofedHeaders {
		if http.CanonicalHeaderKey(name) == trustedHeader {
			continue
		}
		if http.CanonicalHeaderKey(name) == "Cookie" {
			r.Header.Set(name, spoofedCookies)

			continue
		}
		r.Header.Set(name, probeCustomerID)
	}

	return r
}
