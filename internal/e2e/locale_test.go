//go:build integration

package e2e

import (
	"context"
	"go/ast"
	"go/parser"
	"go/token"
	"log/slog"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"sync"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
	"github.com/testcontainers/testcontainers-go"
	tcpostgres "github.com/testcontainers/testcontainers-go/modules/postgres"

	coredb "github.com/bdrtr/gobit/core/db"
)

// This file audits the CLUSTER the suite runs on, not the code that runs on it.
//
// # Why it exists
//
// Three rounds of this repository's history were spent on a cluster whose case
// folding was not what the code assumed (ADR 0038, ADR 0039). The failure has a
// shape worth naming: nothing errors. A search returns fewer rows, an e-mail
// CHECK stops refusing a duplicate, and every test stays green — because the
// tests ran on the same wrong cluster.
//
// The locale is fixed by initdb at the moment a data directory is created, so
// it cannot be corrected later and cannot be set by a test that has already
// connected. What a test CAN do is refuse to trust a cluster that folds
// differently from the one the code was measured against.
//
// # Two probes, and they are not the same kind of thing
//
// The FOLD is the property: three expressions the production code depends on,
// asserted through gobit's own probe rather than through a copy of its SQL.
//
// The ORDER is a CANARY. Nothing in this tree orders by text — the listings
// order by (created_at, id) — so a change in collation order breaks nothing
// directly. It is here because it is the only cheap probe that DISTINGUISHES
// the images: measured, the alpine image and the same image with
// --locale=C.UTF-8 fold identically and order identically, while the debian
// image with no locale argument orders differently. So the fold cannot notice
// the base image changing under the suite and this can.
//
// Saying which is which matters: a reader who took the canary for a dependency
// would think the catalog's ordering is collation-sensitive, and it is not.

// localeWordList is what the canary orders.
//
// The words are chosen to separate the collations that matter: a digit against
// a letter, a hyphen inside a word, and the same word in two cases. Under C the
// capitals sort before the lower case; under a UTF-8 locale they interleave —
// which is how this list tells two images apart even when both fold correctly.
var localeWordList = []string{"apple", "Banana", "a-1", "A1", "apricot", "B-side"}

// localeExpectedOrder is what the image the suite starts returns, MEASURED on
// 2026-09-09 against postgres:16-alpine with no initdb arguments.
//
// Written out rather than computed: a computed expectation would be whatever
// the cluster does, which is the thing being checked.
//
// The first draft of this constant was a GUESS — the interleaved order a UTF-8
// collation gives — and the alpine image returned the C-like one instead:
// capitals first, punctuation ignored differently. The guess was wrong and the
// measurement is what is here, which is the whole rule this repository has
// about numbers in prose.
//
// So this value says nothing about which collation is CORRECT. It says which
// one the suite has been measuring on, and the fold check above is what says
// whether that cluster is usable at all.
const localeExpectedOrder = "A1|B-side|Banana|a-1|apple|apricot"

// foldWarning is the message gobit logs when the cluster folds ASCII only.
//
// It is the real probe's own sentence, matched here rather than re-running the
// probe's SQL: a second copy of that query would be free to drift from the one
// production runs, and this file exists because a measurement taken on the
// wrong cluster is worse than none.
const foldWarning = "this database folds ASCII case only"

// containerImages returns the container images the tree starts, from the AST.
//
// The literals are read as VALUES rather than matched with a pattern over the
// file text. A regexp was tried first and pulled in two prose mentions inside
// comments, one of them naming an image no test ever runs — the gate would then
// have pulled and started a container for a bench note.
//
// A DSN is excluded by the "//" it contains: `postgres://gobit@host/db` shares
// the prefix and is not an image.
func containerImages(t *testing.T) []string {
	t.Helper()

	found := map[string]bool{}

	err := filepath.WalkDir(repoRootDir(t), func(path string, entry os.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			if entry.Name() == ".git" || entry.Name() == ".claude" || entry.Name() == "node_modules" {
				return filepath.SkipDir
			}

			return nil
		}
		if !strings.HasSuffix(path, ".go") {
			return nil
		}

		file, parseErr := parser.ParseFile(token.NewFileSet(), path, nil, 0)
		if parseErr != nil {
			return nil
		}

		ast.Inspect(file, func(node ast.Node) bool {
			lit, ok := node.(*ast.BasicLit)
			if !ok || lit.Kind != token.STRING {
				return true
			}
			value, unquoteErr := strconv.Unquote(lit.Value)
			if unquoteErr != nil {
				return true
			}
			// A tag is required after the colon: the bare scheme "postgres:"
			// appears in the tree as a DSN prefix check, and a container image
			// without a tag is not one this suite starts either.
			tag, isImage := strings.CutPrefix(value, "postgres:")
			if isImage && tag != "" && !strings.Contains(value, "//") {
				found[value] = true
			}

			return true
		})

		return nil
	})
	require.NoError(t, err, "the tree could not be walked for container images")

	images := make([]string, 0, len(found))
	for image := range found {
		images = append(images, image)
	}

	return images
}

// repoRootDir walks up to the module root.
func repoRootDir(t *testing.T) string {
	t.Helper()

	dir, err := os.Getwd()
	require.NoError(t, err)

	for range 8 {
		if _, statErr := os.Stat(filepath.Join(dir, "go.mod")); statErr == nil {
			return dir
		}
		dir = filepath.Dir(dir)
	}

	t.Fatal("the module root could not be found")

	return ""
}

// TestEveryClusterTheSuiteStartsFoldsTheWayTheCodeAssumes is the audit.
//
// Each image is started the way the suite starts it — WITH NO initdb arguments
// — because that is the cluster the tests actually measure on. Starting it with
// the compose file's locale would audit a cluster no test uses.
func TestEveryClusterTheSuiteStartsFoldsTheWayTheCodeAssumes(t *testing.T) {
	images := containerImages(t)

	require.NotEmpty(t, images,
		"no container image was read out of the tree; the walk has gone blind and this "+
			"audit would pass having started nothing")

	for _, image := range images {
		t.Run(image, func(t *testing.T) {
			assertClusterFoldsAndOrders(t, image)
		})
	}
}

// assertClusterFoldsAndOrders starts one image and asks it both questions.
func assertClusterFoldsAndOrders(t *testing.T, image string) {
	t.Helper()

	ctx := context.Background()

	container, err := tcpostgres.Run(ctx, image,
		tcpostgres.WithDatabase("gobit_locale"),
		tcpostgres.WithUsername("gobit"),
		tcpostgres.WithPassword("gobit"),
		tcpostgres.BasicWaitStrategies(),
	)
	t.Cleanup(func() {
		if termErr := testcontainers.TerminateContainer(container); termErr != nil {
			t.Logf("the %s container could not be stopped: %v", image, termErr)
		}
	})
	require.NoError(t, err, "%s could not be started", image)

	dsn, err := container.ConnectionString(ctx, "sslmode=disable")
	require.NoError(t, err)

	// THE FOLD, through gobit's own probe. db.New runs it and logs; the handler
	// below is how the result is read without a second copy of the query.
	warnings := &captureHandler{}
	// DefaultConfig rather than a literal: the pool refuses a zero MaxConns,
	// and a hand-built config here would be a second copy of the defaults.
	pool, err := coredb.New(ctx, coredb.DefaultConfig(dsn), slog.New(warnings))
	require.NoError(t, err, "the pool could not be opened against %s", image)
	t.Cleanup(pool.Close)

	assert.False(t, warnings.saw(foldWarning),
		"%s folds ASCII case only. Every measurement taken on this cluster is a "+
			"measurement of the wrong one: a shopper's non-ASCII search silently returns "+
			"fewer rows, and the e-mail CHECK constraints stop refusing a second account "+
			"for the same person. The locale is fixed by initdb, so the fix is a new data "+
			"directory rather than a setting.", image)

	// THE CANARY. It is not a property the code depends on; see the file header.
	var ordered string
	require.NoError(t, pool.Pool().QueryRow(ctx,
		`SELECT string_agg(v, '|' ORDER BY v) FROM unnest($1::text[]) AS v`,
		localeWordList).Scan(&ordered))

	assert.Equal(t, localeExpectedOrder, ordered,
		"%s orders text differently from the cluster this code was measured against. "+
			"Nothing in the tree orders by text, so this breaks no query — it is the only "+
			"cheap probe that notices the base image or its locale changing under the "+
			"suite, which the fold check above cannot: measured, two images can fold "+
			"identically and order differently.", image)
}

// captureHandler keeps the messages logged through it.
type captureHandler struct {
	mu       sync.Mutex
	messages []string
}

func (c *captureHandler) Enabled(context.Context, slog.Level) bool { return true }

func (c *captureHandler) Handle(_ context.Context, record slog.Record) error {
	c.mu.Lock()
	defer c.mu.Unlock()

	c.messages = append(c.messages, record.Message)

	return nil
}

func (c *captureHandler) WithAttrs([]slog.Attr) slog.Handler { return c }

func (c *captureHandler) WithGroup(string) slog.Handler { return c }

// saw reports whether a message containing the phrase was logged.
func (c *captureHandler) saw(phrase string) bool {
	c.mu.Lock()
	defer c.mu.Unlock()

	for _, message := range c.messages {
		if strings.Contains(message, phrase) {
			return true
		}
	}

	return false
}
