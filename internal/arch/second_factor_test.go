package arch_test

import (
	"go/ast"
	"go/parser"
	"go/token"
	"path/filepath"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds ONE claim, and it is the claim the feature is worth nothing
// without: every path that mints a session token asks for the account's second
// factor first (ADR 0147).
//
// # Why a gate and not only a test
//
// The behavior is tested where it belongs — the service refuses a password-only
// login for an account that proved a factor. What a test cannot see is the path
// that does not exist yet. `Login` is the only session mint today; the day an
// installation wants a magic link, an OAuth callback or an impersonation verb,
// that path will be written beside it, will compile, and will hand out a token
// nobody asked a factor for. Every existing test stays green, because they are
// all about the path that was already right.
//
// So the population is DERIVED: whoever calls the minting function has to call
// the demand, or be written down here with a reason.
//
// # What it cannot see
//
// A caller that reaches the demand through another function this scan does not
// unfold — the check is one level deep, on purpose, because a call graph walk
// would answer "somewhere below here it is called" and the thing that matters is
// that the mint and the demand sit in one readable body. A helper that wrapped
// the demand would pass; it would also be a place to read.
//
// And a demand that is CALLED and ignored. Wrapping the call in `if false` was
// mutated and this gate stayed green — it prices the presence of the call, not
// its effect. What bit there was the service's own tests, six of them, which is
// the right division: the behavior is tested where it behaves, and this asks
// only whether the question is asked at all.

// The two unexported functions this gate pairs, and the package they live in.
const (
	sessionMintFunc   = "issueToken"
	secondFactorFunc  = "demandSecondFactor"
	authServicePkgDir = modulesDir + "/auth/service"
)

// sessionMintExemptions are the functions allowed to mint a session without
// asking for the second factor.
//
// A reason is required. The list is empty today and that is the point: an empty
// exemption list beside a derived population is a rule with nothing hidden
// behind it.
var sessionMintExemptions = map[string]string{}

// TestEverySessionMintDemandsTheSecondFactor refuses a second way in.
func TestEverySessionMintDemandsTheSecondFactor(t *testing.T) {
	t.Parallel()

	minting := 0

	for _, file := range productionFiles(t, filepath.Join(repoRoot, authServicePkgDir)) {
		fset := token.NewFileSet()
		parsed, err := parser.ParseFile(fset, file, nil, 0)
		require.NoError(t, err, "%s could not be parsed", file)

		rel := strings.TrimPrefix(file, repoRoot+"/")

		for _, decl := range parsed.Decls {
			fn, ok := decl.(*ast.FuncDecl)
			if !ok || fn.Body == nil || fn.Name.Name == sessionMintFunc {
				continue
			}
			if !callsFunction(fn, sessionMintFunc) {
				continue
			}

			minting++
			if reason, exempt := sessionMintExemptions[fn.Name.Name]; exempt {
				assert.NotEmpty(t, reason, "%s has an exemption with no reason", fn.Name.Name)

				continue
			}

			assert.Truef(t, callsFunction(fn, secondFactorFunc),
				"%s.%s mints a session token and never calls %s.\n"+
					"A token is the whole of an administrator's authority, and an account "+
					"that proved an authenticator is one a password alone must not open "+
					"(ADR 0147). Every login test in the tree stays green when a SECOND "+
					"mint appears, because they are all about the first one.\n"+
					"Call the demand before the token is signed, or add %s to "+
					"sessionMintExemptions with the reason this path is different.",
				rel, fn.Name.Name, secondFactorFunc, fn.Name.Name)
		}
	}

	require.Positive(t, minting,
		"no session mint was found at all; %s may have been renamed and this gate is "+
			"now checking nothing", sessionMintFunc)
}

// callsFunction reports whether the body calls the named function directly.
func callsFunction(fn *ast.FuncDecl, name string) bool {
	found := false

	ast.Inspect(fn.Body, func(node ast.Node) bool {
		call, ok := node.(*ast.CallExpr)
		if !ok {
			return true
		}

		switch target := call.Fun.(type) {
		case *ast.Ident:
			found = found || target.Name == name
		case *ast.SelectorExpr:
			found = found || target.Sel.Name == name
		}

		return true
	})

	return found
}
