# ADR 0025 — gobit is a LIBRARY a project imports, not a template a project copies

- **Status:** Accepted (direction); the public surface is the open half
- **Date:** 2026-09-05
- **Phase:** after the roadmap

## Context

There are two ways to ship a commerce framework in Go, and the repository has so
far shipped neither on purpose.

The **template model**: a customer project clones the repository and edits it.
The cost arrives later — a bug found in one project is carried by hand to the
others, the copies drift, and "which one is current" stops having an answer.
That trade is only sound if the core will never be updated again, which for
commerce it will not be.

The **library model**: the core is an importable Go module, the customer project
is a small program that imports it, binds its own handlers and providers, and
compiles one binary. PocketBase is the reference in Go; Medusa is the same shape
in Node.

**gobit is today the template model, structurally and not by choice.** 100% of
the framework — 155,054 lines of non-test code — is under `internal/`, a tree Go
forbids any other module from importing. There is not one importable package
outside `internal/` and `cmd/`. So the only way to use gobit is to clone it and
edit `cmd/server`.

The sharpest consequence: `plugins/` sits outside `internal/` and looks like an
extension point, but every plugin in it imports `internal/core/plugin` to
satisfy the host contract. An out-of-tree plugin cannot. **The repository has a
plugin system that no third party can write a plugin for** — not because the
compile-time model was rejected, but because the contract it implements is
unreachable.

## Decision

**gobit becomes a library that a customer project imports.** The customer
project is a small program — an `app`, its hooks, its own modules and providers,
`Start()` — and it tracks the core as a dependency in `go.mod`.

## Why this is a carve-out and NOT a rewrite

The measurement, taken before deciding:

| candidate public package | lines |
| --- | --- |
| the module contract (`Module`, `Registry.Add`) | 168 |
| the provider contracts | 667 |
| the event surface | 1,086 |
| the typed error kinds | 235 |
| the container | 805 |
| **total** | **2,961 — 1.9% of the codebase** |

Every extension point the library model needs already exists and is already
exercised in production paths:

- provider registries resolved BY NAME, with the selection in configuration and
  an unknown name stopping the startup — payment, tax, notification and file all
  use it;
- an event bus with `Subscribe`, and an outbox (ADR 0023) so a promised event
  cannot be lost between the commit and the publish;
- `Registry.Add(mod Module)` — the method a project needs to add a module of its
  own, already the mechanism the composition root uses;
- migration merging per owner, which already refuses two owners claiming one
  table;
- a `metadata` jsonb on eleven modules' models, including product, variant and
  taxonomy;
- full-path route registration, which the admin panel already proves a fourth
  tree can do without touching core.

So the work is relocating and naming, not inventing. **A rewrite would discard
twenty-four ADRs, the arch guards that enforce them, and every fix this
repository measured its way to — and would arrive at the same place.** It is the
expensive path to an outcome the cheap path already reaches.

## What is decided and what is not

Decided: the direction, and that the transition is a carve-out.

**Not decided: which packages become public.** That is the whole risk, because
this repository already knows the rule — a field that enters a contract can
never be taken out again — and a public package is that rule at package scale.
The brief that prompted this decision says the same thing in its own words: keep
the public API small, use `internal/` generously.

The test to apply: **what fraction of the codebase has to leave `internal/`?**
The candidate set above is 1.9%. The module models are a further 7,058 lines
(4.6%) and are the largest permanent decision in the whole move — a published
struct field is forever, and eleven of the sixteen modules already carry a
`metadata` map that exists precisely so a project can extend a model WITHOUT the
struct changing.

If the answer creeps past a few percent, the surface is too big and the
generalisation is too early — which the same brief names, correctly, as being as
harmful as forking.

## Consequences

- **Semver starts meaning what it says.** There are tags through `v0.8.0`, but
  with no importable package they version an APPLICATION: nothing downstream can
  break because nothing downstream can compile. The first public package makes
  the tags real and makes `/v2` a real cost.
- **An out-of-tree plugin becomes possible**, which is the concrete test of
  whether the surface is right: if `ecom-iyzico` can be written outside this
  repository against published contracts, the carve-out worked.
- **`cmd/server` becomes the starter's contents.** Its 3,518 lines are the
  lifecycle a customer project should not have to write; they move behind an
  `app` package rather than being published as they stand.
- **The arch guards have to learn the new boundary.** They currently enforce
  that modules do not import each other and that workflows import no module;
  they will additionally have to enforce that a public package does not reach
  back into something that stayed internal.

## Reopening the decision

Reopen if the first two or three customer projects show that the surface is
missing something they genuinely cannot express — which is the moment the brief
names as the one that decides the boundary. Widening a surface is cheap;
narrowing one is not, so the bias is to publish late.
