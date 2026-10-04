package webpush

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strconv"
	"strings"
	"testing"
	"time"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"

	"github.com/bdrtr/gobit/core/eventbus"
)

// TestTheRecordOutlivesThePushHorizon pins the order of the two durations.
//
// The record forgets an id after claimRetention, and only the age check stops a
// delivery of that id after then. A retention at or below the horizon lets a
// late delivery find neither and push the order again (ADR 0389).
func TestTheRecordOutlivesThePushHorizon(t *testing.T) {
	t.Parallel()

	assert.Greater(t, claimRetention, pushHorizon,
		"an id the record has forgotten must belong to an order the age check refuses")
}

// TestTheHorizonIsThePushTTL pins the horizon to the TTL the push service is
// given, so the plugin does not push late what the push service would drop.
func TestTheHorizonIsThePushTTL(t *testing.T) {
	t.Parallel()

	assert.Equal(t, time.Duration(ttlSeconds)*time.Second, pushHorizon)
}

// TestAnOrderIsPushedUpToTheHorizon walks tooLate across the boundary.
//
// An order exactly pushHorizon old is still pushed; one a nanosecond older is
// not. A placement in the future (a clock ahead of this one) is pushed, and one
// that cannot be read is reported unreadable rather than late.
func TestAnOrderIsPushedUpToTheHorizon(t *testing.T) {
	t.Parallel()

	now := time.Date(2026, 10, 5, 12, 0, 0, 0, time.UTC)
	at := func(placed time.Time) eventbus.Event {
		return eventbus.Event{Data: map[string]any{fieldPlacedAt: placed.Format(time.RFC3339Nano)}}
	}

	for name, tc := range map[string]struct {
		event    eventbus.Event
		late     bool
		readable bool
	}{
		"exactly the horizon":    {at(now.Add(-pushHorizon)), false, true},
		"a nanosecond past it":   {at(now.Add(-pushHorizon - time.Nanosecond)), true, true},
		"placed a minute ago":    {at(now.Add(-time.Minute)), false, true},
		"placed in the future":   {at(now.Add(time.Hour)), false, true},
		"another zone, too late": {at(now.Add(-5 * time.Hour).In(time.FixedZone("TRT", 3*60*60))), true, true},
		"no placed_at":           {eventbus.Event{Data: map[string]any{}}, false, false},
		"a placed_at not a time": {eventbus.Event{Data: map[string]any{fieldPlacedAt: "yesterday"}}, false, false},
		"a placed_at not a text": {eventbus.Event{Data: map[string]any{fieldPlacedAt: now}}, false, false},
	} {
		t.Run(name, func(t *testing.T) {
			t.Parallel()

			late, readable := tooLate(tc.event, now)
			assert.Equal(t, tc.late, late, "late")
			assert.Equal(t, tc.readable, readable, "readable")
		})
	}
}

// orderEventsFile is the order module's declaration of its event fields.
const orderEventsFile = "../../internal/modules/order/service/events.go"

// TestTheFieldsReadAreTheOrderModules holds the payload fields the handler
// reads to the ones the order module writes.
//
// The plugin may not import the module, so it spells the names itself, and a
// name the module renamed would read as empty: no customer means no push, and
// no placed_at means no age check, both without an error. Both sides are read
// from source rather than from a list: the order module's file for every
// EventField* constant, and this package's non-test files for every field*
// constant and every [field] call, whose name has to be one of those
// constants. A field added and read without being listed anywhere is then
// still held. A census that found nothing has gone blind, not passed.
func TestTheFieldsReadAreTheOrderModules(t *testing.T) {
	t.Parallel()

	file, err := parser.ParseFile(token.NewFileSet(), filepath.FromSlash(orderEventsFile), nil, 0)
	require.NoError(t, err, "the order module's event file has to be readable from here")
	published := map[string]bool{}
	for name, value := range stringConstants(t, file) {
		if strings.HasPrefix(name, "EventField") {
			published[value] = true
		}
	}
	require.NotEmpty(t, published,
		"no EventField* constant was found in %s; the census is blind, not green", orderEventsFile)

	sources, err := filepath.Glob("*.go")
	require.NoError(t, err)
	fset := token.NewFileSet()
	read := map[string]string{}
	var calls []*ast.CallExpr
	for _, source := range sources {
		if strings.HasSuffix(source, "_test.go") {
			continue
		}
		file, err := parser.ParseFile(fset, source, nil, 0)
		require.NoError(t, err)
		for name, value := range stringConstants(t, file) {
			if isFieldConstant(name) {
				read[name] = value
			}
		}
		ast.Inspect(file, func(n ast.Node) bool {
			if call, ok := n.(*ast.CallExpr); ok {
				if fn, ok := call.Fun.(*ast.Ident); ok && fn.Name == "field" {
					calls = append(calls, call)
				}
			}

			return true
		})
	}

	// The subjects by name: a census that lost them has gone blind.
	require.Contains(t, read, "fieldCustomerID", "the field* constants were not found")
	require.Contains(t, read, "fieldPlacedAt", "the field* constants were not found")
	require.NotEmpty(t, calls, "no call to field was found")

	for name, value := range read {
		assert.Truef(t, published[value],
			"%s is %q, which the order module does not publish", name, value)
	}
	for _, call := range calls {
		require.Lenf(t, call.Args, 2, "%s: field takes an event and a name", fset.Position(call.Pos()))
		ident, ok := call.Args[1].(*ast.Ident)
		assert.Truef(t, ok && read[ident.Name] != "",
			"%s: field is read with a name that is not a field* constant, so nothing holds it "+
				"to the order module", fset.Position(call.Pos()))
	}
}

// isFieldConstant reports whether a constant's name is one of the handler's
// field names: "field" followed by an upper-case letter.
func isFieldConstant(name string) bool {
	rest, found := strings.CutPrefix(name, "field")

	return found && rest != "" && rest[0] >= 'A' && rest[0] <= 'Z'
}

// stringConstants collects every constant in the file whose value is a string
// literal, by name.
func stringConstants(t *testing.T, file *ast.File) map[string]string {
	t.Helper()

	out := map[string]string{}
	for _, decl := range file.Decls {
		gen, ok := decl.(*ast.GenDecl)
		if !ok || gen.Tok != token.CONST {
			continue
		}
		for _, spec := range gen.Specs {
			value, ok := spec.(*ast.ValueSpec)
			if !ok {
				continue
			}
			for i, name := range value.Names {
				if i >= len(value.Values) {
					continue
				}
				lit, ok := value.Values[i].(*ast.BasicLit)
				if !ok || lit.Kind != token.STRING {
					continue
				}
				unquoted, err := strconv.Unquote(lit.Value)
				require.NoError(t, err)
				out[name.Name] = unquoted
			}
		}
	}

	return out
}
