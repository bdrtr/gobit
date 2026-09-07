package adminui

import (
	"net/http"
	"net/url"
	"path"
	"strings"
)

// nextField is the form field carrying the page the user was trying to reach.
const nextField = "next"

// requestedPath is the panel page this request was trying to reach, or "".
//
// # Why only GET
//
// A return-to is a promise to REPLAY the navigation after signing in, and the
// only navigation that can be replayed by putting a URL in a Location header is
// a GET. Sending an operator back to a POST target after a login would show
// them a page they did not ask for, or re-run nothing at all; sending them to
// the panel's front page is the honest answer for every other method.
func requestedPath(r *http.Request) string {
	if r.Method != http.MethodGet {
		return ""
	}
	target := r.URL.RequestURI()
	if !safeReturnTarget(target) {
		return ""
	}
	return target
}

// safeReturnTarget reports whether target may be written into a Location header.
//
// # This is an open-redirect guard and it is written as an allow-list
//
// The value round-trips through a hidden form field, so it reaches
// [UI.submitLogin] as ATTACKER-INFLUENCEABLE input even though the guard itself
// only ever produces panel paths. [UI.CheckOrigin] already refuses a
// cross-site POST, which makes this the second layer rather than the first —
// and a second layer is exactly what the panel's own CSRF note says it wants
// for anything that depends on the browser behaving.
//
// # It parses, and the first draft did not
//
// The first version compared raw strings: reject "//", then require the
// URLPrefix prefix. Mutation testing found that the "//" branch was DEAD — a
// protocol-relative URL cannot also start with "/admin/ui", so the prefix check
// had already refused it and the branch could be deleted without a single test
// noticing. Looking for a case where it DID matter turned up the real hole in
// the other direction: "/admin/ui/../../admin/v1/orders" carries the prefix as
// a raw string, so that draft ACCEPTED it, and the browser resolves it to a
// path outside the panel entirely.
//
// So the rule is applied to the RESOLVED path, and the three checks below are
// each load-bearing — removing any one of them lets a specific target through:
//
//   - A scheme or a host means another origin, and a URL may carry one while
//     its path still looks local: "https://evil.example/admin/ui/orders" has
//     exactly the path this function is looking for.
//   - [path.Clean] resolves "..", which is how a target keeps the prefix and
//     leaves the tree anyway.
//   - A backslash survives Clean and is folded to a forward slash by some
//     browsers, so "/admin/ui/x\\..\\..\\evil" is the same escape spelled in a
//     way Clean does not see.
//
// The login page and the stylesheet are refused last: the first would loop
// (sign in, land on the form, sign in), and the second is not a page a person
// is on.
func safeReturnTarget(target string) bool {
	if target == "" || strings.Contains(target, `\`) {
		return false
	}

	parsed, err := url.Parse(target)
	if err != nil || parsed.Scheme != "" || parsed.Host != "" || parsed.Opaque != "" {
		return false
	}

	clean := path.Clean(parsed.Path)
	if clean != URLPrefix && !strings.HasPrefix(clean, URLPrefix+"/") {
		return false
	}

	return clean != LoginPath && clean != StylesheetPath
}

// returnTargetOf reads the submitted return-to and falls back to the panel root.
//
// The fallback is not an error path: a login reached by typing the URL carries
// no field at all, and the panel's front page is where that person meant to go.
func returnTargetOf(r *http.Request) string {
	target := r.PostFormValue(nextField)
	if !safeReturnTarget(target) {
		return URLPrefix
	}
	return target
}
