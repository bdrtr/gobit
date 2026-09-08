# ADR 0072 — A model answers a CLOSED question, a plugin speaks to it, and a scheduled job asks

**Summary:** The core publishes a classification contract, a plugin holds the
model client, and one conditional job turns answers into proposals.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

ADR 0071 decided where a machine's opinion about a review is STORED. This is
the other half: who asks, what they may ask, and where the client lives.

Three constraints were settled and none was reopened: the core knows no module
(Principle 2.4), so the contract cannot mention reviews; a capability ships with
its first consumer or not at all (ADR 0063); and the embedder is the data
controller (ADR 0029), so the choice to send a customer's words elsewhere is
theirs.

Measurement: [measurements/ai-subsystem](../measurements/ai-subsystem.md).

## Decision

**The contract is a CLASSIFICATION, not a completion.** `core/provider` gains
`Classifier`, `ClassifyInput` and `Classification`: the caller supplies the
question, the text and a CLOSED set of labels, and gets back one of them, a
reason, and the model that answered. Free text would make every caller invent
its own parsing, and the day a model phrases itself differently each parser
breaks separately and silently. An answer outside the set is a failure of the
call — the label is on its way into a column with a CHECK.

**The slot is SINGULAR: `ai.provider`**, filled by a plugin under the name
itself. It is `error.reporter`'s shape, not the four registries': two payment
providers can both be right for one shop, while two models would leave a caller
deciding whose opinion goes in the column.

**The client is a plugin**, `ai-anthropic`, speaking the Messages API by hand
for the reason `errorsentry` and `errorotlp` write their own bodies. It refuses
to start without a key and a model, and the model has NO default: a model name
is a price and a capability, and defaulting it would move an installation to a
different bill on an upgrade rather than on a decision.

**The job is registered only when the slot is filled.** Most installations run
no model, and a job reporting "no provider" every quarter of an hour is noise an
operator learns to skip — including on the day it says something else. A
classifier with no review module is the opposite case and fails at boot.

**The author's name is not sent.** It is the one thing the module stores about
that person, says nothing about whether the text is spam, and a request log
cannot be un-written.

## Consequences

- **An installation naming the plugin takes on a SUB-PROCESSOR.** The review
  text leaves the building; gobit cannot make that choice for a controller, and
  what it owes them is that the choice is explicit and documented.
- **No accuracy is claimed, because none was measured.** No eval harness and no
  threshold — a threshold is the thing that must never ship unmeasured, since a
  number nobody took reads as a guarantee. Measuring one needs a corpus of
  reviews an operator already decided about, which this tree cannot invent.
- **Two audits were found blind while wiring this.** The published surface was
  audited by DIRECTORY while the promise is made in NAMES — three names entered
  `core/provider` with the suite green — so an inventory now holds every one.
  And the provider-registry tie was four typed assertions; deriving that
  population found a fifth family nothing had mentioned, `tax`, now written down.
- **A scheduled job writes, and the doctrine says so.** ADR 0019's line is
  between having an effect and recording an opinion, not between reading and
  writing; the gate's godoc is amended, not rewritten.

## Rejected

- **A completion contract.** More general, worse here: it moves the parsing to
  every caller and the breakage to a day nobody is looking.
- **A registry of models.** Nothing could choose between two of them per call.
- **The prompt as configuration.** A shop that could write it could ask for
  everything to be approved, and the answer would still read as advice.
- **Vendoring an SDK.** Streaming, batching and retries are the parts worth a
  dependency, and this needs none of them.
- **Asking on read.** ADR 0071 rejected it for the operator's latency; it would
  also put an outbound call on a request path.
