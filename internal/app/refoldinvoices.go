package app

import (
	"context"
	"fmt"
	"io"
	"log/slog"
	"os"
	"os/signal"
	"syscall"

	"github.com/bdrtr/gobit/core/container"
	"github.com/bdrtr/gobit/core/errorreport"
	"github.com/bdrtr/gobit/core/errors"
	"github.com/bdrtr/gobit/internal/core/config"
	"github.com/bdrtr/gobit/internal/modules/invoice"
	invoicesvc "github.com/bdrtr/gobit/internal/modules/invoice/service"
)

// refoldInvoicesCommand corrects the buyer-address handles migration 000003
// could not correct by itself.
//
// # Why a command and not a step at boot
//
// The pass is needed exactly once per installation, and only by an installation
// that has invoices whose buyer address is not plain ASCII. Running it at every
// boot would put a full table walk in the startup path of every shop in the
// world to fix a row most of them do not have; running it inside the migration
// is not possible at all, because a migration is SQL and SQL is the thing that
// cannot fold reliably here (000003 has the measurement).
//
// # Why it needs no -confirm, unlike recover and migrate down
//
// Those two are irreversible: one compensates real side effects, the other drops
// schema. This one is idempotent and CONVERGENT — it computes what the handle
// should be and writes it only where it differs, so running it twice, or by
// accident, or on an installation that never had the defect, leaves the database
// exactly as it found it. Asking an operator to repeat a word to authorize
// something that cannot go wrong teaches them to type past the word that guards
// something that can.
const refoldInvoicesCommand = "refold-invoices"

// codeRefoldFailed reports that the re-fold pass could not finish.
const codeRefoldFailed = "app_refold_failed"

// invoiceRefolder is the invoice service as this command needs it.
//
// The service is taken through a NARROW interface rather than by its concrete
// type, the same way [paymentReconciler] takes the payment service: the
// composition root states what a dependency is FOR, and a root that resolved
// whole services would make every command look like it could do anything the
// module can.
type invoiceRefolder interface {
	RefoldBuyerEmails(ctx context.Context) (invoicesvc.RefoldReport, error)
}

// runRefoldInvoices brings the application up and runs the pass.
//
// It reuses [config.Load] and [openApplication] for the reason every other
// command here does: run inside the running container it is already configured,
// and it cannot be pointed at the wrong installation by accident. Nothing is
// served.
func runRefoldInvoices(args []string, out io.Writer, opts Options) error {
	if len(args) > 0 {
		return errors.Invalid(codeUsage,
			"%s takes no arguments; it corrects every document that needs it and reports the count",
			refoldInvoicesCommand)
	}

	cfg, err := config.Load()
	if err != nil {
		return err
	}

	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	defer stop()

	// Log lines go to stderr so that stdout carries only the report; the same
	// split every other command in this binary makes.
	log := slog.New(slog.NewTextHandler(os.Stderr, &slog.HandlerOptions{Level: slog.LevelWarn}))

	app, closeApp, err := openApplication(ctx, cfg, log, errorreport.NewSink(), opts)
	if err != nil {
		return err
	}
	defer closeApp()

	return refoldInvoices(ctx, app.container, out)
}

// refoldInvoices resolves the invoice service and runs the pass.
//
// Split from [runRefoldInvoices] so that the decision can be tested without a
// process, which is the same split [recoverExecution] makes.
func refoldInvoices(ctx context.Context, c *container.Container, out io.Writer) error {
	svc, err := container.Resolve[invoiceRefolder](c, invoice.ServiceName)
	if err != nil {
		return errors.Wrap(err, errors.KindOf(err), codeRefoldFailed,
			"the invoice service %q could not be resolved; this command needs the invoice module installed",
			invoice.ServiceName)
	}

	report, err := svc.RefoldBuyerEmails(ctx)

	// The report is printed even when the pass failed part way. It writes as it
	// goes, so the rows it already corrected stay corrected, and an operator told
	// only that it failed would not know whether to expect a half-done table.
	if writeErr := writeReport(out, fmt.Sprintf(
		"examined %d document(s) carrying a buyer address; rewrote %d handle(s)\n",
		report.Examined, report.Rewritten)); writeErr != nil {
		return writeErr
	}

	return err
}
