package app

import (
	"log/slog"
	"strings"
	"testing"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/erasure"
	"github.com/bdrtr/gobit/internal/core/workflow/pgstore"
	"github.com/bdrtr/gobit/internal/workflows/erasing"
)

// TestTheSagaStoreServiceNameMatchesTheCompositionRoot keeps two spellings of
// one name equal.
//
// The erasing flow cannot import this package, so it repeats the container name
// of the saga store as its own constant — the ordinary price of resolution by
// name in this repository. What is NOT ordinary is the failure mode: if the
// root renamed the service, the flow's resolution would simply not find it, and
// the coordinator would fall back to a declaration-only stub. Nothing would
// fail to compile, no test would go red, and every erasure report would keep
// listing the saga store while quietly no longer erasing anything in it.
func TestTheSagaStoreServiceNameMatchesTheCompositionRoot(t *testing.T) {
	t.Parallel()

	if erasing.ServiceWorkflowStore != svcWorkflowStore {
		t.Errorf("the erasing flow asks for %q and the composition root provides %q.\n"+
			"The lookup would miss, the coordinator would answer from its stub, and the "+
			"erasure report would say the saga store was swept when it was not.",
			erasing.ServiceWorkflowStore, svcWorkflowStore)
	}
}

// TestTheSagaStoreIsReachedFromTheRealCompositionRoot proves the coordinator
// reaches the REAL store rather than its fallback.
//
// # Why a fallback exists at all
//
// The coordinator is constructible without a database, because unit tests build
// it that way; when the store is absent it still declares what the saga tables
// hold and answers RETAINED. That stub is a testability seam and it is also
// exactly the shape of a silent hole — a report that looks complete while one
// holder answered from a placeholder. This test is what makes the seam safe.
//
// # How the two are told apart
//
// A store built on a nil pool is a real store whose every method fails with
// KindUnavailable (its constructor's godoc says so, and returning an error
// rather than a value is the contract). The stub cannot fail. So a sweep over a
// container carrying a poolless store must report the saga store as a FAILURE:
// if it comes back Retained instead, the real eraser was never reached.
func TestTheSagaStoreIsReachedFromTheRealCompositionRoot(t *testing.T) {
	t.Parallel()

	log := slog.New(slog.DiscardHandler)

	c := container.New(log)
	if err := c.Provide(svcWorkflowStore, pgstore.New(nil, log)); err != nil {
		t.Fatalf("the saga store could not be provided: %v", err)
	}

	co, err := erasing.FromContainer(c, nil)
	if err != nil {
		t.Fatalf("the coordinator could not be built: %v", err)
	}

	report, err := co.Erase(t.Context(), erasure.Subject{Email: "someone@example.test"})
	if err == nil {
		t.Fatal("the poolless store answered without failing; the coordinator is using its stub, " +
			"so every report would claim a sweep that did not happen")
	}

	if !strings.Contains(err.Error(), pgstore.ErasureHolder) {
		t.Errorf("the failure does not name the saga store: %v", err)
	}

	for _, res := range report.Results {
		if res.Holder == pgstore.ErasureHolder {
			t.Errorf("the saga store produced a result as well as a failure: %+v", res)
		}
	}
}

// TestTheSagaStoreDeclaresWhatItHolds pins the declaration to the store itself.
//
// The coordinator carried this declaration as a literal before the store could
// answer for itself, and the literal was WRONG in a way nobody would have
// noticed: it listed both `output` columns as possibly personal, because nobody
// had looked at what goes into them. They hold identifiers and amounts. Reading
// the declaration from the store is what stops the next such guess.
func TestTheSagaStoreDeclaresWhatItHolds(t *testing.T) {
	t.Parallel()

	declarer, ok := pgstore.New(nil, slog.New(slog.DiscardHandler)).(erasure.Declarer)
	if !ok {
		t.Fatal("the saga store no longer declares its personal data; it would vanish from every report")
	}

	holdings := declarer.PersonalData().Holdings
	if len(holdings) == 0 {
		t.Fatal("the saga store declares nothing, yet it stores the checkout plan")
	}

	var sawInput bool

	for _, h := range holdings {
		if h.Table == "workflow_executions" && h.Column == "input" {
			sawInput = true

			if h.Kind != erasure.Named {
				t.Errorf("the input column is declared %q; gobit writes the person there itself", h.Kind)
			}
		}

		if h.Column == "output" {
			t.Errorf("%s.%s is declared as personal; measured, the outputs hold identifiers and "+
				"amounts, and declaring a column that holds nobody sends a controller looking "+
				"in the wrong place", h.Table, h.Column)
		}
	}

	if !sawInput {
		t.Error("workflow_executions.input is not declared, and it is where the checkout plan " +
			"keeps the shopper's e-mail address and both postal addresses")
	}
}
