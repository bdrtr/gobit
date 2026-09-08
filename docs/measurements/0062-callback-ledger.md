# What a callback's record already is — measured 2026-09-08

The evidence behind [ADR 0062](../adr/0062-a-callbacks-ledger-is-the-receiving-modules-table.md):
where the three things `callback_log` was said to need already are, what an
operator can do with the ring's log today, and the one defect the measurement
turned up.

Everything below is read off the tree at 698b649 unless a section says otherwise.

## 1. The three requirements, and where each already is

[ADR 0056](../adr/0056-a-callback-is-recorded-in-the-log-and-not-in-the-audit-table.md)
parked the table on a reader, a scope and a retention answer. They exist — in the
module that receives the callback, not in a shared table.

| What it needs | Where it is today | Whose |
|---|---|---|
| a reader | `GET /admin/v1/paytr/pending`, the stuck-payment listing | the receiving module's |
| a scope | `paytr:read`, alongside `audit:read` and `personal-data:erase` | the receiving module's |
| a retention answer | the table has no `deleted_at`, the plugin has no statement that deletes a row, and it is not a declared holder erasure reaches | already settled |

The listing is not a callback listing, and that is the point of section 2: what
it lists is a payment PayTR has not reported on, which is the callback question
an operator actually asks — *did the provider ever call us at all?*

The retention row is measured rather than argued. `plugins/paymentpaytr` holds
five statements — an insert, a read, the callback update, a refund accumulation
and the pending listing — and not one of them is a DELETE. The table carries no
`deleted_at`, so nothing soft-deletes it either, and the plugin declares no
personal-data holder, so the erasure sweep does not reach it. ADR 0054's "a
money record is kept" is the precedent this matches; that record governs the
order and payment modules and not a plugin's table.

## 2. The one real route already keeps a durable per-event record

`plugins/paymentpaytr` owns one table, and its columns were written for exactly
this. From the migration's own comments:

| Column | What it records | The migration's words |
|---|---|---|
| `callback_at` | when the provider reported, NULL until then | "did PayTR ever call us at all?" |
| `status` | pending → success/failed | "what PayTR last told us" |
| `paid_amount` | the amount the callback carried | kept even when it disagrees, "because the disagreement is the thing an operator has to be able to see" |
| `failure_reason` | what the provider said on a failure | "here so a support question has an answer" |
| `refunded_amount` | what has been sent back | "PayTR has no 'how much has been refunded' query, so the only ledger of that is this column" |

The row is written before the customer leaves for the provider and updated once,
guarded on the status still being pending — so a replayed genuine callback
cannot overturn an earlier outcome. The index is partial, on the pending rows,
because "the operator's question is almost always which payments are stuck".

This is not an accident of the PayTR integration. The plugin's package doc and
the migration both derive the table from the provider contract: `Authorize` is
asked from a session id alone, a gateway that cannot be DRIVEN answers with
whatever the callback said, and that answer has to survive a restart. Any second
callback-driven provider arrives with the same requirement.

## 3. What no module's table can hold

The outcomes where the handler never ran. Driven by
`TestNoCallbackOutcomeIsSilent`, twelve of them; these four never reach a module:

| Outcome | Level | Reaches a module table |
|---|---|---|
| throttled by the quota | WARN | no |
| body could not be read | WARN | no |
| signature failed | ERROR | no |
| verified payload could not be keyed | ERROR | no |

Two more are about the ring rather than the event: the replay window being
unreachable (ERROR) and a callback arriving while the same event is in flight
(INFO). The contradiction (ERROR) is the sharpest of the set. The ring answers
it without running the handler, so the module's row keeps the FIRST outcome —
which is the correct behavior, and it means the second, different assertion
exists in the log line and nowhere else. Two defenses hold that row, one at each
layer: the ring's fingerprint comparison, and the module's own update guarded on
the status still being pending.

## 4. Retention: there is none in this tree, anywhere

Searched across Go, SQL, YAML and Markdown:

- the only retention settings in the repository are the two idempotency stores,
  `core/http` and `core/http/redisguard`. ADR 0029 states this in the same words
  and it is still true.
- `audit_log` has no window and no endpoint that deletes a row. ADR 0037 says so
  under "What this deliberately does NOT do", and `docs/security.md` repeats it
  where an operator meets it: "pruning is an operator's scheduled statement,
  because a log the API can prune is a log an intruder can prune".
- ADR 0029 assigns the window to the embedder: gobit owes the mechanism, not the
  policy.

So a `callback_log` would be the FIRST table in this repository to need a
retention window, and the only one whose population an unauthenticated caller
chooses. `audit_log`'s answer — none, the operator prunes — is available to a
table whose rows are admin writes by identities the installation issued. It is
not available to this one. ADR 0056 §5 put the number on it: 864,000 refused
callbacks a day at the default quota ceiling, and no ceiling at all when
`RATE_LIMIT_PER_MINUTE` is zero or less.

## 5. Does anything read a log back? Yes — one thing, and it is wired by default

Nothing in this repository reads a log FILE. What reads log RECORDS is
`core/errorreport`: a `slog.Handler` the composition root installs as the
server's logger middleware, unconditionally and with no setting to turn it off.
It is the only logger in the tree built that way, and it is the one a callback
can reach — the ring runs in the serving process and nowhere else. It forwards
records at ERROR and above to a sink a plugin fills (`plugins/errorsentry`
today) and drops them while no reporter is installed, which is the usual
installation and costs it a nil check per failing request.

What it gives a callback refusal that a log file does not:

- a **fingerprint**: the code of the error the record carries, which is what a
  collector groups occurrences by;
- a **rate limit per code**: three reports per code per minute by default, so one
  flood cannot bury everything else;
- an **allow-listed payload**: of what a ring line carries, `path` and `status`
  travel; `callback` and `key` are redacted BY NAME, so the reader can see that
  something was withheld rather than absent.

Two limits, measured rather than assumed. The source travels as `path` and not
as `callback`, which is sufficient rather than lucky — `Register` refuses two
providers on one path, so the path determines the source. And a ring line
carries no `request_id`: the ring logs through the logger it was CONSTRUCTED
with, which is the embedder's, and not through the request-scoped one the
middleware puts in the context. Correlating a refusal with the access log line
for the same request is therefore done on the path and the moment.

## 6. The defect this measurement found, and the fix

Of the ring's ERROR outcomes, one carried no error value: the contradiction —
*"a callback contradicted an event already recorded; a human has to look"*. It
logged the store key and a sentence.

`classify` reads the code off the record's `error` attribute; with none it
returns `unclassified`, whose own godoc says that bucket "has to stay empty
enough for a genuinely unclassified failure to be visible in it". The rate limit
is per code, so the contradiction also spent that shared bucket's three reports a
minute. The one outcome whose message says a person must act was the hardest one
in the collector to find.

It now carries `coreerrors.Conflict` with the code `callback_contradiction`,
held by `TestTheContradictionCarriesACodeAnErrorCollectorCanGroupBy`. The
constant is unexported: it is never returned to a caller, and what consumes the
string is an alert rule, which is a literal on that side too.

## 7. The trigger, counted

Production `CallbackRoute` literals in the tree, by source:

| Source | Where |
|---|---|
| `paytr` | `plugins/paymentpaytr`, the module's callback route |

One. `TestASecondCallbackRouteReopensTheCallbackLedger` derives that set from the
production source rather than from a list, and fails when it changes.

Mutation-proved 2026-09-08, each from a copy taken aside and restored from it,
`-count=1`:

| Mutation | Result |
|---|---|
| a second `CallbackRoute` literal added, source `yurtici` | FAIL, naming both sources and their locations |
| the `Source` field made unresolvable (`strings.ToLower("PayTR")`) | FAIL on the unresolvable source, then on blindness |
| the literal's type aliased locally, so the scan matches nothing | FAIL on the blindness floor |

And for section 6:

| Mutation | Result |
|---|---|
| the error logged under a key other than `error` | FAIL: no line carries an error value |
| the code string changed | FAIL, naming the old string and the new one |
