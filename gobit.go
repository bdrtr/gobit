// Package gobit is the front door of the gobit commerce framework.
//
// A program that embeds gobit imports THIS package and nothing else from the
// framework unless it extends it:
//
//	func main() {
//		if err := gobit.New().Version(version).Main(os.Args[1:], os.Stdout); err != nil {
//			fmt.Fprintf(os.Stderr, "fatal: %v\n", err)
//			os.Exit(1)
//		}
//	}
//
// That is the whole starter. cmd/server in this repository is exactly those
// lines and is kept that way ON PURPOSE: it is the example, so it may not be
// allowed to do anything an outside program could not do.
//
// # What a program adds
//
// Its own modules, through [App.Add], and its own plugins, through [App.Use].
// Both are ADDITIVE — what gobit ships is registered either way. There is no
// way to remove a module, and that is deliberate: a module missing from an
// installation is a set of tables that does not exist, and letting a caller
// subtract one would make "which schema does gobit have" unanswerable.
//
// # Why the facade is this small
//
// Everything it hands over — [github.com/bdrtr/gobit/core/module.Module],
// [github.com/bdrtr/gobit/core/plugin.Plugin] — is already published and
// already documented where it lives (ADR 0026). Re-exporting those contracts
// here would double the surface without adding a capability, and the two copies
// would then have to be kept saying the same thing.
//
// The lifecycle behind this facade is NOT published. It is the part a customer
// project should not have to write, not a contract it should be able to depend
// on: an installation's boot order, its shutdown order and its startup refusals
// are things this repository has to stay free to change.
package gobit

import (
	"io"

	"github.com/bdrtr/gobit/core/module"
	"github.com/bdrtr/gobit/core/plugin"
	"github.com/bdrtr/gobit/internal/app"
)

// App is an installation being described, before it is run.
//
// It holds no resources: nothing is opened, connected or migrated until
// [App.Main] is called. Building one is therefore free, and a program may build
// it wherever reads best rather than where the process happens to start.
type App struct {
	opts app.Options
}

// New starts describing an installation.
func New() *App { return &App{} }

// Version sets what this build is called.
//
// It is reported in the startup log, in the generated OpenAPI document and as
// the service version on every trace, so it is worth filling in from the
// linker rather than leaving at "dev": those three are where an operator looks
// to answer "which build is this".
func (a *App) Version(v string) *App {
	a.opts.Version = v

	return a
}

// Add registers a module of the caller's own.
//
// The module is registered AFTER the ones gobit ships, and the registry refuses
// a name that is already taken rather than replacing it — a module cannot be
// swapped out from under the rest of the installation, because the modules that
// resolve it by name would not know.
func (a *App) Add(m module.Module) *App {
	a.opts.Modules = append(a.opts.Modules, m)

	return a
}

// Use installs a plugin of the caller's own.
//
// A plugin handed in here does NOT have to be named in the PLUGINS setting:
// naming is how an operator picks among the plugins compiled into the box, and
// a program that compiled this one in has already made that choice.
func (a *App) Use(p plugin.Plugin) *App {
	a.opts.Plugins = append(a.opts.Plugins, p)

	return a
}

// Main runs the installation and returns on the first error.
//
// With NO arguments it starts the server. With arguments it runs an operator
// subcommand — migrate, stuck, recover, jobs — against the same installation,
// which is why they take no configuration of their own. Starting the server has
// no verb, so no present or future subcommand can reach it by falling through.
//
// out carries the operator-facing report of the subcommands. Errors are
// RETURNED rather than printed: the calling program decides what an operator
// sees, and it is the only part of the process that knows whether there is a
// terminal on the other end.
func (a *App) Main(args []string, out io.Writer) error {
	return app.Main(args, out, a.opts)
}
