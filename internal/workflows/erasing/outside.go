package erasing

import (
	"context"

	"github.com/bdrtr/gobit/core/erasure"
)

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
// Each of them answers [erasure.Retained] with the reason written out. That is
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
func outsideTheModuleTree() []holder {
	return []holder{
		{
			name:     workflowStoreHolder,
			eraser:   staticHolder{name: workflowStoreHolder, why: workflowStoreWhy, kept: workflowStoreColumns()},
			declarer: staticHolder{name: workflowStoreHolder, why: workflowStoreWhy, holdings: workflowStoreHoldings()},
		},
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
// [erasure.Result.Rows] is documented as informational precisely so that a
// holder which cannot count is not forced to invent a number.
type staticHolder struct {
	name     string
	why      string
	kept     []string
	holdings []erasure.Holding
}

// Erase answers Retained without looking anything up.
func (s staticHolder) Erase(_ context.Context, _ erasure.Subject) (erasure.Result, error) {
	return erasure.Result{
		Holder:  s.name,
		Outcome: erasure.Retained,
		Kept:    s.kept,
		Why:     s.why,
	}, nil
}

// PersonalData declares what the store holds.
func (s staticHolder) PersonalData() erasure.Declaration {
	return erasure.Declaration{Holder: s.name, Holdings: s.holdings}
}

const workflowStoreWhy = "the saga store keeps each execution's input, and the checkout workflow's input is " +
	"the cart — which carries the shopper's e-mail address and both postal addresses. A RUNNING execution " +
	"needs its input to compensate (ADR 0017), so the input cannot simply be dropped; a TERMINAL execution's " +
	"input is not needed and is not pruned today either. Nothing in this repository ever deletes from " +
	"workflow_executions outside a test. Until that pruning exists, an erasure leaves this copy behind."

func workflowStoreColumns() []string {
	return []string{
		tableWorkflowExecutions + ".input",
		tableWorkflowExecutions + ".output",
		tableWorkflowExecutions + ".failure",
		tableWorkflowSteps + ".output",
		tableWorkflowSteps + ".failure",
	}
}

func workflowStoreHoldings() []erasure.Holding {
	return []erasure.Holding{
		{
			Table: tableWorkflowExecutions, Column: "input", Kind: erasure.Named,
			Why: "the workflow's arguments; for checkout this is the cart, including the shopper's " +
				"e-mail address and the shipping and billing addresses in full",
		},
		{
			Table: tableWorkflowExecutions, Column: "output", Kind: erasure.Open,
			Why: "the workflow's result, whose shape each workflow chooses",
		},
		{
			Table: tableWorkflowExecutions, Column: "failure", Kind: erasure.Open,
			Why: "the error text of a failed run, which commonly echoes the input that caused it",
		},
		{
			Table: tableWorkflowSteps, Column: "output", Kind: erasure.Open,
			Why: "one step's result; a step that read the customer returns what it read",
		},
		{
			Table: tableWorkflowSteps, Column: "failure", Kind: erasure.Open,
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

func auditHoldings() []erasure.Holding {
	return []erasure.Holding{
		{
			Table: tableAuditLog, Column: "actor_id", Kind: erasure.Named,
			Why: "the identifier of the staff member or API key that made the request",
		},
		{
			Table: tableAuditLog, Column: "path", Kind: erasure.Named,
			Why: "the request path, and an admin path carries the identifier of the record it acted on",
		},
		{
			Table: tableAuditLog, Column: "request_id", Kind: erasure.Open,
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

func linkHoldings() []erasure.Holding {
	return []erasure.Holding{
		{
			Table: tableLink, Column: "to_id", Kind: erasure.Named,
			Why: "the identifier of the record on the far side of a link; for b2b_employee_customer " +
				"that is a customer id. The table name is chosen by whoever declares the link, so " +
				"there is no fixed name to give here",
		},
	}
}
