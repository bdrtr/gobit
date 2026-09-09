package adminui

import (
	"go/parser"
	"go/token"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// TestTheDocNamesEverySection holds the package's own description against the
// menu it describes.
//
// # Why this exists
//
// The doc used to open a section with "Five sections" and then name five of
// them. Adding the sixth falsified both, in the same commit that added it — a
// document made wrong by its own change, which is the fault this repository has
// found in itself most often. Nothing noticed: the menu is a Go slice and the
// description is a comment, and no compiler puts the two together.
//
// # What it checks and what it deliberately does not
//
// It checks that every LABEL in [sections] appears somewhere in the package
// comment. That is the property with a reader on the other side: somebody
// learning what the panel is should not find a list missing the screen they
// were sent to.
//
// It does not check the ORDER, and it does not check that the comment names no
// section the menu lacks — the prose says "the catalog, the orders" where the
// menu says "Catalog", and a matcher strict enough for one would be wrong about
// the other. The count is gone from the heading for the same reason: a number
// that only restates the list beneath it is a surface that can rot and buys
// nothing.
func TestTheDocNamesEverySection(t *testing.T) {
	t.Parallel()

	comment := packageComment(t)

	for _, item := range sections() {
		assert.Contains(t, strings.ToLower(comment), strings.ToLower(item.Label),
			"the package doc describes the panel and never names the %q section.\n"+
				"A reader learning what this package is would find a list missing a screen "+
				"the menu offers — which is how the doc was wrong before this test existed.",
			item.Label)
	}
}

// packageComment returns doc.go's package comment.
//
// It is read from the FILE rather than from a string in this test, because what
// is being checked is the document somebody reads, and a copy here would be a
// second document that agrees with itself.
func packageComment(t *testing.T) string {
	t.Helper()

	fset := token.NewFileSet()
	file, err := parser.ParseFile(fset, "doc.go", nil, parser.ParseComments)
	require.NoError(t, err, "doc.go could not be parsed")
	require.NotNil(t, file.Doc,
		"doc.go carries no package comment at all; this check would then pass over a "+
			"package that describes nothing")

	return file.Doc.Text()
}
