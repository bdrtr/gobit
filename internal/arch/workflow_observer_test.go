package arch_test

import (
	"go/ast"
	"go/token"
	"go/types"
	"maps"
	"path"
	"slices"
	"strconv"
	"strings"
	"testing"

	"github.com/stretchr/testify/assert"
	"github.com/stretchr/testify/require"
)

// This file holds the two censuses of ADR 0385: HOW MANY SAGAS RUN ON THE
// WORKFLOW ENGINE, and WHERE THE ENGINE ACCEPTS CODE IT DOES NOT OWN.
//
// The first is the trigger of a decision, the second is the decision itself,
// both made observable by the suite instead of by memory — see
// [TestASecondSagaReopensTheStepObserverRefusal] and
// [TestTheWorkflowEngineOffersNoObserver].

// engineImportersToday are the production packages outside the engine tree
// that import it.
//
// The checkout is the saga; the composition root builds the engine and
// recovers the checkout's executions; sagawatch reads stuck executions. Nothing
// confines a saga to internal/workflows, so the population is every importer.
var engineImportersToday = []string{
	"internal/app",
	"internal/jobs/sagawatch",
	"internal/workflows/checkout",
}

// sagaNamesToday are the Name values of the workflow definitions built outside
// the engine tree, by package.
//
// There is one, and the number is the whole point.
var sagaNamesToday = []string{"internal/workflows/checkout WorkflowName"}

// engineImportsToday are the engine tree's imports, the standard library's
// included.
//
// A provider slot, a plugin hook, the bus or a tracer would each arrive as a
// new entry here, and so would runtime/trace, expvar or net/http, so the list
// is pinned rather than bounded.
var engineImportsToday = []string{
	"bytes",
	"context",
	"crypto/rand",
	"embed",
	"encoding/base32",
	"encoding/binary",
	"encoding/json",
	modulePath + "/core/db",
	modulePath + "/core/errors",
	modulePath + "/core/personaldata",
	workflowEngineImport,
	"github.com/jackc/pgx/v5",
	"github.com/jackc/pgx/v5/pgconn",
	"github.com/jackc/pgx/v5/pgxpool",
	"io/fs",
	"log/slog",
	"maps",
	"math",
	"reflect",
	"runtime/debug",
	"slices",
	"strings",
	"sync",
	"time",
	"unicode/utf8",
}

// engineSeamsToday are the places the engine tree accepts code it does not own,
// in the vocabulary of [engineSeams]. The list is sorted, so each kind is a
// block.
//
// Each is here for a reason other than watching a step:
//
//   - assert: the three recovery capabilities (ADR 0017), the stored output's
//     JSON form, and a recovered panic read as an error;
//   - field, param and result: the step's input, output and shared values,
//     which are any because a step's types are its own; Retryable, a retry
//     predicate; safeCall's fn, a branch call wrapped for panics; and pgstore's
//     scan and query arguments;
//   - func-type and interface: RunOption configures one run, and the
//     interfaces are the step, the store and the recovery contracts;
//   - outside-type: the context, clock, pool, logger and personal data types
//     the engine names in its declarations;
//   - reflect: a typed-nil step check and the shared-value comparison;
//   - var: sentinel errors, the id encoding and the embedded migrations.
var engineSeamsToday = []string{
	"assert workflow.RunInto json.RawMessage",
	"assert workflow.checkRecoveryBoundary RecoveryBlocker",
	"assert workflow.executor.claimAbandoned ClaimingStore",
	"assert workflow.executor.rebuildChain Recoverable",
	"assert workflow.panicError error",
	"field workflow.RetryPolicy.Retryable",
	"field workflow.StepContext.Input",
	"field workflow.StepContext.Shared",
	"field workflow.branchResult.out",
	"field workflow.branchResult.shared",
	"func-type workflow.RunOption",
	"interface pgstore.rowSource",
	"interface workflow.ClaimingStore",
	"interface workflow.Executor",
	"interface workflow.Recoverable",
	"interface workflow.Recoverer",
	"interface workflow.RecoveryBlocker",
	"interface workflow.Step",
	"interface workflow.Store",
	"outside-type context.CancelFunc",
	"outside-type context.Context",
	"outside-type embed.FS",
	"outside-type encoding/json.RawMessage",
	"outside-type " + modulePath + "/core/db.Pool",
	"outside-type " + modulePath + "/core/personaldata.Declaration",
	"outside-type " + modulePath + "/core/personaldata.Declarer",
	"outside-type " + modulePath + "/core/personaldata.Eraser",
	"outside-type " + modulePath + "/core/personaldata.Result",
	"outside-type " + modulePath + "/core/personaldata.Subject",
	"outside-type github.com/jackc/pgx/v5/pgxpool.Pool",
	"outside-type io/fs.FS",
	"outside-type log/slog.Logger",
	"outside-type sync.Mutex",
	"outside-type time.Duration",
	"outside-type time.Time",
	"param pgstore.rowSource.Scan.dest",
	"param pgstore.skipExecColumns.targets",
	"param pgstore.store.queryExecution.args",
	"param pgstore.wrapDB.a",
	"param workflow.Executor.Run.input",
	"param workflow.ParallelStep.safeCall.fn",
	"param workflow.RunInto.input",
	"param workflow.branchContext.shared",
	"param workflow.executor.Run.input",
	"param workflow.executor.encode.v",
	"param workflow.executor.execute.input",
	"param workflow.executor.recovered.r",
	"param workflow.mergeShared.branch",
	"param workflow.mergeShared.dst",
	"param workflow.mergeShared.snapshot",
	"param workflow.panicError.r",
	"reflect workflow.isNilStep reflect.Chan",
	"reflect workflow.isNilStep reflect.Func",
	"reflect workflow.isNilStep reflect.Interface",
	"reflect workflow.isNilStep reflect.Map",
	"reflect workflow.isNilStep reflect.Pointer",
	"reflect workflow.isNilStep reflect.Slice",
	"reflect workflow.isNilStep reflect.UnsafePointer",
	"reflect workflow.isNilStep reflect.ValueOf",
	"reflect workflow.mergeShared reflect.DeepEqual",
	"result pgstore.jsonParam.#0",
	"result pgstore.scanTargets.#0",
	"result workflow.Executor.Run.#0",
	"result workflow.ParallelStep.Invoke.#0",
	"result workflow.ParallelStep.safeCall.out",
	"result workflow.Step.Invoke.output",
	"result workflow.executor.Run.#0",
	"result workflow.executor.execute.#0",
	"result workflow.executor.invokeStep.output",
	"result workflow.executor.replay.out",
	"result workflow.executor.safeInvoke.out",
	"result workflow.executor.unwind.#0",
	"var pgstore.idEncoding",
	"var pgstore.migrationFiles",
	"var pgstore.migrationsRoot",
	"var workflow.ErrPanic",
	"var workflow.ErrUncompensated",
	"var workflow.idEncoding",
}

// workflowEngineImport is the import path of the saga engine.
const workflowEngineImport = modulePath + "/internal/core/workflow"

// engineStoreInterface is the engine's Store seam, the one a decorator would
// embed to watch AppendStep.
const engineStoreInterface = "Store"

// appendStepImplementationsToday are the files that implement Store.AppendStep.
var appendStepImplementationsToday = []string{
	"internal/core/workflow/memory.go",
	"internal/core/workflow/pgstore/pgstore.go",
}

// TestASecondSagaReopensTheStepObserverRefusal is ADR 0385's trigger.
//
// # What was decided
//
// The workflow engine takes no hook, observer or step callback. A step's name,
// status, attempts, start and end are the StepRecord the executor hands to
// Store.AppendStep, and a saga that wants a span or a timer opens it inside its
// own steps.
//
// # Why a SECOND saga is the fact that reopens it
//
// With one saga, any watching fits inside that saga's steps and a shared seam
// would have exactly one caller. With two, the same watching is written twice,
// and that is the day an engine-level seam pays for itself.
//
// # Why a census and not a rule
//
// It does not say a second saga is wrong. It says a second saga is the event
// the decision named, and it fails so that whoever adds one reads the decision.
// Two populations are pinned, both across the whole production tree, because
// nothing confines a saga to internal/workflows: the packages that import the
// engine, and the Name of every workflow.Workflow literal built outside it. A
// saga in a new package fails the first; a saga added to a package that already
// imports the engine fails the second. A definition assembled field by field
// instead of as a literal is not seen. An empty result fails as BLINDNESS rather
// than passing as agreement.
func TestASecondSagaReopensTheStepObserverRefusal(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)

	importers := map[string]bool{}
	names := map[string]bool{}
	for _, file := range tree.files {
		if underPath(file.importPath, workflowEngineImport) {
			continue
		}
		// The raw import list, not file.imports: that table drops blank and dot
		// imports, and a dot import of the engine still makes a saga.
		if !slices.Contains(rawImports(file), workflowEngineImport) {
			continue
		}
		dir := path.Dir(file.path)
		importers[dir] = true

		ast.Inspect(file.tree, func(node ast.Node) bool {
			literal, ok := node.(*ast.CompositeLit)
			if !ok || len(literal.Elts) == 0 || !namesEngineType(file, literal.Type, "Workflow") {
				return true
			}
			name := "<unnamed>"
			for _, element := range literal.Elts {
				pair, ok := element.(*ast.KeyValueExpr)
				if !ok {
					continue
				}
				if key, ok := pair.Key.(*ast.Ident); ok && key.Name == "Name" {
					name = types.ExprString(pair.Value)
				}
			}
			names[dir+" "+name] = true

			return true
		})
	}

	require.True(t, importers[workflowsDirName+"/checkout"] && len(names) > 0,
		"the checkout was not seen importing the workflow engine and naming its workflow; "+
			"this census has gone BLIND.\n"+
			"The checkout runs on the engine today, so an empty result does not mean the sagas "+
			"are gone — it means the import path or the directory moved and ADR 0385's trigger "+
			"can no longer fire.")

	const reopen = "A second saga is the fact ADR 0385 named. That record refused a hook, " +
		"observer or step callback on the workflow engine WHILE one saga runs on it: a step's " +
		"timing and attempts are kept by the Store, and a saga that wants a span opens it in " +
		"its own steps. A second saga is the day the same watching would be written twice.\n" +
		"This is NOT a defect in your saga: read ADR 0385, decide, supersede it, and update " +
		"this census in the same change."
	assert.Equal(t, engineImportersToday, slices.Sorted(maps.Keys(importers)),
		"the packages that import the workflow engine have changed. If the new one runs a "+
			"saga, wherever it lives:\n"+reopen+"\n"+
			"If it only reads the engine's records, add it here with its reason.")
	assert.Equal(t, sagaNamesToday, slices.Sorted(maps.Keys(names)),
		"the workflow definitions built outside the engine have changed.\n"+reopen)
}

// TestTheWorkflowEngineOffersNoObserver is ADR 0385's refusal.
//
// The engine tree — internal/core/workflow and everything under it — is held in
// three ways, each closing a shape an observer could take:
//
//   - its imports are pinned, the standard library's included, so a provider
//     slot, a plugin hook, the bus, a tracer, runtime/trace or expvar cannot
//     arrive unnoticed;
//   - its seams are pinned (see [engineSeams]): interface and func types, every
//     package variable, every field, parameter and result whose type holds a
//     func, a channel, an interface literal or any, every outside type a
//     declaration names, and in bodies every type assertion, local type,
//     context.Value lookup and use of reflect;
//   - Store.AppendStep is implemented only by the two stores, and no struct
//     anywhere in the production tree embeds the engine's Store — a decorator
//     that watches AppendStep also hides ClaimingStore from recovery (ADR 0017).
//
// A span opened inside a saga's step is outside the engine tree and stays
// allowed.
//
// # What it does not see
//
// The census is syntactic, and a member is counted by its type's spelling. A
// new field, parameter or result of an outside type the engine already names —
// the logger, a context, an fs.FS — passes; so does a new call on a value the
// engine already holds. The logger is the widest of these: whoever builds the
// slog handler the engine is given reads every line it writes about a step.
func TestTheWorkflowEngineOffersNoObserver(t *testing.T) {
	t.Parallel()

	tree := scanProductionSource(t)

	var engine []*sourceFile
	for _, file := range tree.files {
		if underPath(file.importPath, workflowEngineImport) {
			engine = append(engine, file)
		}
	}
	require.NotEmpty(t, engine,
		"no production file was found under %s; this census has gone BLIND.", workflowEngineImport)

	t.Run("imports", func(t *testing.T) {
		t.Parallel()

		imports := map[string]bool{}
		for _, file := range engine {
			for _, imported := range rawImports(file) {
				imports[imported] = true
			}
		}

		require.True(t, imports[modulePath+"/core/errors"] && imports["context"],
			"the engine tree's imports of context and core/errors were not seen; this census "+
				"has gone BLIND.")
		assert.Equal(t, engineImportsToday, slices.Sorted(maps.Keys(imports)),
			"the workflow engine's imports have changed.\n"+
				"A provider, plugin, bus or tracing import — runtime/trace and expvar among "+
				"them — is how a hook, observer or step callback would arrive, and ADR 0385 "+
				"refused one: a step's timing and attempts are kept by the Store, and a saga "+
				"that wants a span opens it in its own steps.\n"+
				"If this import is not an observer, add it here with its reason in the same "+
				"change; if it is, supersede ADR 0385 first.")
	})

	t.Run("seams", func(t *testing.T) {
		t.Parallel()

		seams := map[string]bool{}
		for _, file := range engine {
			for _, seam := range engineSeams(file) {
				seams[seam] = true
			}
		}

		require.True(t, seams["interface workflow."+engineStoreInterface],
			"the engine's Store interface was not seen as a seam; this census has gone BLIND.")
		assert.Equal(t, engineSeamsToday, slices.Sorted(maps.Keys(seams)),
			"the places the workflow engine accepts code it does not own have changed.\n"+
				"An interface, a func type, a package variable, a func, channel, interface or "+
				"any typed member, a new outside type, a type assertion, a context.Value lookup "+
				"or reflection is the shape a hook, observer or step callback takes, and ADR "+
				"0385 refused one.\n"+
				"If this is not an observer, add it here with its reason in the same change; "+
				"if it is, ADR 0385 refused it — supersede it first.")
	})

	t.Run("store decorators", func(t *testing.T) {
		t.Parallel()

		var implementations []string
		for _, file := range tree.files {
			for _, decl := range file.tree.Decls {
				fn, ok := decl.(*ast.FuncDecl)
				if ok && fn.Recv != nil && fn.Name.Name == "AppendStep" {
					implementations = append(implementations, file.path)
				}
			}

			ast.Inspect(file.tree, func(node ast.Node) bool {
				structType, ok := node.(*ast.StructType)
				if !ok {
					return true
				}
				for _, field := range structType.Fields.List {
					if len(field.Names) == 0 && namesEngineStore(file, field.Type) {
						t.Errorf("%s: a struct embeds the workflow engine's Store.\n"+
							"A Store decorator is how AppendStep would be watched, and ADR 0385 "+
							"refused it: an embedding wrapper hides ClaimingStore from recovery "+
							"(ADR 0017). Supersede ADR 0385 first.",
							tree.location(file, field.Pos()))
					}
				}

				return true
			})
		}
		slices.Sort(implementations)

		require.Contains(t, implementations, "internal/core/workflow/memory.go",
			"the in-memory store's AppendStep was not seen; this census has gone BLIND.")
		assert.Equal(t, appendStepImplementationsToday, implementations,
			"a new AppendStep method has appeared in the production tree.\n"+
				"Only the two stores implement it today; a third is a Store decorator, the "+
				"shape a step observer takes once the engine refuses one (ADR 0385). If it "+
				"is a new store, add it here in the same change; if it watches steps, "+
				"supersede ADR 0385 first.")
	})
}

// underPath reports whether the import path p is root or lies beneath it.
func underPath(p, root string) bool {
	return p == root || strings.HasPrefix(p, root+"/")
}

// rawImports returns every import path of the file, blank and dot imports
// included.
func rawImports(file *sourceFile) []string {
	var imports []string
	for _, imp := range file.tree.Imports {
		unquoted, err := strconv.Unquote(imp.Path.Value)
		if err == nil {
			imports = append(imports, unquoted)
		}
	}

	return imports
}

// namesEngineStore reports whether the type expression names the engine's
// Store, qualified from outside the engine package or bare inside it.
func namesEngineStore(file *sourceFile, expr ast.Expr) bool {
	if star, ok := expr.(*ast.StarExpr); ok {
		expr = star.X
	}

	return namesEngineType(file, expr, engineStoreInterface)
}

// namesEngineType reports whether the type expression is the engine's type of
// that name, qualified from outside the engine package or bare inside it.
func namesEngineType(file *sourceFile, expr ast.Expr, name string) bool {
	switch typed := expr.(type) {
	case *ast.Ident:
		return file.importPath == workflowEngineImport && typed.Name == name
	case *ast.SelectorExpr:
		qualifier, ok := typed.X.(*ast.Ident)

		return ok && typed.Sel.Name == name && file.imports[qualifier.Name] == workflowEngineImport
	}

	return false
}

// engineSeams lists the file's seams in the census's vocabulary.
//
// Declarations are read whole: interface and func types, named types holding
// one or defined from an outside type, every package variable by name, and the
// fields, parameters and results whose type holds a func, a channel, an
// interface literal or any. Every outside type a declaration names is listed
// once, by import path.
//
// Bodies are read for the four ways a body accepts code it was not given in a
// signature: a type assertion or type switch case, a type declared inside it, a
// one-argument Value call (context.Value), and the reflect package.
func engineSeams(file *sourceFile) []string {
	pkg := file.tree.Name.Name

	var seams []string
	for _, decl := range file.tree.Decls {
		switch typed := decl.(type) {
		case *ast.GenDecl:
			seams = append(seams, genDeclSeams(file, pkg, typed)...)
			for _, spec := range typed.Specs {
				if value, ok := spec.(*ast.ValueSpec); ok {
					for _, expr := range value.Values {
						seams = append(seams, bodySeams(file, pkg, expr)...)
					}
				}
			}
		case *ast.FuncDecl:
			owner := pkg + "."
			if typed.Recv != nil && len(typed.Recv.List) > 0 {
				owner += receiverTypeName(typed.Recv.List[0].Type) + "."
			}
			owner += typed.Name.Name
			seams = append(seams, signatureSeams(owner, typed.Type)...)
			seams = append(seams, outsideTypes(file, typed.Type)...)
			if typed.Body != nil {
				seams = append(seams, bodySeams(file, owner, typed.Body)...)
			}
		}
	}

	return seams
}

// genDeclSeams lists the seams of one type or variable declaration.
func genDeclSeams(file *sourceFile, pkg string, decl *ast.GenDecl) []string {
	var seams []string
	for _, spec := range decl.Specs {
		switch typed := spec.(type) {
		case *ast.TypeSpec:
			seams = append(seams, outsideTypes(file, typed.Type)...)
			name := pkg + "." + typed.Name.Name
			switch body := typed.Type.(type) {
			case *ast.InterfaceType:
				seams = append(seams, "interface "+name)
				for _, method := range body.Methods.List {
					signature, ok := method.Type.(*ast.FuncType)
					if !ok {
						continue
					}
					for _, methodName := range method.Names {
						seams = append(seams, signatureSeams(name+"."+methodName.Name, signature)...)
					}
				}
			case *ast.FuncType:
				seams = append(seams, "func-type "+name)
			case *ast.StructType:
				for i, field := range body.Fields.List {
					if !holdsSeamType(field.Type) {
						continue
					}
					for _, fieldName := range memberNames(field, i) {
						seams = append(seams, "field "+name+"."+fieldName)
					}
				}
			default:
				if holdsSeamType(body) || len(outsideTypes(file, body)) > 0 {
					seams = append(seams, "type "+name)
				}
			}
		case *ast.ValueSpec:
			if decl.Tok != token.VAR {
				continue
			}
			// Every package variable, whatever its type or value: `var hook =
			// noop` has a func type that no syntax spells.
			if typed.Type != nil {
				seams = append(seams, outsideTypes(file, typed.Type)...)
			}
			for _, varName := range typed.Names {
				if varName.Name != "_" {
					seams = append(seams, "var "+pkg+"."+varName.Name)
				}
			}
		}
	}

	return seams
}

// signatureSeams lists the parameters and results of a signature whose type
// holds a func, a channel, an interface literal or any.
func signatureSeams(owner string, signature *ast.FuncType) []string {
	var seams []string
	for kind, list := range map[string]*ast.FieldList{"param": signature.Params, "result": signature.Results} {
		if list == nil {
			continue
		}
		for i, field := range list.List {
			if !holdsSeamType(field.Type) {
				continue
			}
			for _, name := range memberNames(field, i) {
				seams = append(seams, kind+" "+owner+"."+name)
			}
		}
	}

	return seams
}

// outsideTypes lists every type from outside the engine tree that the type
// expression names, by import path.
func outsideTypes(file *sourceFile, expr ast.Expr) []string {
	var seams []string
	ast.Inspect(expr, func(node ast.Node) bool {
		selector, ok := node.(*ast.SelectorExpr)
		if !ok {
			return true
		}
		qualifier, ok := selector.X.(*ast.Ident)
		if !ok {
			return true
		}
		imported := file.imports[qualifier.Name]
		if imported == "" {
			imported = qualifier.Name
		}
		if !underPath(imported, workflowEngineImport) {
			seams = append(seams, "outside-type "+imported+"."+selector.Sel.Name)
		}

		return false
	})

	return seams
}

// bodySeams lists the places inside a body, or a package variable's value,
// where the engine takes code it was not handed through a declaration.
func bodySeams(file *sourceFile, owner string, node ast.Node) []string {
	var seams []string
	ast.Inspect(node, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.TypeAssertExpr:
			// A type switch's guard has no type; its cases are read below.
			if typed.Type != nil {
				seams = append(seams, "assert "+owner+" "+types.ExprString(typed.Type))
			}
		case *ast.TypeSwitchStmt:
			for _, statement := range typed.Body.List {
				clause, ok := statement.(*ast.CaseClause)
				if !ok {
					continue
				}
				for _, caseType := range clause.List {
					if ident, ok := caseType.(*ast.Ident); !ok || ident.Name != "nil" {
						seams = append(seams, "assert "+owner+" "+types.ExprString(caseType))
					}
				}
			}
		case *ast.TypeSpec:
			seams = append(seams, "local-type "+owner+"."+typed.Name.Name)
		case *ast.CallExpr:
			if selector, ok := typed.Fun.(*ast.SelectorExpr); ok && selector.Sel.Name == "Value" && len(typed.Args) == 1 {
				seams = append(seams, "value-lookup "+owner)
			}
		case *ast.SelectorExpr:
			if qualifier, ok := typed.X.(*ast.Ident); ok && file.imports[qualifier.Name] == "reflect" {
				seams = append(seams, "reflect "+owner+" reflect."+typed.Sel.Name)
			}
		}

		return true
	})

	return seams
}

// holdsSeamType reports whether a type or value expression contains a func
// type, a channel type, an interface literal or the identifier any.
//
// A function literal's body is not read, only its signature.
func holdsSeamType(expr ast.Expr) bool {
	found := false
	ast.Inspect(expr, func(node ast.Node) bool {
		switch typed := node.(type) {
		case *ast.FuncType, *ast.ChanType, *ast.InterfaceType, *ast.FuncLit:
			found = true
		case *ast.Ident:
			found = typed.Name == "any"
		}

		return !found
	})

	return found
}

// memberNames returns a field's names, or its position when it has none.
func memberNames(field *ast.Field, position int) []string {
	if len(field.Names) == 0 {
		return []string{"#" + strconv.Itoa(position)}
	}

	names := make([]string, 0, len(field.Names))
	for _, name := range field.Names {
		names = append(names, name.Name)
	}

	return names
}
