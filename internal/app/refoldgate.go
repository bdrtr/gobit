package app

import (
	"context"
	"log/slog"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	invoicesvc "github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// invoiceHandleRefolder is the invoice service as the startup gate needs it.
//
// A narrow interface for the reason [paymentReconciler] is one: the composition
// root says what a dependency is FOR, and resolving whole services would make
// every step here look like it could do anything the module can.
type invoiceHandleRefolder interface {
	RefoldNonAsciiBuyerEmails(ctx context.Context) (invoicesvc.RefoldReport, error)
}

// refoldInvoiceHandles corrects, at startup, the buyer-address handles that
// migration 000003 could not correct by itself.
//
// # Why this is not left to an operator running a command
//
// It was, for one day, and that was the wrong call. 000003 added
// buyer_email_folded — the handle a data-subject erasure resolves a person by —
// and had to backfill it for documents already in the table. A migration is SQL,
// and SQL is the thing that cannot fold reliably here (ADR 0038 has the
// measurement), so the backfill is correct for ASCII and only for ASCII. The
// remedy was `gobit refold-invoices`, and an installation whose operator never
// ran it kept the defect.
//
// What that defect COSTS is why operator discipline is the wrong place for it: a
// person asks to be forgotten, the invoice module answers "we looked and you are
// not here" while holding their document, and nothing anywhere reports it. A
// silent, legally-consequential data fault is not a thing to leave to whoever
// reads a migration header.
//
// # Why it runs rather than refuses to boot
//
// Refusing would take a shop down over rows that only affect the completeness of
// an erasure answer, and it would refuse on every boot until a human intervened.
// The pass is CONVERGENT and idempotent — it computes what each handle should be
// and writes only where it differs — so running it is strictly safer than
// refusing, and after the first boot it writes nothing.
//
// # Why the cost is bounded
//
// It reads only the documents whose address is not pure ASCII, which is the only
// shape the SQL backfill can have got wrong. On the ordinary installation that
// set is empty and the gate costs one query.
//
// The command stays, and stays WIDER: it reads every document with an address,
// because a second disagreement — btrim() against Go's TrimSpace over tabs and
// newlines — is invisible to this scope. The gate is the floor, not the ceiling.
//
// # Why a failure here does not stop the boot
//
// The same reason core/db's case-folding probe only warns: an installation whose
// invoices are all ASCII has nothing wrong with it, and taking the process down
// because a maintenance read failed would turn a data-quality step into an outage.
// What is NOT acceptable is silence, so a failure is logged at ERROR with the
// command that finishes the job.
func refoldInvoiceHandles(ctx context.Context, c *container.Container, log *slog.Logger) {
	svc, err := container.Resolve[invoiceHandleRefolder](c, invoice.ServiceName)
	if err != nil {
		// The invoice module is not installed. gobit is a library and an
		// embedder chooses its modules, so this is the ordinary case for a shop
		// that issues no documents — not a fault.
		return
	}

	report, err := svc.RefoldNonAsciiBuyerEmails(ctx)
	if err != nil {
		log.ErrorContext(ctx, "the invoice buyer-address handles could not be checked at startup",
			"error", err,
			slog.Int("examined", report.Examined),
			slog.Int("rewritten", report.Rewritten),
			slog.String("effect", "an erasure request naming a non-ASCII address may report that "+
				"a person has no invoices while their document is held"),
			slog.String("fix", "run `gobit refold-invoices`, which reads every document rather "+
				"than only the non-ASCII ones"))

		return
	}

	if report.Rewritten == 0 {
		log.DebugContext(ctx, "the invoice buyer-address handles were already folded by Go",
			slog.Int("examined", report.Examined))

		return
	}

	// A rewrite is worth an INFO line and not a debug one: it says a
	// legally-consequential column was wrong until this boot, which is
	// something an operator reading their logs afterwards has to be able to find.
	log.InfoContext(ctx, "invoice buyer-address handles were re-folded at startup",
		slog.Int("examined", report.Examined),
		slog.Int("rewritten", report.Rewritten),
		slog.String("why", "migration 000003's backfill is written in SQL, which folds ASCII only "+
			"on a --locale=C cluster; these rows were resolvable by no erasure request until now"))
}
