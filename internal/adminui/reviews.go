package adminui

import (
	"net/http"

	corehttp "github.com/bdrtr/gobit/core/http"
)

// reviewsLabel is what the section is called on screen.
//
// It is a constant for the reason [ordersLabel] is: the menu, the page title
// and the heading all print it, and three copies of a word are three places to
// rename it in.
const reviewsLabel = "Reviews"

// The keys the review screen's shell fills in.
const (
	apiPrefixKey     = "APIPrefix"
	reviewsScriptKey = "ReviewsScript"
)

// showReviews renders the review screen's SHELL.
//
// # It renders nothing an operator reads
//
// Every other handler in this package fetches records and hands them to a
// template. This one hands the template two paths and stops, because the screen
// is a client of `/admin/v1` (ADR 0030) and the data arrives in the browser.
// The panel's own session reaches that API through [UI.APISession].
//
// # Why the API prefix is passed in rather than written in the script
//
// The script is a static asset with one ETag: a prefix baked into it would be
// the same bytes for every installation, so moving the admin surface would give
// an operator a screen calling a path nothing serves. Passed through the shell,
// the value has ONE home on the Go side. The composition root pins it to
// [corehttp.DefaultAdminPrefix] and internal/app asserts the two agree.
func (u *UI) showReviews(w http.ResponseWriter, r *http.Request) {
	data := map[string]any{
		titleKey:         reviewsLabel,
		apiPrefixKey:     corehttp.DefaultAdminPrefix,
		reviewsScriptKey: assetURL(ReviewsScriptPath, reviewsScriptStamp),
	}

	u.templates.render(w, r, http.StatusOK, "reviews.gohtml", data)
}
