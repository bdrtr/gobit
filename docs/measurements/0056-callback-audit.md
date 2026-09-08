# Auditing a callback write — measured 2026-09-08

The evidence behind [ADR 0056](../adr/0056-a-callback-is-recorded-in-the-log-and-not-in-the-audit-table.md):
what the audit contract already says, what a callback row could and could not
hold, and what the ring said about itself before this record was written.

## 1. Where the audit contract states its subject, verbatim

Six statements, in the six places a reader meets them. None of them is a passing
remark; each is the reason a surface was left out.

| Where | What it says |
|---|---|
| `core/audit/audit.go`, package doc | "Package audit records who called which admin write, and what came back." |
| `core/audit/migrations/000001_audit_init.up.sql`, table comment | "The audit log: one row per admin WRITE that reached the server." |
| the same file, section heading and body | "Only writes, and only the admin surface … The storefront is unauthenticated by decision (ADR 0008), so a row there would record 'somebody' and mean nothing." |
| the same file, on the column | "actor_id and actor_kind are the caller: a user or an api key." |
| `core/http/audit.go`, on `Audit` | "Why it wraps writes only, and only where an identity exists … A row is worth writing when it names somebody." |
| `docs/security.md` | "The audit log records one row per admin write … Storefront requests are not recorded (its principal is a publishable key naming a sales channel rather than a person, so a row there would say 'somebody' and mean nothing)." |

Two more places carry the same subject without arguing for it:
`internal/app/auditlog.go` publishes `actor_id` over the wire as "who made the
request", and `core/http.Principal` documents its `Kind` as a closed pair,
"user" | "api_key".

The last row is the one that decides the question rather than restating it. The
publishable key is a constant that names a CHANNEL and not a person, and the
repository has already refused, in writing, to audit the surface it guards. A
callback's `Source` is the same shape: a literal naming a provider.

## 2. What an `actor_id` of "paytr" would be

`audit_log` carries an index on `(actor_id, created_at DESC, id DESC)` and the
migration names the question it answers: "what did this person do".

The two values that column holds today are identifiers this installation ISSUED
— a user id or an API key id. Both are revocable, both resolve to a holder, and
the filter is useful precisely because its domain is the set of identities the
installation handed out.

A callback row's value would be `CallbackRoute.Source`: a literal a plugin
compiles in, identical on every row forever, issued by nobody and revocable by
nobody. Two further consequences, measured against the schema:

- **It duplicates an axis that already exists.** `Register` refuses two
  providers on one path, so path determines source. Filtering by `actor_id` =
  "paytr" is the path filter with a coarser key and a second index behind it.
- **It is a proof in some rows and an assertion in others.** A verified callback
  proves its sender holds the shared secret. A REFUSED one proves nothing at all
  — anyone can POST to the path — and nothing in the row says which kind it is.

## 3. The outcome is the fact worth recording, and the table cannot hold it

The ring can answer a callback in five ways, and the answers are the PROVIDER's
protocol rather than gobit's. Measured on the one real route,
`plugins/paymentpaytr`:

| Outcome | PayTR's answer | The row `audit_log` could write |
|---|---|---|
| accepted | 200 `OK` | actor "paytr", POST, /paytr/callback, 200 |
| duplicate (a contradiction) | 200 `OK` | actor "paytr", POST, /paytr/callback, 200 |
| rejected (forged signature) | 403 `BAD_HASH` | … 403 |
| malformed | 400 `BAD_REQUEST` | … 400 |
| unavailable | 500 `RETRY` | … 500 |

The first two rows are identical. The contradiction is the outcome the ring logs
at ERROR with "a human has to look" — the same event asserting a different
amount — and in `audit_log` it is indistinguishable from an ordinary successful
payment callback.

Nothing in the contract stops it getting worse: a provider that reads the body
and ignores the status is exactly the shape ADR 0028 was built for, and such a
route may answer all five with 200. `audit_log` has no column but `status` in
which an outcome could land, and `status` belongs to the provider.

## 4. What the ring said about itself, before and after

Every branch that can end a callback, and whether it left a line naming the
callback. The four silent ones were all in the same class: the handler ran.

| Branch | Line before | Line now |
|---|---|---|
| quota unreachable, let through | WARN | unchanged |
| throttled | WARN | unchanged |
| body could not be read | WARN | unchanged |
| signature failed | ERROR | unchanged |
| verified payload could not be keyed | ERROR | unchanged |
| unkeyable payload, handler runs | **none** | INFO, with the status |
| no replay window, handler runs | **none** | INFO, with the status |
| same event in flight | INFO | unchanged |
| replay window unreachable | ERROR | unchanged |
| replayed from the record | INFO | unchanged |
| contradicting retry | ERROR | unchanged |
| handler succeeded | **none** | INFO, with the status |
| handler answered 5xx or overflowed | **none** | INFO, with the status |
| processed but not recorded | ERROR | unchanged |
| handler panicked | none (the recoverer and the access log carry it) | unchanged |

The shape of the silence is the point: every line that existed was written by a
guard REFUSING something. A log containing no callback line meant either
"nothing arrived" or "everything succeeded", and no reader could tell which.

The access log does carry every callback — `RequestLogger` writes one "request
completed" line per request with the method, the path and the status — but it
names no source, and by section 3 the status cannot name the outcome.

## 5. What the trap costs, in numbers

A record that only covers callbacks which PASSED the guards is evidence about
the guards. So the population has to be drawn where the ring RECOGNIZES a route,
before the quota — which means an unauthenticated caller decides how many
records get written.

In the log that is affordable. In a table it is not, with today's defaults:

- `RATE_LIMIT_PER_MINUTE` defaults to 600. At the ceiling that is 864,000
  refused callbacks a day, each one an INSERT.
- The bucket is per client IP only while `TRUSTED_PROXY_HOPS` is positive; the
  default is 0, so behind a reverse proxy every caller shares one bucket — the
  state `config.RateLimitKeyIsPerClient` reports.
- With `RATE_LIMIT_PER_MINUTE` at or below zero the limiter is not built at all
  and there is no ceiling.
- `audit_log` has no retention window and no endpoint that deletes a row;
  pruning is an operator's scheduled statement, "because a log the API can prune
  is a log an intruder can prune" (`docs/security.md`).

That is the ordering argument of ADR 0028 inverted: the quota is first so that a
refused request is almost free, and an audit write placed correctly makes a
refused request the most expensive thing on the surface.

## 6. What a separate ledger would have to bring

A `callback_log` table is the candidate that fits the facts — it can carry a
source, an outcome and a replay key, and it touches no published contract. What
it costs is not the table:

- a migration owner and a place in the ownership audit;
- a column in `TestEveryColumnIsWrittenBySomething`'s scope for every field;
- a reader, or it is the write-only ledger ADR 0037 was written about — four
  places in this repository already cite `audit_log` as exactly that mistake;
- a scope beside `audit:read`, and a decision about who holds it;
- a retention answer, since section 5's population is chosen by the caller.

Those are product decisions, not mechanical ones.
