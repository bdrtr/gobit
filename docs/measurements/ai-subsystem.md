# An AI subsystem — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

## Amendment 2026-09-09: the brief was ANSWERED, in two records

What this report measured has since been decided and built, so a reader landing
here should know they are reading the evidence rather than the plan.

[ADR 0071](../adr/0071-a-machine-proposal-is-not-a-moderation.md) settled where
a machine's opinion is stored: four columns of its own on `reviews`, three
mirrored CHECK constraints, and neither the status nor the moderation moment
touched — so a proposal is not a moderation and the storefront cannot see one.

[ADR 0072](../adr/0072-a-model-answers-a-closed-question-and-a-job-asks-it.md)
settled who asks. `core/provider` gained a CLASSIFICATION contract with a closed
label set rather than a completion; the slot is singular (`ai.provider`), the
client is a plugin (`ai-anthropic`), and one job — registered only when the slot
is filled — turns answers into proposals.

Three things this report asked for were deliberately NOT built, and each is
recorded where it belongs rather than left as an implied next step:

- **No eval harness and no accuracy threshold.** A threshold is the thing that
  must never ship unmeasured, because a number nobody took reads as a guarantee.
  Measuring one needs a corpus of reviews an operator has already decided about.
- **No suggestion store of its own.** ADR 0066 refused a table and this build
  kept that: one row per review at most, read only with the review.
- **No panel page.** The proposal is a field on the admin DTO; a page is a
  separate decision nobody has made.

## Amendment 2026-09-08: eight passages were overtaken, and the file is NOT rewritten

A measurement says what was true on the day it was taken, so the text below
stands and this block says which of its sentences a later decision has since
answered. ADR 0045 named this debt when it was accepted — "whoever lands the
module owes that edit" — and the 2026-09-08 move out of `docs/gaps.md` carried
the passages forward unchanged, which relocated the disagreement instead of
paying it.

1. **"semantic search reopens ADR 0015 rather than sitting on top of it"** and
   **"Embeddings, semantic search and recommendations all sit behind the ADR 0015
   reopening and should be costed as that, not as features"** — ADR 0045 decided
   the opposite. The cluster contract keeps `extensions: none` and pgvector
   arrives as a separate OPT-IN extension module. Cost them as a plugin nobody
   has written, not as a contract renegotiation.
2. **"the selection in configuration and an unknown name stopping the startup"**
   holds for notification and file alone. Payment and fulfillment select per
   TRANSACTION, and tax from the region row's provider id.
3. **"anything that reads a review AND calls the AI is a workflow (ADR 0001/0006)"**
   is false. `internal/jobs` is bound by no such import rule and is this tree's
   answer for scheduled cross-record work; the brief at the head of this file
   says so itself.
4. **"The review module, then …"** in "What would come first" — the review module
   was built on 2026-09-06 and is no longer first.
5. **"exactly the constraint B7 records for inventory"** dangles. The A–G gap
   inventory left `docs/gaps.md` on 2026-09-08; B7's event half closed with
   ADR 0063 and its ledger half with ADR 0068.
6. **The event-gate sentence is no longer the whole gate.** ADR 0063 added a
   second, which refuses a topic whose only subscriber is a generic forwarder —
   so publishing a topic is now MORE expensive than this file priced it, not
   less. The exemption map is the remaining remedy, and it is empty by policy.
7. **"Masking a review … is errorreport's policy generalised"** does not hold.
   That policy is an allow list over KEYED attributes; a review body is one
   free-form value, where an allow list degenerates to allow-all or deny-all.

The brief: put the LLM in the platform as a SUBSYSTEM rather than a tool called
from outside. A core `ai` package with a `Provider` interface and a typed
`Task[In, Out]` layer above it; review moderation as the first use case, driven
off the outbox rather than the HTTP path; then summaries, description drafting,
query rewriting, embeddings, recommendations, a support assistant. With
versioned prompts, token budgets, an input-hash cache, PII masking, prompt-
injection separation, an eval set, and graceful degradation.

**Nothing about AI exists in the repository today.** What follows is what the
brief assumes, measured.

### Two assumptions that do not hold

1. ~~**There is no review module.**~~ **Built 2026-09-06 (B4), and the count is
   seventeen:** product, pricing, inventory, region, customer, cart, payment,
   order, fulfillment, promotion, tax, auth, file, notification, b2b, invoice
   and review. A customer can leave a review now and moderation has something to
   moderate, so the half this item said had to come first is done — schema,
   service, storefront write, admin read and a four-edge moderation state, with
   the storefront's reads carrying `approved` as a SQL literal.

   The other half of this item still stands and is now the ONLY half: the AI
   subsystem does not exist. Read what that leaves precisely, because the
   moderation FLOW is not the thing that is missing — it exists and it is a
   HUMAN's, a queue at `GET /admin/v1/reviews?status=submitted` and one endpoint
   that moves a review. What an AI would add is a SUGGESTION on that queue, not
   a decision, which is the `sagawatch` shape ADR 0017 already argues for.

   And the event this item named is still absent, deliberately: the module
   publishes none, because `TestTheEventTopicsHaveASubscriber` refuses a topic
   no production file subscribes to and its exemption map is empty by policy. A
   review event and its first subscriber are one package or neither — exactly
   the constraint B7 records for inventory. So "`review.created` reaching a
   worker" is not free machinery waiting to be used; it is a second thing the
   first AI use case has to bring with it.

2. **pgvector is not available and is not free to add.** The cluster today has
   `pg_trgm` and `unaccent` available; `vector` is not. More to the point,
   ADR 0015 fixes the contract at **zero required extensions** and says in
   writing that adding one is a change to what an operator must provide — a
   decision with a date on it, not the side effect of a feature.

   `pg_trgm` is already the standing candidate there, with a measured case
   (search `?q=` 58.9 ms to 2.2 ms) that was still not taken. Embeddings would
   make `vector` the second candidate, and the honest reading is that semantic
   search reopens ADR 0015 rather than sitting on top of it.

### What the brief needs and this repository already has

The shape the brief describes is, unusually, the shape this codebase already
uses. Seven pieces exist:

1. **The provider slot.** Payment, tax, notification and file all resolve a
   provider from a registry BY NAME, with the selection in configuration and an
   unknown name stopping the startup. `ai.Provider` is that pattern again, and
   "swap Anthropic for a local model in one line" is what the registry already
   delivers for payment.

2. **The outbox.** `review.created` reaching a worker without the HTTP path
   waiting is exactly what ADR 0023 built this session: the event is written
   inside the transaction that promises it, so a review that was saved cannot
   have an unfired event. The brief's "a review must never be lost" is that
   guarantee, already there.

3. **The job runner.** A worker draining a queue with an interval and a run
   budget is `internal/core/job` (ADR 0019), including the rule that `MaxRun`
   must be shorter than `Every` — refused at startup, which is how the outbox
   relay was caught before it shipped.

4. **Versioned files embedded in the binary.** Migrations, panel templates and
   the panel stylesheet all do it, the last with an ETag derived from the bytes.
   "Prompts are versioned files, not strings in code" is the same mechanism, and
   the stylesheet's stamp is the model for deriving a prompt version from its
   content rather than trusting somebody to bump a constant.

5. **Metrics.** An OTel meter provider is wired; token counts per task are a
   counter on it. (~~It exports over OTLP, not Prometheus~~ **Corrected
   2026-09-08, ADR 0046: the provider is a Prometheus SCRAPE now.** It is built
   when `METRICS_ADDR` is set, and whatever records into it — a token counter
   included — is read from `/metrics` on that address rather than pushed to a
   collector. The observability entry above carries the rest.)

6. **Redaction.** `errorreport/policy.go` already splits attributes into what
   may travel and what may not, and REPORTS what it removed rather than dropping
   it silently. Masking a review before it leaves the process is that policy
   generalised, and "report what was removed" is the property worth keeping:
   a masker that silently ate the whole comment would look like a working
   masker.

7. **An eval harness.** `go test` with testcontainers is how every module is
   tested; a labelled set of reviews with an accuracy assertion is a test like
   any other. What it needs that does not exist is a way to run it WITHOUT
   calling a paid provider on every `make test` — a recorded-response fixture,
   with the live run behind a tag, the way `integration` and `smoke` already
   separate the expensive suites.

### The decisions the brief implies that this repository would have to make

- **"Core module" is two different things here.** `internal/core` holds
  capabilities modules opt into; `internal/modules` holds owners of data.
  an `ai` package belongs in the core tree (it owns no commerce data and every
  module may use it); the review data belongs in a module; and anything that reads a review AND
  calls the AI is a workflow (ADR 0001/0006). The brief's `Task[In, Out]` with
  `Build` and `Parse` is core; `review.moderate` is the review module's task
  definition, registered into core's registry the way a payment provider is.

- **The model's decision must not be the last word, and this codebase already
  argues that case elsewhere.** The returns flow refuses to refund automatically
  on receipt because that is the shop's decision; the same reasoning gives the
  moderation thresholds to configuration and the low-confidence cases to a human
  queue. Recording the human's decision is then not only an audit trail — it is
  the eval set growing itself.

- **An LLM cache is a different argument from a read cache.** The read-cache
  section above measures that caching database reads is not warranted: the
  database is twelve times faster than the code formatting its answers. A
  content-addressed cache over LLM calls is not that. It saves money and
  removes non-determinism, and neither of those was on the scale being measured
  there. The two must not be conflated.

- **Prompt injection has a structural answer, not a prompt-worded one.** The
  brief is right that the review text travels in its own block. The stronger
  half is what this repository would insist on anyway: the model returns a
  decision, the decision passes a threshold, and a human sees anything below it
  — so a successful injection changes a confidence score, not what gets
  published.

### What would come first

The review module, then an `ai` package under core with one task and one
provider, then
the moderation flow on the outbox that already exists. Embeddings, semantic
search and recommendations all sit behind the ADR 0015 reopening and should be
costed as that, not as features.

---
