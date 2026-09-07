package adminui

// The narrowings every screen performs on a read layer record. A [query.Record]
// hands out `any`, so each screen would otherwise write its own type assertion
// for the same field shapes, and one of those copies would be the one that
// panics or accepts a float64 where money is meant. They live here rather than
// in a screen file because five of the panel's screens read records; the page
// parameter joins them for the same reason — every paged screen reads it.

import (
	"strconv"
	"strings"
	"time"

	"github.com/bdrtr/gobit/core/query"
)

// pageNumber reads the page parameter; anything unreadable is page one.
//
// A bad page number is not an error the operator needs to see: the address bar
// is edited by hand and "?page=abc" answering with an error page would be
// louder than the mistake. Falling back to the first page is both harmless and
// obvious on screen.
func pageNumber(raw string) int {
	page, err := strconv.Atoi(strings.TrimSpace(raw))
	if err != nil || page < 1 {
		return 1
	}

	return page
}

// recordString reads a string field, or "" when it is absent or not a string.
func recordString(rec query.Record, field string) string {
	return stringValue(rec[field])
}

// recordTime reads a time field, or the zero time.
func recordTime(rec query.Record, field string) time.Time {
	t, _ := rec[field].(time.Time)

	return t
}

// stringValue narrows any to a string without panicking.
func stringValue(v any) string {
	s, _ := v.(string)

	return s
}

// intValue narrows any to an int, accepting the integer shapes a provider may
// return.
//
// float64 is DELIBERATELY not accepted. The read layer runs in-process and
// hands the provider's own values through, so an amount arrives as an integer;
// a float64 here would mean the value had passed through a JSON round trip and
// lost precision, and rendering it would print a wrong price confidently
// (plan Section 8: money is never a float).
func intValue(v any) (int, bool) {
	switch n := v.(type) {
	case int:
		return n, true
	case int32:
		return int(n), true
	case int64:
		return int(n), true
	default:
		return 0, false
	}
}
