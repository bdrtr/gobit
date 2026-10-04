# Who would watch a workflow step — measured 2026-10-04

The evidence behind
[ADR 0385](../adr/0385-a-workflow-step-has-no-observer.md), taken on the tree at
9c787e4b. Paths only, no line numbers: the files move, the findings are what
they hold.

## The sagas

Nothing confines a saga to `internal/workflows`: no rule keeps a module, a job
or the composition root from importing `internal/core/workflow`. The production
packages outside the engine tree that import it are three:

```
$ grep -rl '"github.com/bdrtr/gobit/internal/core/workflow"' --include='*.go' . \
    | grep -v _test | grep -v '^./internal/core/workflow' | xargs -n1 dirname | sort -u
./internal/app
./internal/jobs/sagawatch
./internal/workflows/checkout
```

`internal/app` builds the engine and recovers the checkout's executions,
`internal/jobs/sagawatch` reads stuck executions, and the checkout is the one
saga. It is the only package that writes a `workflow.Workflow` literal with a
name, `Name: WorkflowName`, in `internal/workflows/checkout/complete_cart.go`;
`internal/app/recover.go` writes only the empty value on its error returns.

The checkout runs its workflow on a context made with `context.WithoutCancel`
over the request's, under a timeout: the request's values, its trace among them,
travel; its cancellation does not.

## The engine tree's imports

Every import of `internal/core/workflow` and `internal/core/workflow/pgstore`,
production files only, the standard library's included:

```
bytes                   context                 crypto/rand
embed                   encoding/base32         encoding/binary
encoding/json           io/fs                   log/slog
maps                    math                    reflect
runtime/debug           slices                  strings
sync                    time                    unicode/utf8
github.com/bdrtr/gobit/core/db
github.com/bdrtr/gobit/core/errors
github.com/bdrtr/gobit/core/personaldata
github.com/bdrtr/gobit/internal/core/workflow
github.com/jackc/pgx/v5
github.com/jackc/pgx/v5/pgconn
github.com/jackc/pgx/v5/pgxpool
```

No `core/provider`, `core/plugin`, event bus or `go.opentelemetry.io` import,
and no `runtime/trace`, `expvar` or `net/http`. `reflect` serves a typed-nil
check and `reflect.DeepEqual`; `runtime/debug` is a panic's stack in the log.

## The engine tree's seams

`TestTheWorkflowEngineOffersNoObserver` lists 78 entries in nine kinds. By
kind, with what they are:

```
assert        5   ClaimingStore, Recoverable, RecoveryBlocker (ADR 0017);
                  json.RawMessage in RunInto; error on a recovered panic
field         5   RetryPolicy.Retryable; StepContext.Input and .Shared;
                  branchResult.out and .shared (all any but Retryable)
func-type     1   RunOption, configures one run
interface     8   Step, Store, Executor, Recoverer, Recoverable,
                  RecoveryBlocker, ClaimingStore; pgstore.rowSource
outside-type 16   context, time, sync, embed, io/fs, json, slog, db,
                  pgxpool and personaldata types named in declarations
param        16   step inputs and shared maps (any), safeCall's fn,
                  panic values, pgstore's scan and query arguments
reflect       9   isNilStep's kind checks; mergeShared's DeepEqual
result       12   step outputs (any), pgstore's scan targets
var           6   ErrPanic, ErrUncompensated, two id encodings,
                  pgstore's embedded migrations and their root
```

No body calls `Value` on a context, and no body declares a type. None of the
78 is called around a step by anyone but the engine itself.

What the census does not see: a new member whose outside type is already
listed (a second `*slog.Logger`, another `context.Context`), and a new call on
a value the engine already holds. The logger is the widest: whoever builds the
slog handler the engine is given reads every line it writes about a step.

## The Store

`AppendStep` is implemented by two types, and no production type embeds
`workflow.Store`:

```
$ grep -rl 'func (.*) AppendStep' --include='*.go' . | grep -v _test
internal/core/workflow/memory.go
internal/core/workflow/pgstore/pgstore.go
```

Only a test double embeds `Store` (`internal/core/workflow/workflow_test.go`).
Recovery finds `ClaimingStore` by type assertion in
`internal/core/workflow/recover.go`; an embedding wrapper would hide it, as
`docs/known-limits.md` already records.

## Who reads a step's timing

`StepRecord.Attempts`, `StartedAt` and `EndedAt` are written by
`internal/core/workflow/executor.go` and upserted by
`internal/core/workflow/pgstore/pgstore.go`. `internal/core/workflow/pgstore/stuck.go`
selects the columns; `internal/app/stuck.go` prints a held saga's steps by
index, name, status and held output. The `StartedAt`/`EndedAt` reads in
`internal/app/jobs.go` are a job run's, not a step's. No production code reads a
step's attempts or duration.

A compensated step keeps one row. The compensation record is written over the
Invoke record: `StartedAt` and `Attempts` stay Invoke's, `EndedAt` becomes the
compensation's end, and compensation retries go to the log
(`StepRecord`'s field comments in `internal/core/workflow/store.go`,
`compensate` in `internal/core/workflow/executor.go`). For a compensated or
compensation_failed step, `ended_at - started_at` spans every later step and
the compensation, and `attempts` counts Invoke alone.

## What a failure already reports

`internal/core/workflow/executor.go` logs a failed step at ERROR with the keys
`workflow`, `execution_id`, `step`, `step_index`, `attempt` and `error`, and
logs a compensation that could not be completed at ERROR. The error collector's
default allow list in `core/errorreport/policy.go` lets `workflow` and `step`
travel, not `execution_id` or `attempt`. `internal/core/workflow/pgstore/stuck.go`
lists every `compensation_failed` execution.

## ParallelStep

`NewParallel` is called only by tests:

```
$ grep -rl 'NewParallel(' --include='*.go' .
internal/core/workflow/parallel.go
internal/core/workflow/parallel_test.go
internal/core/workflow/regression_test.go
```
