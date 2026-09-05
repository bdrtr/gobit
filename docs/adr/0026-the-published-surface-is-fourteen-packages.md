# ADR 0026 — The published surface is core/: fourteen packages, and no commerce model is among them

- **Status:** Accepted
- **Date:** 2026-09-05
- **Phase:** after the roadmap
- **Closes:** the open half of ADR 0025

## Context

ADR 0025 decided that gobit is a library a project imports, and deliberately
left one question open: **which packages become public.** It also set the test
to apply — what fraction of the codebase has to leave internal/ — and warned
that a published name cannot be taken back.

The question was answered by measurement rather than by taste, because the
repository already contains the answer. Eight plugins are written against the
host contract today; whatever they import is, by definition, what an extension
author outside this repository would need. Adding what the transitive closure of
those imports drags in gives a surface that is derived, not chosen.

**The plugins import nine packages:** plugin, errors, provider, module, http,
db, container, eventbus, query.

**The closure of those nine adds exactly three** — audit, errorreport, link —
**and then stops.** It stops because of a rule this repository has enforced
since ADR 0001: the core knows no commerce module. The closure was checked for
that directly, and no package in it reaches internal/modules at all.

That is the fact the whole decision rests on. The thing ADR 0025 called "the
largest permanent decision in the whole move" — publishing the module models,
7,058 lines of struct fields that could never change again — turns out not to be
a decision at all. Nothing in the surface needs them.

## Decision

**The published tree is `core/`, and it holds fourteen packages.**

| package | why an outside program must name it |
| --- | --- |
| core/plugin | the host contract every plugin implements |
| core/module | the module contract, and the registry a project adds its own module to |
| core/provider | the payment, tax, notification, fulfillment and file contracts |
| core/errors | the typed kinds; a provider returns them and the transport maps them |
| core/http | routes, principals, and the one place an error response is written |
| core/http/redisguard | the shared rate-limit and idempotency store |
| core/db | the pool and transaction handle a module's repository takes |
| core/container | resolution by name — the only way a module reaches a service |
| core/eventbus | publish and subscribe |
| core/eventbus/outbox | a promised event that survives the commit (ADR 0023) |
| core/query | the cross-module read layer (ADR 0004) |
| core/link | link definitions (ADR 0005) |
| core/audit | the record an admin-facing route is expected to leave |
| core/errorreport | the reporter contract, which two plugins already implement |

**The rule for membership**, so the next case does not need a judgement call: a
package is published when a program OUTSIDE this repository must name it to
compile — to implement a provider, register a module, write a plugin, or boot
the server. Everything else stays under internal/, where it can still change.

**Everything else stays internal.** Seven core packages did not make the
closure and remain unpublished: config, job, logger, observability, openapi,
page, workflow. None of them is on a contract an outside program has to satisfy
today.

## Confronting the test ADR 0025 set

ADR 0025 said the surface is too big if it creeps past a few percent, and
estimated 1.9%. The measured answer is larger:

| tree | non-test lines | share |
| --- | --- | --- |
| core/ — published | 12,079 | **7.8%** |
| internal/core/ — stays internal | 8,928 | 5.8% |
| internal/modules/ — stays internal | 110,604 | 71.3% |
| repository, non-test | 155,062 | 100% |

The gap between 1.9% and 7.8% is not scope creep. **The 1.9% estimate was made
before the closure was computed, and it omitted six packages that a plugin in
this repository imports today** — http, db, query, link, audit and errorreport.
They were always required; they were simply not counted.

Two things keep the larger number honest:

- **The commitment that mattered is zero.** ADR 0025 named the module models as
  the permanent decision. 110,604 lines of them stay internal. Not one commerce
  struct is published.
- **A third of the surface is one package.** core/http is 4,409 of the 12,079
  lines, and most of it is machinery — the router, idempotency, rate limiting,
  CORS — rather than contract. Sixty-one of its eighty exported names are
  already used from outside it, so it is not a thin surface over a fat inside;
  splitting it is real work with a real risk of scattering the error-response
  rule that internal/arch enforces. It is named here as the one place the
  surface could later shrink, and it is not being done now.

## What this does NOT do

**An out-of-tree program still cannot boot gobit.** The composition root — 2,600
non-test lines in cmd/server — is still a program, not a library. Publishing the
contracts makes an out-of-tree PLUGIN possible; it does not yet make an
out-of-tree APPLICATION possible. That is the remaining half of ADR 0025 and it
is a separate step.

## Enforcement

Four audits, each mutation-proved:

- **The declared list.** internal/arch names the fourteen packages explicitly
  rather than discovering them, so publishing is an edit that shows up in a diff
  next to this ADR. A package appearing under core/ without being declared
  fails; a declared package with no files fails too, because a stale record of
  what was promised is worse than a missing one.
- **Self-containment.** No published package may import an internal one. Go's
  own internal/ rule does not catch this: the restriction is on where the
  importer sits, and a published package sits inside this module. The breakage
  lands downstream, where an outside program cannot name the internal type — a
  wall in its own code with no explanation on this side.
- **No escape.** Every Go package in the repository is internal, published, a
  command, or a plugin. A fifth kind — a helpers/ at the root, a pkg/ reached
  for out of habit — is importable by the world and audited by nothing.
- **The production tree list.** Seven audits each kept a private copy of the
  list of trees to scan, each with a comment saying it had to be edited by hand.
  This move added a tree and all seven narrowed at once, because a scan whose
  root list misses a tree finds nothing there and passes having found nothing.
  The list is now written once and checked against the repository.

That last one is the general lesson and it is worth stating separately: **a
detector's root list is DATA, and a bulk rename edits data silently.** The move
was mechanical everywhere except in the places where a path was a value rather
than a reference, and every one of those places failed open.

## Consequences

- **Semver starts meaning what it says.** The tags through v0.8.0 versioned an
  application; nothing downstream could break because nothing downstream could
  compile. Fourteen packages are now a promise, and /v2 is now a real cost.
- **An out-of-tree plugin is possible, and it is proved rather than claimed.**
  examples/plugin is a separate Go module — Go's own rule refuses it
  internal/ — and internal/arch COMPILES it. It registers a payment provider,
  mounts a route, subscribes to an event and returns a typed error using five
  published packages and nothing else. That compilation is the only check here
  that can catch a surface which is unusable rather than merely wrong: an
  unexported type in a contract, or a helper the caller needs that was never
  exported, is neither an import of internal/ nor a missing declaration.
- **Publishing more is cheap; publishing less is not.** the job package will have
  to be published the day a plugin can register a job, and that is a deliberate edit in
  two places, not a file move.
- **The eight in-tree plugins are now written against published contracts.**
  They were the measurement; they are now also the example.

## Reopening

Reopen if a customer project cannot express something without a package that
stayed internal — which is the moment ADR 0025 named as the one that decides the
boundary. Widening is cheap and narrowing is not, so the bias stays: publish
late, and publish what was measured rather than what was anticipated.
