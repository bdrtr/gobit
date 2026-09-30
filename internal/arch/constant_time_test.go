package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"io/fs"
	"os"
	"path/filepath"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ADR 0257: a file that makes or checks a secret compares
// nothing with == or bytes.Equal.
//
// # The population is derived from imports
//
// A comparison's operands carry no type a syntactic scan can see, and their
// names say nothing: the TOTP check compared `expected` with `typed`, the token
// check `a` with `b`. A scan by name found one of the seven comparisons. So the
// population is the FILES that produce a MAC, a derived key, a digest or random
// bytes, which is where a secret is made and checked, and in them every
// comparison between two values is a finding unless it is listed below as
// comparing no secret.

// secretImports mark a file as one that makes or checks a secret.
var secretImports = map[string]bool{
	"crypto/hmac":                true,
	"crypto/subtle":              true,
	"crypto/sha1":                true,
	"crypto/sha256":              true,
	"crypto/sha512":              true,
	"crypto/rand":                true,
	"golang.org/x/crypto/argon2": true,
}

// variableTimeCalls compare their arguments byte by byte and stop at the first
// difference.
var variableTimeCalls = map[string]bool{
	"bytes.Equal":       true,
	"bytes.Compare":     true,
	"strings.Compare":   true,
	"strings.EqualFold": true,
	"reflect.DeepEqual": true,
}

// notSecretComparisons are the comparisons in those files that compare no
// secret, by file and by their source text. Each is a decision, and each says
// why.
var notSecretComparisons = map[string][]string{
	// The version field of a stored argon2id hash: its format, not the hash.
	"contrib/identity-session/password.go": {"version != argon2.Version"},
	// An error's kind against a constant.
	"core/eventbus/eventbus.go": {"errors.KindOf(err) == errors.KindInvalid"},
	// The fingerprint of the caller's own request, compared with the one the
	// same key stored: it identifies a request, and the caller holds both.
	"core/http/callback_guard.go": {"record.Fingerprint == keys.fingerprint"},
	"core/http/idempotency.go":    {"rec.Fingerprint != izi"},
	// A configured backend against a constant.
	"internal/app/guards.go": {"cfg.GuardBackend == config.BackendRedis"},
	// A step's status against constants.
	"internal/core/workflow/store.go": {"s == StepInvoked", "s == StepCompensationFailed"},
	// A gift card code's LENGTH once normalized, before any lookup.
	"internal/modules/payment/models/giftcard.go": {"out.Len() != GiftCardCodeLength"},
	// A URL's scheme against constants.
	"plugins/files3/plugin.go": {"u.Scheme != schemeHTTPS", "u.Scheme != schemeHTTP"},
}

// constantTimeSites are the files holding the five comparisons the known
// limits named before ADR 0257 (a TOTP code, an argon2id derived key, two
// cookie MACs and a token digest) and the two signed callbacks (PayTR's and the
// outgoing webhooks'). The import-derived population has to reach each of them,
// or the gate protects none.
var constantTimeSites = []string{
	"internal/modules/auth/service/totp.go",
	"contrib/identity-session/password.go",
	"contrib/identity-session/session.go",
	"internal/modules/auth/models/token.go",
	"plugins/paymentpaytr/hash.go",
	"plugins/webhookout/signature.go",
}

// isTrivialOperand is a side of a comparison that holds no secret: a literal,
// nil, a boolean, or a length.
func isTrivialOperand(e ast.Expr) bool {
	switch v := e.(type) {
	case *ast.BasicLit:
		return true
	case *ast.Ident:
		return v.Name == "nil" || v.Name == "true" || v.Name == "false"
	case *ast.CallExpr:
		fn, ok := v.Fun.(*ast.Ident)
		return ok && fn.Name == "len"
	case *ast.UnaryExpr:
		return isTrivialOperand(v.X)
	case *ast.ParenExpr:
		return isTrivialOperand(v.X)
	}
	return false
}

// secretComparisons returns the files that import a secretImports package and
// the comparisons each holds, by source text.
func secretComparisons(t *testing.T) (population []string, found map[string][]string) {
	t.Helper()

	found = map[string][]string{}
	fset := token.NewFileSet()
	err := filepath.WalkDir(repoRoot, func(path string, entry fs.DirEntry, err error) error {
		if err != nil {
			return err
		}
		if entry.IsDir() {
			switch entry.Name() {
			case ".git", "testdata", "vendor", "node_modules":
				return filepath.SkipDir
			}
			return nil
		}
		if !strings.HasSuffix(path, ".go") || strings.HasSuffix(path, "_test.go") {
			return nil
		}

		src, err := os.ReadFile(path)
		if err != nil {
			return err
		}
		file, err := parser.ParseFile(fset, path, src, parser.SkipObjectResolution)
		if err != nil {
			return err
		}
		imports := false
		for _, spec := range file.Imports {
			if importPath, _ := strconv.Unquote(spec.Path.Value); secretImports[importPath] {
				imports = true
			}
		}
		if !imports {
			return nil
		}

		rel, err := filepath.Rel(repoRoot, path)
		if err != nil {
			return err
		}
		rel = filepath.ToSlash(rel)
		population = append(population, rel)
		text := func(n ast.Node) string {
			return string(src[fset.Position(n.Pos()).Offset:fset.Position(n.End()).Offset])
		}

		ast.Inspect(file, func(n ast.Node) bool {
			switch v := n.(type) {
			case *ast.BinaryExpr:
				if (v.Op == token.EQL || v.Op == token.NEQ) &&
					!isTrivialOperand(v.X) && !isTrivialOperand(v.Y) {
					found[rel] = append(found[rel], text(v))
				}
			case *ast.CallExpr:
				if sel, ok := v.Fun.(*ast.SelectorExpr); ok {
					if pkg, ok := sel.X.(*ast.Ident); ok && variableTimeCalls[pkg.Name+"."+sel.Sel.Name] {
						found[rel] = append(found[rel], text(v))
					}
				}
			}
			return true
		})

		return nil
	})
	require.NoError(t, err)

	return population, found
}

// TestASecretIsComparedInConstantTime is ADR 0257. In a file that makes or
// checks a secret, every comparison between two values is either listed as
// comparing no secret or a finding; the secret itself is compared with
// hmac.Equal or subtle.ConstantTimeCompare, whose result is compared with a
// literal and so is not one.
func TestASecretIsComparedInConstantTime(t *testing.T) {
	t.Parallel()

	population, found := secretComparisons(t)
	for _, site := range constantTimeSites {
		require.Contains(t, population, site,
			"%s holds a constant-time comparison and imports none of the packages that "+
				"mark a file as making a secret; the gate cannot see it", site)
	}

	for _, file := range slices.Sorted(func(yield func(string) bool) {
		for file := range found {
			if !yield(file) {
				return
			}
		}
	}) {
		for _, comparison := range found[file] {
			assert.Contains(t, notSecretComparisons[file], comparison,
				"%s compares %q with an operator that stops at the first difference. If "+
					"either side is a secret or derived from one, compare with hmac.Equal or "+
					"subtle.ConstantTimeCompare; if neither is, list it in "+
					"notSecretComparisons with the reason.", file, comparison)
		}
	}

	// A listed comparison that no longer exists is a reason nobody can check.
	for file, comparisons := range notSecretComparisons {
		for _, comparison := range comparisons {
			assert.Contains(t, found[file], comparison,
				"notSecretComparisons lists %q in %s, which no longer holds it", comparison, file)
		}
	}
}
