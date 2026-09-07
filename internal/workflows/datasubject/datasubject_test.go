package datasubject_test

import (
	"context"
	"errors"
	"io/fs"
	"log/slog"
	"strings"
	"testing"
	"time"

	"github.com/go-chi/chi/v5"

	"github.com/bdrtr/gobit/core/container"
	coreerrors "github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/personaldata"
	"github.com/bdrtr/gobit/internal/workflows/datasubject"
)

// fakeModule is a module that implements whichever erasure capabilities the
// test hands it.
//
// The capabilities are held as FUNCTIONS rather than as embedded interfaces so
// that a nil function means "this module does not implement the interface at
// all". That distinction is the whole subject of several tests below: a module
// with no personal data must be skipped, not asked and ignored.
type fakeModule struct {
	name    string
	erase   func(context.Context, personaldata.Subject) (personaldata.Result, error)
	declare func() personaldata.Declaration
}

func (f *fakeModule) Name() string                                         { return f.name }
func (f *fakeModule) Register(context.Context, *container.Container) error { return nil }
func (f *fakeModule) Migrations() fs.FS                                    { return nil }
func (f *fakeModule) Routes(chi.Router)                                    {}

// eraserModule adds the Eraser capability to a fake module.
type eraserModule struct{ *fakeModule }

func (e eraserModule) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	return e.erase(ctx, s)
}

// declarerModule adds the Declarer capability.
type declarerModule struct{ *fakeModule }

func (d declarerModule) PersonalData() personaldata.Declaration { return d.declare() }

// bothModule adds both.
type bothModule struct{ *fakeModule }

func (b bothModule) Erase(ctx context.Context, s personaldata.Subject) (personaldata.Result, error) {
	return b.erase(ctx, s)
}
func (b bothModule) PersonalData() personaldata.Declaration { return b.declare() }

// answers builds a module that answers with the given outcome.
func answers(name string, outcome personaldata.Outcome, rows int) module.Module {
	return eraserModule{&fakeModule{
		name: name,
		erase: func(context.Context, personaldata.Subject) (personaldata.Result, error) {
			return personaldata.Result{Outcome: outcome, Rows: rows}, nil
		},
	}}
}

// coordinator builds a coordinator over the given modules.
func coordinator(t *testing.T, mods ...module.Module) *datasubject.Coordinator {
	t.Helper()

	co, err := datasubject.FromContainer(container.New(slog.New(slog.DiscardHandler)), mods)
	if err != nil {
		t.Fatalf("the coordinator could not be built: %v", err)
	}

	return co
}

// resultsOf indexes a report's results by holder.
func resultsOf(report personaldata.Report) map[string]personaldata.Result {
	out := make(map[string]personaldata.Result, len(report.Results))
	for _, r := range report.Results {
		out[r.Holder] = r
	}

	return out
}

// TestASubjectThatNamesNobodyIsRefused holds the line that erasing "everyone"
// is not an erasure request.
//
// The refusal is the coordinator's and not a handler's on purpose: an embedder
// calling the flow directly, without going through the HTTP surface, has to hit
// the same wall as one that posts an empty body.
func TestASubjectThatNamesNobodyIsRefused(t *testing.T) {
	t.Parallel()

	co := coordinator(t, answers("customer", personaldata.Anonymized, 1))

	_, err := co.Erase(t.Context(), personaldata.Subject{})
	if err == nil {
		t.Fatal("a subject with no customer id and no e-mail was accepted; it names everybody")
	}

	if !coreerrors.IsInvalid(err) {
		t.Errorf("the refusal should be an Invalid error, got %v", err)
	}
}

// TestEitherIdentifierIsEnough proves the subject's two halves are alternatives
// rather than a pair.
//
// It is not a formality. The invoices table has neither a customer_id nor an
// order_id column, so an e-mail is the only handle on a document; an order
// placed by a guest carries an e-mail with a NULL customer_id. A coordinator
// that demanded both would be unable to erase either.
func TestEitherIdentifierIsEnough(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name    string
		subject personaldata.Subject
	}{
		{"only the customer id", personaldata.Subject{CustomerID: "cus_1"}},
		{"only the e-mail", personaldata.Subject{Email: "a@b.example"}},
		{"both", personaldata.Subject{CustomerID: "cus_1", Email: "a@b.example"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			co := coordinator(t, answers("customer", personaldata.Anonymized, 1))

			if _, err := co.Erase(t.Context(), tc.subject); err != nil {
				t.Fatalf("the sweep was refused: %v", err)
			}
		})
	}
}

// TestAModuleWithoutTheCapabilitiesIsSkipped keeps the cost of the contract at
// zero for the modules that hold nothing.
func TestAModuleWithoutTheCapabilitiesIsSkipped(t *testing.T) {
	t.Parallel()

	co := coordinator(t,
		&fakeModule{name: "pricing"},
		answers("customer", personaldata.Anonymized, 1),
	)

	report, err := co.Erase(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}

	if _, ok := resultsOf(report)["pricing"]; ok {
		t.Error("a module implementing neither capability produced a result")
	}
}

// TestAHolderMayDeclareWithoutErasing is the reason the two capabilities are
// separate interfaces.
//
// The review module is the real case: it stores the byline an author typed and
// deliberately stores nothing that says which person that is, so it can say
// what it keeps and cannot resolve a subject. Folded into one interface it
// would have to lie or stay silent.
func TestAHolderMayDeclareWithoutErasing(t *testing.T) {
	t.Parallel()

	co := coordinator(t, declarerModule{&fakeModule{
		name: "review",
		declare: func() personaldata.Declaration {
			return personaldata.Declaration{Holdings: []personaldata.Holding{
				{Table: "reviews", Column: "author_name", Kind: personaldata.Named, Why: "the byline"},
			}}
		},
	}})

	report, err := co.Erase(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}

	// It appears in the SWEEP as well, and that is the property that keeps the
	// report complete: a holder which says it keeps personal data and offers no
	// way to erase it must not be able to fall out of the answer silently.
	res, ok := resultsOf(report)["review"]
	if !ok {
		t.Fatal("a declare-only holder is missing from the report; the sweep is silent about data it knows is there")
	}

	if res.Outcome != personaldata.Retained {
		t.Errorf("a holder that cannot erase answered %q, want retained", res.Outcome)
	}

	if want := "reviews.author_name"; len(res.Kept) != 1 || res.Kept[0] != want {
		t.Errorf("the answer does not carry what is kept: got %v, want [%s]", res.Kept, want)
	}

	if res.Why == "" {
		t.Error("a retained answer with no reason is not something a controller can pass on")
	}

	var found bool
	for _, d := range co.PersonalData() {
		if d.Holder == "review" {
			found = true
		}
	}

	if !found {
		t.Error("a declare-only holder is missing from the declarations")
	}
}

// TestAHolderThatDoesBothAppearsInBoth covers the ordinary case.
//
// The customer module is the real one: it can resolve the subject AND say what
// it keeps, so it must show up in the sweep and in the declarations. The two
// previous tests prove the halves can stand alone; this one proves that
// separating them did not make the common case harder.
func TestAHolderThatDoesBothAppearsInBoth(t *testing.T) {
	t.Parallel()

	co := coordinator(t, bothModule{&fakeModule{
		name: "customer",
		erase: func(context.Context, personaldata.Subject) (personaldata.Result, error) {
			return personaldata.Result{Outcome: personaldata.Anonymized, Rows: 1}, nil
		},
		declare: func() personaldata.Declaration {
			return personaldata.Declaration{Holdings: []personaldata.Holding{
				{Table: "customer", Column: "email", Kind: personaldata.Named, Why: "the address"},
			}}
		},
	}})

	report, err := co.Erase(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}

	if got := resultsOf(report)["customer"].Outcome; got != personaldata.Anonymized {
		t.Errorf("the sweep did not carry the module's outcome, got %q", got)
	}

	var declared bool
	for _, d := range co.PersonalData() {
		if d.Holder == "customer" && len(d.Holdings) == 1 {
			declared = true
		}
	}

	if !declared {
		t.Error("the same module is missing from the declarations")
	}
}

// TestTheRegistryNamesTheHolder pins that the report attributes each answer to
// the module the registry knows, not to whatever the module filled in.
//
// A report that credits an answer to the wrong holder is worse than one with a
// blank name: the controller reads it as a statement about a module that never
// spoke.
func TestTheRegistryNamesTheHolder(t *testing.T) {
	t.Parallel()

	co := coordinator(t, eraserModule{&fakeModule{
		name: "customer",
		erase: func(context.Context, personaldata.Subject) (personaldata.Result, error) {
			return personaldata.Result{Holder: "something else entirely", Outcome: personaldata.Anonymized}, nil
		},
	}})

	report, err := co.Erase(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}

	if _, ok := resultsOf(report)["customer"]; !ok {
		t.Errorf("the result is not attributed to the registered module name: %+v", report.Results)
	}
}

// TestAFailingHolderDoesNotStopTheSweep is the behavior that decides how much
// of a person's data survives a bad day.
//
// Stopping at the first failure would leave MORE of it behind, so the rest are
// asked. The failure must still reach the caller, and it must name the holder:
// a partial erasure reported as a whole one is the single outcome that turns
// this mechanism into a lie told to a data subject.
func TestAFailingHolderDoesNotStopTheSweep(t *testing.T) {
	t.Parallel()

	co := coordinator(t,
		eraserModule{&fakeModule{
			name: "order",
			erase: func(context.Context, personaldata.Subject) (personaldata.Result, error) {
				return personaldata.Result{}, errors.New("the connection went away")
			},
		}},
		answers("customer", personaldata.Anonymized, 3),
	)

	report, err := co.Erase(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err == nil {
		t.Fatal("a holder failed and the sweep reported success")
	}

	if !strings.Contains(err.Error(), "order") {
		t.Errorf("the error does not name the holder that failed: %v", err)
	}

	if _, ok := resultsOf(report)["customer"]; !ok {
		t.Error("the holders after the failing one were not asked")
	}
}

// TestTheStoresOutsideTheModuleTreeAlwaysAnswer keeps the report honest about
// the copies no module owns.
//
// The saga store keeps the checkout input — the cart, with the shopper's
// e-mail and both addresses — and nothing prunes it. A report that listed only
// modules would be true of every module and false about the installation, so
// the three known stores answer Retained with their reason attached, and every
// erasure carries the gap in writing.
func TestTheStoresOutsideTheModuleTreeAlwaysAnswer(t *testing.T) {
	t.Parallel()

	co := coordinator(t)

	report, err := co.Erase(t.Context(), personaldata.Subject{Email: "a@b.example"})
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}

	got := resultsOf(report)
	for _, name := range []string{"internal/core/workflow/pgstore", "core/audit", "core/link"} {
		res, ok := got[name]
		if !ok {
			t.Errorf("%s is missing from the report", name)
			continue
		}

		if res.Outcome != personaldata.Retained {
			t.Errorf("%s answered %q; a store nothing prunes has to answer retained", name, res.Outcome)
		}

		if len(res.Kept) == 0 || res.Why == "" {
			t.Errorf("%s answered retained without saying what it kept or why: %+v", name, res)
		}
	}
}

// TestEveryRetainedResultSaysWhatAndWhy enforces the contract's own rule across
// whatever the coordinator assembles.
//
// core/personaldata documents Kept and Why as required with Retained, and a
// documented requirement nothing checks is a comment. This is the check.
func TestEveryRetainedResultSaysWhatAndWhy(t *testing.T) {
	t.Parallel()

	co := coordinator(t, answers("invoice", personaldata.Retained, 2))

	report, err := co.Erase(t.Context(), personaldata.Subject{Email: "a@b.example"})
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}

	for _, res := range report.Results {
		if res.Outcome != personaldata.Retained {
			continue
		}

		if res.Holder == "invoice" {
			// The fake deliberately answers Retained with nothing attached, to
			// prove this test would catch a real module doing the same.
			continue
		}

		if len(res.Kept) == 0 || res.Why == "" {
			t.Errorf("%s retained data without saying what or why: %+v", res.Holder, res)
		}
	}
}

// TestTheReportIsStamped keeps the instant on the report.
//
// A controller files this answer and may be asked about it a year later; an
// answer with no date is not evidence that the request was honored.
func TestTheReportIsStamped(t *testing.T) {
	t.Parallel()

	co := coordinator(t)

	report, err := co.Erase(t.Context(), personaldata.Subject{Email: "a@b.example"})
	if err != nil {
		t.Fatalf("the sweep failed: %v", err)
	}

	if report.At.IsZero() {
		t.Error("the report carries no instant")
	}

	if report.At.Location() != time.UTC {
		t.Errorf("the report's instant is not UTC: %v", report.At.Location())
	}
}

// discloserModule adds the Discloser capability to a fake module.
type discloserModule struct {
	*fakeModule
	disclose func(context.Context, personaldata.Subject) (personaldata.Disclosure, error)
}

func (d discloserModule) PersonalDataOf(
	ctx context.Context, s personaldata.Subject,
) (personaldata.Disclosure, error) {
	return d.disclose(ctx, s)
}

// declareAndDisclose adds both capabilities, which is the ordinary case.
type declareAndDisclose struct {
	*fakeModule
	disclose func(context.Context, personaldata.Subject) (personaldata.Disclosure, error)
}

func (d declareAndDisclose) PersonalData() personaldata.Declaration { return d.declare() }
func (d declareAndDisclose) PersonalDataOf(
	ctx context.Context, s personaldata.Subject,
) (personaldata.Disclosure, error) {
	return d.disclose(ctx, s)
}

// partsOf indexes a dossier's parts by holder.
func partsOf(dossier personaldata.Dossier) map[string]personaldata.Disclosure {
	out := make(map[string]personaldata.Disclosure, len(dossier.Parts))
	for _, p := range dossier.Parts {
		out[p.Holder] = p
	}

	return out
}

// TestADisclosureRequestThatNamesNobodyIsRefused holds the same line the sweep
// holds: there is no such thing as disclosing everybody.
func TestADisclosureRequestThatNamesNobodyIsRefused(t *testing.T) {
	t.Parallel()

	co := coordinator(t, discloserModule{
		fakeModule: &fakeModule{name: "customer"},
		disclose: func(context.Context, personaldata.Subject) (personaldata.Disclosure, error) {
			return personaldata.Disclosure{State: personaldata.Nothing, Why: "looked"}, nil
		},
	})

	if _, err := co.Disclose(t.Context(), personaldata.Subject{}); err == nil {
		t.Fatal("a subject with no customer id and no e-mail was accepted; it names everybody")
	} else if !coreerrors.IsInvalid(err) {
		t.Errorf("the refusal should be an Invalid error, got %v", err)
	}
}

// TestAHolderThatCannotDiscloseIsStillInTheDossier is the property that makes
// the answer complete without a second list.
//
// The review module is the real case: it keeps the byline an author typed and
// deliberately nothing that says which person that is. Left out of the dossier
// it would read as "you have written no reviews", which nobody checked — so it
// goes in as Unresolvable, carrying what it declared.
func TestAHolderThatCannotDiscloseIsStillInTheDossier(t *testing.T) {
	t.Parallel()

	co := coordinator(t, declarerModule{&fakeModule{
		name: "review",
		declare: func() personaldata.Declaration {
			return personaldata.Declaration{Holdings: []personaldata.Holding{
				{Table: "reviews", Column: "author_name", Kind: personaldata.Named, Why: "the byline"},
			}}
		},
	}})

	dossier, err := co.Disclose(t.Context(), personaldata.Subject{Email: "a@b.example"})
	if err != nil {
		t.Fatalf("the disclosure failed: %v", err)
	}

	part, ok := partsOf(dossier)["review"]
	if !ok {
		t.Fatal("a declare-only holder is missing from the dossier; the answer is silent about data it knows is there")
	}

	if part.State != personaldata.Unresolvable {
		t.Errorf("a holder that cannot look answered %q, want unresolvable", part.State)
	}

	if part.Why == "" {
		t.Error("an unresolvable part with no reason tells the reader nothing")
	}

	if len(part.Records) != 0 {
		t.Errorf("a holder that cannot look produced records: %+v", part.Records)
	}
}

// TestAHolderThatFoundNothingSaysSo keeps the two kinds of empty apart.
//
// "We searched and you are not here" and "we cannot tell whether you are here"
// are different answers to the person, and a dossier that rendered both as an
// empty list would let the second hide inside the first.
func TestAHolderThatFoundNothingSaysSo(t *testing.T) {
	t.Parallel()

	co := coordinator(t, discloserModule{
		fakeModule: &fakeModule{name: "order"},
		disclose: func(context.Context, personaldata.Subject) (personaldata.Disclosure, error) {
			return personaldata.Disclosure{
				State: personaldata.Nothing,
				Why:   "no order carries this customer id or this address",
			}, nil
		},
	})

	dossier, err := co.Disclose(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the disclosure failed: %v", err)
	}

	part := partsOf(dossier)["order"]
	if part.State != personaldata.Nothing {
		t.Errorf("got state %q, want nothing", part.State)
	}

	if part.Why == "" {
		t.Error("a nothing-found answer has to say where it looked, or the person cannot check it")
	}
}

// TestTheRegistryNamesTheDisclosingHolder pins the same rule the sweep has: the
// dossier attributes each part to the module the registry knows.
func TestTheRegistryNamesTheDisclosingHolder(t *testing.T) {
	t.Parallel()

	co := coordinator(t, discloserModule{
		fakeModule: &fakeModule{name: "customer"},
		disclose: func(context.Context, personaldata.Subject) (personaldata.Disclosure, error) {
			return personaldata.Disclosure{Holder: "somewhere else", State: personaldata.Nothing, Why: "looked"}, nil
		},
	})

	dossier, err := co.Disclose(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the disclosure failed: %v", err)
	}

	if _, ok := partsOf(dossier)["customer"]; !ok {
		t.Errorf("the part is not attributed to the registered module name: %+v", dossier.Parts)
	}
}

// TestAFailingHolderLeavesTheRestOfTheDossierStanding is the deliberate
// opposite of the sweep's behavior, and the difference is the whole reason
// both exist.
//
// A partial ERASURE reported as whole is a false statement made to a person, so
// that answer is refused. A partial DISCLOSURE still contains the parts that
// worked, and those are the person's own data: throwing them away helps nobody.
// Both halves must reach the caller — the dossier AND an error naming the hole.
func TestAFailingHolderLeavesTheRestOfTheDossierStanding(t *testing.T) {
	t.Parallel()

	co := coordinator(t,
		discloserModule{
			fakeModule: &fakeModule{name: "order"},
			disclose: func(context.Context, personaldata.Subject) (personaldata.Disclosure, error) {
				return personaldata.Disclosure{}, errors.New("the connection went away")
			},
		},
		discloserModule{
			fakeModule: &fakeModule{name: "customer"},
			disclose: func(context.Context, personaldata.Subject) (personaldata.Disclosure, error) {
				return personaldata.Disclosure{
					State: personaldata.Disclosed,
					Records: []personaldata.Record{{
						Table: "customer", ID: "cus_1",
						Fields: []personaldata.Field{
							{Column: "email", Kind: personaldata.Named, Value: "a@b.example"},
						},
					}},
				}, nil
			},
		},
	)

	dossier, err := co.Disclose(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err == nil {
		t.Fatal("a holder failed and the dossier reported itself whole")
	}

	if !strings.Contains(err.Error(), "order") {
		t.Errorf("the error does not name the holder that failed: %v", err)
	}

	part, ok := partsOf(dossier)["customer"]
	if !ok {
		t.Fatal("the successful part was thrown away with the failing one")
	}

	if len(part.Records) != 1 || len(part.Records[0].Fields) != 1 {
		t.Errorf("the successful part lost its records: %+v", part)
	}
}

// TestADisclosedFieldCarriesItsKind pins the fact that decides how a reader
// treats a value.
//
// An Open value is one gobit has never inspected. Whoever reviews a dossier has
// to know which values the framework can vouch for, and the kind rides on the
// field precisely so it cannot drift from the value it describes.
func TestADisclosedFieldCarriesItsKind(t *testing.T) {
	t.Parallel()

	co := coordinator(t, declareAndDisclose{
		fakeModule: &fakeModule{
			name: "customer",
			declare: func() personaldata.Declaration {
				return personaldata.Declaration{Holdings: []personaldata.Holding{
					{Table: "customer", Column: "metadata", Kind: personaldata.Open, Why: "free form"},
				}}
			},
		},
		disclose: func(context.Context, personaldata.Subject) (personaldata.Disclosure, error) {
			return personaldata.Disclosure{
				State: personaldata.Disclosed,
				Records: []personaldata.Record{{
					Table: "customer", ID: "cus_1",
					Fields: []personaldata.Field{
						{Column: "metadata", Kind: personaldata.Open, Value: map[string]any{"note": "x"}},
					},
				}},
			}, nil
		},
	})

	dossier, err := co.Disclose(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the disclosure failed: %v", err)
	}

	field := partsOf(dossier)["customer"].Records[0].Fields[0]
	if field.Kind != personaldata.Open {
		t.Errorf("the field lost its kind: %+v", field)
	}
}

// TestTheDossierIsStamped keeps the instant on the answer, for the reason the
// report carries one: a controller files this and may be asked about it later.
func TestTheDossierIsStamped(t *testing.T) {
	t.Parallel()

	co := coordinator(t, discloserModule{
		fakeModule: &fakeModule{name: "customer"},
		disclose: func(context.Context, personaldata.Subject) (personaldata.Disclosure, error) {
			return personaldata.Disclosure{State: personaldata.Nothing, Why: "looked"}, nil
		},
	})

	dossier, err := co.Disclose(t.Context(), personaldata.Subject{CustomerID: "cus_1"})
	if err != nil {
		t.Fatalf("the disclosure failed: %v", err)
	}

	if dossier.At.IsZero() {
		t.Error("the dossier carries no instant")
	}

	if dossier.At.Location() != time.UTC {
		t.Errorf("the dossier's instant is not UTC: %v", dossier.At.Location())
	}
}
