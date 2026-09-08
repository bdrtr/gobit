# A locale has no source — measured 2026-09-08

Evidence for
[ADR 0061](../adr/0061-a-locale-needs-a-source-before-it-needs-a-home.md), which
closes gap A11. Everything below was taken against the tree on the day, with the
commands beside each figure so the next round can re-take them rather than trust
them.

## 1. Can a request ASK for a language? No, and nothing has changed

[ADR 0050](../adr/0050-gobit-stores-one-language-and-the-second-is-the-embedders.md)'s
central finding was that the storefront has no notion of a shopper's language.
Every leg of it was re-run.

| probe | command | result |
|---|---|---|
| a header | `grep -rniE 'accept[-_]?language' --include='*.go' .` | 0 hits |
| a path segment | no route pattern in the tree carries one | 0 |
| a query parameter | no handler reads one | 0 |
| an identity field | `core/http.Principal` | still 4 fields: ID, Kind, Scopes, SalesChannelIDs |

`Principal` is a closed struct in a published package, so the fourth row is not
"nobody added one yet" — an outside package cannot add a fifth field at all.

## 2. Where the word DOES appear, and what it means there

Counted over production (non-test) Go files, by occurrence rather than by
matching line:

    for f in $(grep -rl -i locale --include='*.go' . | grep -v _test.go); do
        printf '%s %s\n' "$(grep -o -i locale "$f" | wc -l)" "$f"
    done | sort -rn

| where | occurrences | files | meaning |
|---|---|---|---|
| `plugins/webpush` | 64 | 5 | a DEVICE's template choice (ADR 0018) |
| everywhere else | 35 | 15 | the PostgreSQL cluster's collation (ADR 0015) |
| **total** | **99** | **20** | of 699 production Go files |

**The partition is sharper than ADR 0050 recorded it.** That record said every
non-plugin mention is the cluster's fold, which is true. What it did not say is
the stronger fact: outside the plugin, not one of the 35 is a NAME. Every one is
inside a comment or a string — `--locale=C`, `a C-locale cluster`, an operator
hint in a `slog` field. Nothing in the tree outside that plugin declares an
identifier, a struct field, a JSON tag or a column named for a locale. That is
what makes the gates in `internal/arch/locale_test.go` possible: they can refuse
a NAME while leaving prose completely alone.

ADR 0050's own figures were 77 / 45 / 32. The non-plugin count it reported (32)
is within three of today's 35 and its file-level partition still holds; the
plugin's own count moved most. The numbers are re-taken rather than reconciled —
a report says what was true on the day it was taken.

Schema side, comments stripped:

| where | occurrences |
|---|---|
| `plugins/webpush/migrations` | 1 — `locale text NOT NULL DEFAULT ''` |
| every other migration in the tree | 0 |

Every other SQL mention is a `--` comment about a C-locale cluster, in the
invoice and product migrations. That is why the SQL gate strips line comments
and nothing else.

## 3. What ADR 0050 said would have to change first, and did

ADR 0050 gave two reasons the timing was wrong. One is spent.

| ADR 0050's reason | today |
|---|---|
| "no locale reaches a handler gobit owns" | **still true** — section 1 |
| "ADR 0044 is Accepted and unbuilt, and it rewrites the catalog read paths a locale would ride" | **spent** — the reads have moved |

ADR 0050 evidenced the second with
`grep -rn '/store/v1/sales-channels' --include='*.go' .` returning zero hits.
The same command today returns the bound patterns in the product module's store
routes, the search plugin's description, and eight test and rig call sites. The
four routes ADR 0044 named are bound.

This matters for the SHAPE and not for the timing. ADR 0044 moved the sales
channel into the path with a reason that transfers exactly: a value that varies
the response body cannot be cached from a header nothing emits `Vary` for. A
locale varies the body in the same way. So "where would a locale go" stops being
an open question and becomes a precedent — which is why ADR 0061 can settle the
shape without building the route.

## 4. Why neither half can be built alone

This is the measurement that turns A11 from a gap into a decision.

**The storage half cannot go first.** Its key would be a locale, and section 1
says no request carries one. The only party that could produce a value is an
embedder that has REPLACED a gobit handler, which ADR 0050 measured and priced.

**The request half cannot go first either**, and the rule that stops it is this
repository's own: a capability and its first consumer ship together, and the
exemption map is empty by policy. A locale path segment would have to vary
something. Counted:

| candidate consumer | rows that would vary | why not |
|---|---|---|
| product / variant / collection / image | 0 | `metadata` is a jsonb bag with no schema, and no read keys on a locale |
| category, tag, option, option value | 0 | no `metadata` column at all |
| country, currency | 0 | no `metadata`, no UPDATE path, 249 + 41 English names |
| search index | 0 | `plugins/searchpg` indexes title, handle, subtitle, tags, variant titles, SKUs, description — no metadata |
| admin panel | 0 | English strings compiled into the binary, behind `internal/` |

Zero rows in the tree hold a second-language string that any read path returns.
A locale segment shipped today would return byte-identical bodies for every
value it is given — a published URL shape, which ADR 0025 says can never be
taken back, bought for nothing.

## 5. The trigger, and why it is pointable

ADR 0050 listed four triggers. Three of them are statements about a customer
project that does not exist, and one ("proof the metadata path does not work")
is about the workaround rather than about the axis. ADR 0061 names two that a
person can observe.

### 5.1 A second component names a locale

Exactly one does, and the two gates make the second one a failing test that
prints the file. The mutation proofs below are what says the gates would
actually see it.

### 5.2 One catalog, two languages, one ledger

ADR 0050's shipped workaround is one installation per language. That is correct
for a merchant whose two languages are two separate SHOPS. It is measurably
wrong the moment they are one shop, and the schema says why:

| what duplicates | where it is declared | what breaks |
|---|---|---|
| stock | `inventory_levels` | two ledgers over one warehouse; both installations can sell the last unit |
| order numbering | the order module's `display_id` IDENTITY column | two increasing sequences shown to customers of one merchant |
| invoice numbering | `invoice_series`, a locked counter restarting each year | two legal document series for one taxpayer |

The invoice row is the sharpest: that counter exists precisely because a hole in
a series reads as a document issued and withdrawn, and two installations produce
two series that no reconciliation can merge afterwards.

So the observable fact is not "somebody wants translations". It is: **an
installation is asked to serve one catalog in two languages while sharing one of
those three.** A person can point at two databases with the same SKU set and a
request to reconcile their stock or their invoice numbers.

## 6. The gates, and the mutations that prove them

`internal/arch/locale_test.go`. Population from the tree — `productionTrees` and
the walk over them for Go, a walk from the repository root for `*.up.sql` —
never from the property being audited.

Six mutations, each applied to the tree, run with `-count=1`, and reverted from
a copy taken aside beforehand:

| # | mutation | expected | result |
|---|---|---|---|
| 1 | a `LocaleProbe` struct with a `Locale` field and a `json:"locale"` tag appended to a product model | fail | FAIL — reported all three names |
| 2 | a COMMENT naming a locale and a `--locale=C` cluster appended to the same file | pass | PASS — prose is not a name |
| 3 | `ALTER TABLE product ADD COLUMN locale text` appended to a product migration | fail | FAIL |
| 4 | every locale identifier in `plugins/webpush` renamed to `langtag` | fail | FAIL — "the exemption is the one thing this gate forgives" |
| 5 | the plugin's own `locale` column renamed in its migration | fail | FAIL |
| 6 | every `.go` file under `cmd/` removed | fail | FAIL — "the `cmd` tree yielded no production Go file" |

Mutation 2 is the negative control and it is the one that matters most: without
it, a gate that simply refused the WORD would look identical from the outside
and would fail on 35 correct comments about the cluster.

Mutations 4, 5 and 6 are the blindness floors — the exemption falling silent in
Go, the exemption falling silent in SQL, and a production tree dropping out of
the walk. Each fails rather than approving by reading nothing.
