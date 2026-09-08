# Importable core, thin application — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

> **Acted on 2026-09-05, and the measurement below is now HISTORY.** The twelve
> packages an extension author needs were promoted out of `internal/` to
> `core/` (ADR 0026), and the composition root moved out of `package main` to
> `internal/app` behind a four-method facade at the module root (ADR 0027).
> `cmd/server` is fifteen lines. An out-of-tree plugin is proved by compilation
> in `examples/plugin`, and an out-of-tree application by `examples/starter`,
> which the audit both compiles and runs.

The brief: ship the core as an IMPORTABLE Go module the way PocketBase does —
`go get`, `app := New()`, bind hooks, compile one binary — with a thin starter
repository per customer project. Stay away from the fork model. Open the
extension points that make forking unnecessary: provider interfaces, hooks over
the domain events, router access, merged migrations, a `metadata` jsonb on the
models. Compile-time plugin registration, Caddy-style. Keep the public API
small; take semver seriously.

### The finding that governs everything else

**gobit is a fork-model repository today, structurally, and not by choice —
100% of the framework is under `internal/`.** 531 files and 143,872 lines of
non-test code, all of it in a tree Go forbids any other module from importing.
There is not one importable package outside `internal/` and `cmd/`.

So the current answer to "how does a customer project use gobit" is: clone it
and edit `cmd/server`. That is the fork model, with every consequence the brief
names — a fix found in one project carried by hand to the others, and no
answer to "which one is current".

Moving to the PocketBase shape is therefore **not a matter of adding an `App`
struct.** The struct is the easy part. The work is deciding which packages stop
being internal, because that decision is the one this repository already knows
it cannot walk back: its own recurring sentence is that a field which enters a
contract can never be taken out again. The brief says the same thing — keep the
public API small — and the two agree completely.

### The good news: the mechanisms all exist, in the wrong place

Every extension point the brief asks for is already built. None of it needs
inventing; it needs relocating and naming.

| the brief asks for | what exists today | where it lives |
| --- | --- | --- |
| `PaymentProvider`, `TaxCalculator`, … | provider registries resolved BY NAME, selection in config, unknown name stops the startup | `core/provider`, per-module registries |
| hooks over domain events | an event bus with `Subscribe`, domain events, and an outbox so a promised event cannot be lost (ADR 0023) | `core/eventbus` |
| router access | chi; every module registers its own full paths, and the panel proves a fourth tree can mount its own | `core/http`, `Module.Routes` |
| merged migrations | the registry already merges per-owner migration sources and refuses two owners claiming one table | `core/module`, `internal/app/migrate.go` |
| a project can add its own module | `Registry.Add(mod Module)` — the exact method the brief needs | `core/module` |
| `Product.Metadata` jsonb | present on auth, cart, customer, fulfillment, invoice, order, payment, product, promotion and tax — inside product it reaches the variant and taxonomy tables too | ~~eleven modules~~ **ten; corrected 2026-09-06** — variant and taxonomy are product's OWN tables rather than modules of their own, and promotion, which has the column, was left out |
| compile-time plugin registration | a plugin host with an Install phase, selected by configuration | `core/plugin`, `plugins/` |

### The plugin tree is not the extension point it looks like

`plugins/` sits outside `internal/` and is importable — and **every plugin in it
imports `core/plugin` to satisfy the host contract.** An out-of-tree
plugin cannot do that. So the plugin system works for plugins that live in this
repository and for no others, which means the Caddy model the brief names
(`import _ "github.com/user/ecom-iyzico"`, register in `init()`) is not
currently possible — not because it was rejected, but because the contract it
would implement is unreachable.

That is the sharpest single consequence of the internal-only tree, and it is
worth stating plainly: **the repository has a plugin system that no third party
can write a plugin for.**

### What semver means today, and what it would mean after

There are tags through `v0.8.0` and the CHANGELOG is kept properly. But with no
importable package, those tags version an APPLICATION: nothing downstream can
break, because nothing downstream can compile against it. The moment a package
becomes public the tags start meaning what the brief wants them to mean, and
`/v2` becomes a real cost rather than a hypothetical one.

### The question this repository would have to answer first

Not "how do we build hooks" — they exist. It is: **which packages become the
public surface, and what is the smallest set that makes forking unnecessary?**

The brief's own warning is the right guide and matches this repository's
instincts: too-early generalisation is as harmful as forking. The honest first
step is small and testable — take ONE customer-shaped need, express it against
a public surface, and see what that surface had to expose. The candidates, in
the order the brief implies:

1. An `app` package with the lifecycle `cmd/server` already performs: build the
   registry, install plugins, bootstrap, serve. Today that logic is 1,200 lines
   of `internal/app/setup.go` that no external program can call.
2. The module contract (`Module`, `Registry.Add`) so a project can add a module
   of its own.
3. The provider contracts, so `ecom-iyzico` can exist out of tree.
4. The event surface for hooks.
5. The domain models — the largest and the most permanent decision, because a
   published struct field is forever.

Everything below that stays internal. The measurement to keep: what fraction of
143,872 lines has to leave `internal/`. If the answer is more than a few
percent, the surface is too big.

---
