package datasubject

import (
	"context"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/personaldata"
)

// ServiceWorkflowStore is the saga store's name in the container.
//
// It is spelled out here rather than imported, because the composition root's
// copy of the name is unexported and a workflow may not import the root. The
// repetition is the ordinary price of resolution by name in this repository,
// and [TestTheSagaStoreIsReachedFromTheRealCompositionRoot] in internal/app is
// what keeps the two spellings equal.
const ServiceWorkflowStore = "core.workflow.store"

// outsideTheModuleTree returns the holders of personal data that are NOT
// modules, and therefore cannot be discovered by walking the registry.
//
// # Why they are in the report at all
//
// ADR 0029's declaration obligation is written in terms of modules, and taking
// that literally would have produced a report that is true about every module
// and false about the installation. Three stores outside the module tree hold
// personal data, they were measured rather than guessed, and none of them can
// be reached by a type assertion over module.Registry.Modules().
//
// Each of them answers [personaldata.Retained] with the reason written out. That is
// not a euphemism for "not built": it is the accurate word. The data is kept,
// gobit knows exactly what and where, and the decision to keep it — for now, in
// the workflow store's case — is recorded in ADR 0033 rather than left to be
// discovered. Every erasure report therefore carries the gap in writing, which
// is worth more than a sentence in a document nobody reads while answering a
// data subject.
//
// # What is deliberately NOT here
//
// The three plugins that hold personal data — webpush, webhookout and searchpg
// — are absent on purpose and it is not an oversight: each brings its own
// module through core/plugin's Host.AddModule, which adds it to the SAME
// registry the composition root walks. They are ordinary holders and answer for
// themselves, or they do not, and either way the audit sees them.
func outsideTheModuleTree(c *container.Container) []holder {
	return []holder{
		sagaStoreHolder(c),
		{
			name:     auditHolder,
			eraser:   staticHolder{name: auditHolder, why: auditWhy, kept: auditColumns()},
			declarer: staticHolder{name: auditHolder, why: auditWhy, holdings: auditHoldings()},
		},
		{
			name:     linkHolder,
			eraser:   staticHolder{name: linkHolder, why: linkWhy, kept: linkColumns()},
			declarer: staticHolder{name: linkHolder, why: linkWhy, holdings: linkHoldings()},
		},
	}
}

// sagaStoreHolder returns the saga store, asked for by name.
//
// This is the ONE thing the coordinator resolves from the container, and it is
// why FromContainer takes one. The store owns its own two tables and therefore
// owns the statement that empties them — the same rule that keeps a module's
// SQL inside the module — so the capability lives there and is reached here.
//
// # The fallback is not a degraded mode, it is a testability seam
//
// When the store is absent the holder still appears, declaring what those
// tables hold and answering RETAINED. That case is reachable only from a
// coordinator built on an empty container, which is what a unit test does; the
// real composition root always provides the store, and
// TestTheSagaStoreIsReachedFromTheRealCompositionRoot in internal/app fails the
// day it stops. Without that test the fallback would be exactly the silent hole
// this whole mechanism exists to prevent — a report that looks complete while
// one holder quietly answered from a stub.
func sagaStoreHolder(c *container.Container) holder {
	h := holder{name: workflowStoreHolder}

	if eraser, err := container.Resolve[personaldata.Eraser](c, ServiceWorkflowStore); err == nil {
		h.eraser = eraser
	}

	if declarer, err := container.Resolve[personaldata.Declarer](c, ServiceWorkflowStore); err == nil {
		h.declarer = declarer
	}

	if h.eraser == nil && h.declarer == nil {
		h.eraser = staticHolder{name: workflowStoreHolder, why: workflowStoreWhy, kept: workflowStoreColumns()}
		h.declarer = staticHolder{name: workflowStoreHolder, why: workflowStoreWhy, holdings: workflowStoreHoldings()}
	}

	return h
}

// The tables the two named stores keep the data in.
//
// They are constants because the same name appears in the Kept list and in the
// declaration, and a report whose two halves disagreed about a table name would
// be wrong in the least visible way possible.
const (
	tableWorkflowExecutions = "workflow_executions"
	tableWorkflowSteps      = "workflow_execution_steps"
	tableAuditLog           = "audit_log"
	tableLink               = "<link table>"
)

// The names the report uses for the three. They are package paths rather than
// module names because that is what they are — naming them "workflow" or
// "audit" would put them in the same namespace as modules and invite somebody
// to look for a module directory that does not exist.
const (
	workflowStoreHolder = "internal/core/workflow/pgstore"
	auditHolder         = "core/audit"
	linkHolder          = "core/link"
)

// staticHolder is a holder whose answer does not depend on the subject.
//
// It cannot count rows and does not pretend to: Rows stays zero, and
// [personaldata.Result.Rows] is documented as informational precisely so that a
// holder which cannot count is not forced to invent a number.
type staticHolder struct {
	name     string
	why      string
	kept     []string
	holdings []personaldata.Holding
}

// Erase answers Retained without looking anything up.
func (s staticHolder) Erase(_ context.Context, _ personaldata.Subject) (personaldata.Result, error) {
	return personaldata.Result{
		Holder:  s.name,
		Outcome: personaldata.Retained,
		Kept:    s.kept,
		Why:     s.why,
	}, nil
}

// PersonalData declares what the store holds.
func (s staticHolder) PersonalData() personaldata.Declaration {
	return personaldata.Declaration{Holder: s.name, Holdings: s.holdings}
}

// workflowStoreWhy is the fallback's answer, and it says what it is rather than
// what the store would say.
//
// The store ITSELF erases and declares now (see [sagaStoreHolder]); this text is
// only reached when the coordinator was built without one, which is a test. An
// earlier version of it described the store's contents and went stale the moment
// the real eraser landed — it was still claiming the input is never pruned after
// the pruning shipped. The fix is not a better description: a stand-in should
// not describe something it is standing in for.
const workflowStoreWhy = "the saga store could not be reached from the container, so nothing here was " +
	"searched or emptied. The store keeps each execution's input, which for the one workflow that runs " +
	"is the checkout plan and carries the shopper's e-mail address and both postal addresses. What it " +
	"holds and what an erasure does to it are the store's own answer to give; this entry exists so that " +
	"a coordinator built without it cannot pass over the store in silence."

// workflowStoreColumns names what the fallback reports as kept.
//
// The two `output` columns are deliberately absent and their removal is a
// measurement rather than a tidy-up: the execution's output and every step's
// output hold identifiers and amounts, so no shape written to them carries a
// person. Declaring a column that holds nobody sends a controller looking in the
// wrong place. The store's own declaration says the same three things, and the
// two agreeing is not an accident to be relied on — see the note on
// [workflowStoreWhy] about why this stand-in stays minimal.
func workflowStoreColumns() []string {
	return []string{
		tableWorkflowExecutions + ".input",
		tableWorkflowExecutions + ".failure",
		tableWorkflowSteps + ".failure",
	}
}

func workflowStoreHoldings() []personaldata.Holding {
	return []personaldata.Holding{
		{
			Table: tableWorkflowExecutions, Column: "input", Kind: personaldata.Named,
			Why: "the workflow's arguments; for checkout this is the cart, including the shopper's " +
				"e-mail address and the shipping and billing addresses in full",
		},
		{
			Table: tableWorkflowExecutions, Column: "failure", Kind: personaldata.Open,
			Why: "the error text of a failed run, which commonly echoes the input that caused it",
		},
		{
			Table: tableWorkflowSteps, Column: "failure", Kind: personaldata.Open,
			Why: "one step's error text, with the same echo problem as the execution's",
		},
	}
}

const auditWhy = "the audit log records WHICH staff member touched WHICH admin path, and an admin path " +
	"embeds the record's identifier — so a row reads as \"this named person opened that named shopper's " +
	"record\". Erasing it would destroy the evidence that the erasure itself was carried out, which is the " +
	"one record a controller is most likely to be asked for."

func auditColumns() []string {
	return []string{tableAuditLog + ".actor_id", tableAuditLog + ".path", tableAuditLog + ".request_id"}
}

func auditHoldings() []personaldata.Holding {
	return []personaldata.Holding{
		{
			Table: tableAuditLog, Column: "actor_id", Kind: personaldata.Named,
			Why: "the identifier of the staff member or API key that made the request",
		},
		{
			Table: tableAuditLog, Column: "path", Kind: personaldata.Named,
			Why: "the request path, and an admin path carries the identifier of the record it acted on",
		},
		{
			Table: tableAuditLog, Column: "request_id", Kind: personaldata.Open,
			Why: "joins the row to the process log lines of the same request, whatever those contain",
		},
	}
}

const linkWhy = "a link table binds one module's record to another's by bare identifier, and one of the " +
	"declared links — b2b_employee_customer — puts a customer id on the far side. The tables are created " +
	"at runtime from Go rather than by a migration, so they are invisible to every schema sweep, and the " +
	"identifier alone does not describe the person: it stops meaning anything once the customer record it " +
	"points at has been anonymized."

func linkColumns() []string {
	return []string{tableLink + ".from_id", tableLink + ".to_id"}
}

func linkHoldings() []personaldata.Holding {
	return []personaldata.Holding{
		{
			Table: tableLink, Column: "to_id", Kind: personaldata.Named,
			Why: "the identifier of the record on the far side of a link; for b2b_employee_customer " +
				"that is a customer id. The table name is chosen by whoever declares the link, so " +
				"there is no fixed name to give here",
		},
	}
}
