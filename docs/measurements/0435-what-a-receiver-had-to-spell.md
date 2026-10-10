# What a receiver had to spell — measured 2026-10-10

Evidence for [ADR 0435](../adr/0435-a-webhook-receiver-builds-against-a-published-contract.md)
and gap D271. Read on a tree at 5d62868d (v0.11.0 and one documentation
commit); the integration lanes ran against Docker's `postgres:16-alpine`
through testcontainers.

## 1. The fault, from outside

A marketplace (kayi) ran as a separate process beside gobit v0.10.0, its own
repository and `go.mod`, talking to gobit over HTTP and the signed outbound
webhook alone. To compile and to act on a delivery it had to spell twenty-five
names gobit had not published as a contract.

Seven it gave the compiler, all in its `kayi/webhook.go`:

| Name | Where gobit defined it | Published before |
|---|---|---|
| `webhookout.VerifySignature` | `plugins/webhookout/signature.go` | no: `plugins/` is outside ADR 0026; its godoc said it was exported for the receiver |
| `webhookout.SignedFields` | the same file | no |
| `webhookout.HeaderTimestamp`, `HeaderDelivery`, `HeaderEvent`, `HeaderAttempt`, `HeaderSignature` | the same file | no, prose only |

Eighteen it wrote as strings at run time:

| Spelling | What it is | Where gobit defined it | Published before | Here |
|---|---|---|---|---|
| `id`, `event`, `event_id`, `occurred_at`, `attempt`, `data`, `redacted` | the envelope's keys | the unexported `body` type in `plugins/webhookout/sender.go` | no document, no README, no `/openapi.json` | `webhook.Delivery`'s JSON names, and its schema under `webhooks` |
| `order.placed`, `order.line_canceled` | topics | the order module's event constants; copied into `plugins/webhookout/module.go` | `docs/extending.md`'s table, read by no test, and `GET /admin/v1/webhooks/` behind `webhook:read` | the keys of `/openapi.json`'s `webhooks` |
| `order_id`, `currency_code`, `placed_at`, `cancellation_id`, `order_line_item_id`, `canceled_quantity`, `canceled_at` | payload fields | the order module's field constants; the plugin's `TopicFields` | the same table, and the listing's `topic_fields` behind `webhook:read` | each topic's `data` under `webhooks` |
| `delivered` | a parcel status | the fulfillment module's status constants | no: a status DTO field is a string and the document enumerates no value | avoided: `delivered_at` is set exactly when a parcel is delivered (`fulfillments_delivered_stamp`, `stampFor`) and is in the document |
| `gobit_session` | the session cookie's name | `contrib/identity-session`'s `DefaultCookieName`, which `Options.CookieName` overrides | no | avoided: the request's `Cookie` header is forwarded whole |

Three more the report had not counted, found by searching the consumer for
string comparisons: `authorized` and `captured` (payment session statuses,
the class of `delivered`, now a known limit) and `admin`, which is already
`corehttp.ScopeAdmin`.

The consumer files, by `grep -rl` in its tree:

```
webhookout.VerifySignature, webhookout.SignedFields, webhookout.Header
    kayi/webhook.go
"event_id"            kayi/webhook.go kayi/commission.go kayi/payout.go e2e/backbone_test.go
"redacted"            kayi/webhook.go e2e/backbone_test.go
"order.placed"        kayi/unpublished.go kayi/commission.go e2e/backbone_test.go e2e/harness_test.go
"order.line_canceled" kayi/unpublished.go e2e/backbone_test.go e2e/harness_test.go
"canceled_quantity"   kayi/unpublished.go
"delivered"           kayi/unpublished.go kayi/payout.go
"gobit_session"       kayi/unpublished.go kayi/auth.go e2e/backbone_test.go
"authorized", "captured"
                      kayi/gobitread.go
```

## 2. The searches behind "not published"

Run at the repository root before the change.

- `git ls-files | grep -iE 'openapi|swagger|\.ya?ml$|schema\.d\.ts|\.json$'`:
  no static OpenAPI document; it is built at run time.
- `grep -n -i webhook core/openapi/openapi.go`: nothing; the document had
  `openapi`, `info`, `paths` and `components` only.
- `grep -rl 'X-Gobit-' --include='*.go' --include='*.md' .`: the plugin's
  signature file and tests, and the unrelated `X-Gobit-Customer` of
  `core/identitytest`.
- `grep -rl 'envelope\|event_id\|X-Gobit' docs README.md`: no envelope was
  described anywhere outside Go.
- `grep -rl extending --include='*_test.go' .`: no test read
  `docs/extending.md`. The table had fallen behind once (D150).
- `grep -rl 'webhookout\.\(Sign\|VerifySignature\|SignedFields\|Header\)' --include='*.go' .`:
  `internal/e2e/webhook_test.go` alone, outside the plugin. It now spells
  the header names as literals, as an independent receiver does.
- `grep -rl 'Deprecated:' --include='*.go' core plugins contrib gobit.go`:
  nothing. These are the tree's first deprecated names.
- `grep -rl '"webhooks"' internal/mcp`: nothing; `gobit mcp` reads `paths`
  alone, so the new section makes no tool.

## 3. The published surface

`internal/arch/testdata/published-names.txt` gains eleven lines:

```
core/openapi Doc.DescribeWebhook
core/webhook Delivery
core/webhook HeaderAttempt
core/webhook HeaderDelivery
core/webhook HeaderEvent
core/webhook HeaderSignature
core/webhook HeaderTimestamp
core/webhook Sign
core/webhook SignatureVersion
core/webhook SignedFields
core/webhook VerifySignature
```

`publishedPackages` gains `core/webhook`, and the README's count went red
before it was corrected (`TestTheCountsInTheProseAreTrue`, its line number
left out here):

```
README.md says "twenty packages" — 20 — and the tree holds 21.
```

`core/webhook` imports `crypto/hmac`, `crypto/sha256`, `encoding/base64`,
`strconv`, `strings` and `time`, and nothing else.

## 4. The validator and two generators

The design's open risk: does the tooling the repository documents accept an
OpenAPI 3.1 `webhooks` section, and what does it make of it? Measured on the
document the e2e ground serves (every module, the `search-pg`, `analytics`
and `webhook-out` plugins), dumped by a throwaway test calling the same
handler, against `openapitools/openapi-generator-cli:v7.10.0`, the image
`make openapi-validate` and `make openapi-client` run.

| Document | Bytes | `validate` |
|---|---|---|
| 5d62868d, before the change | 1,739,617 | `No validation issues detected.` exit 0 |
| this change, first form (no mark) | 1,800,025 | exit 0, one recommendation: `Unused model: WebhookoutDelivery` |
| this change, as committed (`x-internal`) | 1,800,389 | exit 0, the same recommendation |

The validator accepts the section. It does not read `webhooks` for
references, so it calls the envelope component unused; a copy of the first
form with `webhooks` deleted gave the same recommendation, which places it.

`generate`, first form, `typescript-fetch` and `go`, both exit 0 and both with
the generator's own warning that 3.1 generation "is in development and is not
officially supported yet". Each webhook was rendered as a PATH: thirteen
methods (`WebhookCartCompleted` …) in the clients' `WebhooksApi`, each
building `localBasePath + "/cart.completed"` and POSTing the envelope to this
server, which binds no such route. That is a callable lie in every generated
client, so the core marks every webhook entry `x-internal`. On the committed
document:

```
typescript-fetch EXIT 0 skipped=13 X-Gobit-in-apis=0
go EXIT 0 skipped=13 X-Gobit-in-apis=0
[main] INFO  o.o.codegen.DefaultGenerator - Operation (post cart.completed - webhook_cart_completed) not generated since x-internal is set to true
```

and both clients still carry the model a receiver decodes into
(`models/WebhookoutDelivery.ts`, `model_webhookout_delivery.go`, seven fields,
`redacted: Array<string>`).

`openapi-typescript` 7.13.0, the generator of the storefront repository's
`schema.d.ts`, run read-only from that repository's `node_modules` with its
output outside it: exit 0 on both documents. Before, it wrote
`export type webhooks = Record<string, never>;`; after, an
`export interface webhooks` keyed by topic whose `post` names
`operations["webhook_order_placed"]` and the rest, beside `paths` and not in
it, and `components.schemas.WebhookoutDelivery` with the seven fields.

## 5. Mutants

Each mutant one edit (the paired one two), run with `-count=1` after a
baseline run of every command it uses came back green, the tree restored after
each; the driver asserted every anchor matched exactly once. The plugin's
integration package ran whole against Docker; the e2e command ran the two
webhook tests on their own ground. One mutant (the private envelope) first
killed only by not compiling; it was rewritten to compile and run again.

| Mutant | Killed by |
|---|---|
| the length prefix taken out of the signed string | `TestTheLengthPrefixKeepsTwoMessagesApart`, `TestASignatureMatchesAnIndependentImplementation`, `ExampleVerifySignature` |
| `SignatureVersion` no longer a signed part | `TestASignatureMatchesAnIndependentImplementation`, `ExampleVerifySignature` |
| the attempt not signed | `TestEverySignedFieldChangesTheSignature/attempt`, `TestASignatureMatchesAnIndependentImplementation`, `ExampleVerifySignature` |
| `hmac.Equal` replaced by `==` | `TestASecretIsComparedInConstantTime` (its site moved to `core/webhook/signature.go`) |
| `HeaderEvent` = `X-Gobit-Topic` | `TestTheWireNamesAreTheWrittenOnes`; in the integration package `TestASignedDeliveryReachesAReceiverThatCanVerifyIt` and four more, whose receiver now spells the header names as literals |
| `SignatureVersion` = `v2` | `TestTheWireNamesAreTheWrittenOnes`, `TestASignatureMatchesAnIndependentImplementation`, `ExampleVerifySignature` |
| `json:"event_id"` renamed `eventId` | `TestADeliveryTravelsUnderItsSevenNames`, `TestTheSenderWritesThePublishedEnvelope`, `TestADeliveryTravelsAsThePublishedEnvelope` |
| `Redacted` gains `omitempty` | `TestADeliveryTravelsUnderItsSevenNames/nothing_withheld`, `TestTheSenderWritesThePublishedEnvelope`, `TestADeliveryTravelsAsThePublishedEnvelope`, `TestTheDocumentDescribesEveryForwardedTopic` |
| the envelope's fields reordered | `TestADeliveryTravelsUnderItsSevenNames`, `TestTheSenderWritesThePublishedEnvelope` |
| `core/webhook` imports an `internal/` package | `TestNoPublishedPackageImportsAnInternalOne` |
| the sender back on a private struct with an extra field | `TestTheSenderWritesThePublishedEnvelope`, `TestADeliveryTravelsAsThePublishedEnvelope` (strict decode) |
| the sender writes a nil `redacted` as null | `TestTheSenderWritesThePublishedEnvelope` |
| the sender writes a nil payload as null | `TestTheSenderWritesThePublishedEnvelope` |
| the sender heads the topic under a literal of its own | `TestTheSenderWritesThePublishedEnvelope`; the integration receivers |
| the sender writes `occurred_at` under another name after marshaling | `TestTheSenderWritesThePublishedEnvelope`; e2e `TestTheServedWebhooksAreWhatIsSent` (and not `TestARealOrderReachesARealWebhookReceiver`, which reads `data` alone) |
| the old `VerifySignature` always true | `TestTheOldNamesAreTheNewOnes` |
| the old `Sign` signs with another key | `TestTheOldNamesAreTheNewOnes` |
| the old `HeaderAttempt` names another header | `TestTheOldNamesAreTheNewOnes` |
| a forwarded topic not described | `TestTheDocumentDescribesEveryForwardedTopic` |
| a topic's `data` field dropped from the description | `TestTheDocumentDescribesEveryForwardedTopic` |
| a header not described | `TestTheDocumentDescribesEveryForwardedTopic` |
| the event header not pinned to its topic | `TestTheDocumentDescribesEveryForwardedTopic` |
| `data` closed with `additionalProperties: false` | `TestTheDocumentDescribesEveryForwardedTopic` |
| the envelope described in its request form | `TestTheDocumentDescribesEveryForwardedTopic` |
| `build` writes no `webhooks` | `TestAWebhookIsWrittenUnderWebhooks`, `TestTheDocumentDescribesEveryForwardedTopic` |
| an empty `webhooks` written | `TestAWebhookIsWrittenUnderWebhooks` |
| `DescribeWebhook` does not invalidate the cache | `TestAWebhookIsWrittenUnderWebhooks` |
| a nil `Security` written as null | `TestAWebhookIsWrittenUnderWebhooks`, `TestTheDocumentDescribesEveryForwardedTopic` |
| nil `Responses` written as null | `TestAWebhookIsWrittenUnderWebhooks` |
| no `operationId` derived | `TestAWebhookIsWrittenUnderWebhooks` |
| `x-internal` not written | `TestAWebhookIsWrittenUnderWebhooks` |
| a webhook body's request-form reference not renamed | killed by the first form's test of that rename; the review found the rename itself to be the fault (section 6, D3), and the test was replaced by its opposite |
| a field deleted from `docs/extending.md`'s table | `TestTheEventTableIsThePublishedPayloads` |
| a row deleted from the table | `TestTheEventTableIsThePublishedPayloads` |
| a field the table names that no publisher writes | `TestTheEventTableIsThePublishedPayloads` |
| a publisher gains a field and `TopicFields` follows, the table does not | `TestTheEventTableIsThePublishedPayloads` (and nothing else: the existing census passes it) |
| `core/webhook` not declared in `publishedPackages` | `TestThePublishedPackagesAreTheDeclaredOnes` |
| `core/webhook VerifySignature` left out of the inventory | `TestThePublishedNamesAreTheDeclaredOnes` |
| `core/openapi Doc.DescribeWebhook` left out of the inventory | `TestThePublishedNamesAreTheDeclaredOnes` |
| the README left at "twenty packages" | `TestTheCountsInTheProseAreTrue` |

Forty mutants, forty killed.

## 6. After the independent review

A review of the first form found two medium and eight low findings; each was
fixed in the same change, as a finding on a fix amends that fix. Every run
here is `GOTOOLCHAIN=go1.26.9`.

**O1, the empty secret.** `webhook.VerifySignature("", webhook.Sign("", f), f)`
returned true: HMAC is defined over an empty key, so a receiver whose secret
variable was missing from one deployment accepted any delivery an attacker
signed under it. `VerifySignature` now verifies nothing under an empty secret,
under both names. `TestAnEmptySecretVerifiesNothing` and
`TestASignatureUnderAKeyTheAttackerKnowsDoesNotVerify` are new, and the alias
test asks both verifiers one table of six questions.

**O2, the document's `string`.** The section typed every `data` field
`string`, and nothing held a publisher to it: a document typing them all
`integer` passed every lane. Of the two ways out, the gate was measured to be
reliable and taken. Every forwarded topic is published at nine sites in five
packages (`internal/modules/{cart,fulfillment,order,payment,product}/service`),
the payload built by a same-package function returning a map literal or by a
local map grown by index assignment — the two shapes the census already
reads, and a payload in any other shape already fails it.
`TestEveryPayloadValueIsAString` type-checks those packages from source, with
the export data `go list -export -deps` builds and the standard library's
importer, and holds each of the 37 values to the type `string` itself, a
declared string type refused too, since the in-memory bus hands a subscriber's
`.(string)` the value as it is. It runs in under a second on a warm build
cache. The describe test now pins each field's schema to `{"type": "string"}`
and the e2e test checks every value of a real delivery is a JSON string.

**D1, the table parser.** A topic on two rows was read with the last row
winning, a field named in a parenthesised note was dropped, and a row that
did not split into two cells was skipped. All three now fail, and
`TestTheEventTableParseRefusesWhatItCannotRead` tries each on a table the
document does not hold.

**D2, empty responses.** A webhook described with no answers was written with
`"responses": {}`:

```
nil-responses-old.json: Validation Error: {} should be non-empty
Failed validating 'minProperties' in schema['properties']['webhooks']['additionalProperties']['else']['properties']['post']['properties']['responses']
nil-responses.json: OK
```

(`openapi-spec-validator` 0.9.0, `--schema 3.1.0`, in a virtual environment.)
It is now written with one `default` answer.

**D3, the body's form.** A webhook body built from `Doc.SchemaOf` of a type the
document also publishes in its request form was renamed to `…Input`, whose
fields are not required. Webhook bodies are no longer renamed;
`TestAWebhookBodyKeepsTheWrittenForm` is the review's case.

**D4, redirects (D272).** The sender's client followed redirects: a 301, 302
or 303 became a GET with no body, counted as delivered when its target
answered 2xx, and a 307 or 308 sent the body on to any address, plain http
included. The client now follows none, and a 3xx is a failed attempt whose
error names its `Location`. The first redirect test ran 302, 307 and 308 and asserted the address pointed
at was never reached; section 7 replaced it.

**D5, one name described twice.** A second module describing a webhook name
silently replaced the first. It is now a clash, ADR 0036's rule for component
names: `Doc.Build` fails, naming the webhook and both modules. The module that
described a name describing it again still replaces its own.

**D6, D7, D8.** The ADR names the cost a receiver's `go.mod` takes on (gobit's
`go 1.26.9`). `x-internal` keeps server generators silent too: `go-server`
from the first form's committed document, exit 0, skipped all thirteen
(`Operation (post cart.completed - webhook_cart_completed) not generated since
x-internal is set to true`) and generated `model_webhookout_delivery.go`.
`docs/api-surfaces.md` says the paths are generated from the router tree and
the webhooks are described from what is sent.

The amended served document, dumped from the e2e ground as before (1,801,234
bytes): openapi-generator `validate` exit 0 with the same single
recommendation, `Unused model: WebhookoutDelivery`; `openapi-spec-validator
--schema 3.1.0` `OK`. The documents before the change and of the first form are
`OK` under the strict validator too.

Mutants of the amendment, each after a green baseline, `-count=1`, the tree
restored after each. The driver first wrote every mutant as an empty file (it
opened a file for writing before reading it), so that whole run was discarded;
one mutant's first form tripped `go vet` instead of a test and was rewritten.

| Mutant | Killed by |
|---|---|
| the empty-secret guard removed | `TestAnEmptySecretVerifiesNothing`, `TestTheOldNamesAreTheNewOnes` |
| the empty-secret guard answering true | `TestAnEmptySecretVerifiesNothing`, `TestTheOldNamesAreTheNewOnes` |
| the core accepting a forgery under the empty key | `TestASignatureUnderAKeyTheAttackerKnowsDoesNotVerify`, `TestTheOldNamesAreTheNewOnes` |
| the old name accepting a forgery under the empty key | `TestTheOldNamesAreTheNewOnes` |
| the old name OR-ing `webhook.VerifySignature("", …)`, the review's literal form | survives, and is equivalent after the fix: the call it adds answers false for an empty secret, so the disjunction adds nothing. The two forms of it that can be true are the two rows above |
| a publisher writing an int | `TestEveryPayloadValueIsAString` |
| a publisher writing a declared string type | `TestEveryPayloadValueIsAString` |
| an index assignment writing a declared string type | `TestEveryPayloadValueIsAString` |
| the document typing every data field `integer` (the review's M2) | `TestTheDocumentDescribesEveryForwardedTopic`; e2e `TestTheServedWebhooksAreWhatIsSent` |
| a topic on two rows read, the last winning | `TestTheEventTableParseRefusesWhatItCannotRead` |
| a row that does not split skipped | `TestTheEventTableParseRefusesWhatItCannotRead` |
| a note dropped before the fields are read | `TestTheEventTableParseRefusesWhatItCannotRead` |
| the table naming a topic on two rows (the review's M1) | `TestTheEventTableIsThePublishedPayloads` |
| the table naming a field inside a note (the review's M1b) | `TestTheEventTableIsThePublishedPayloads` |
| empty responses written as `{}` | `TestAWebhookIsWrittenUnderWebhooks` |
| a webhook body renamed to its request form again | `TestAWebhookBodyKeepsTheWrittenForm` |
| a second module's description replacing the first | `TestTwoModulesDescribingOneWebhookStopTheDocument` |
| one module's second description refused | `TestTwoModulesDescribingOneWebhookStopTheDocument` |
| the sender's client following redirects | the first redirect test (302, 307, 308), replaced in section 7 |
| a 3xx's error not naming where it pointed | the first redirect test, replaced in section 7 |

Twenty mutants: nineteen killed, one equivalent.

## 7. After the follow-up review

The follow-up review confirmed the ten findings above fixed and raised one
medium and five low findings; each was fixed in the same change.

**Y-O1, redirects.** Refusing every 3xx (section 6, D4) broke a receiver that
works through a 307 or 308: a framework that adds a trailing slash answers one
on every delivery, Go's client re-sent the body and the X-Gobit headers, and
the signature verified at the end. Such a receiver's queued deliveries carry
the URL they were queued with, so after an upgrade they could be sent nowhere
else. The sender now follows a 307 or 308 — the two that keep the method, the
body and the headers — at most three hops (`maxRedirects`) and only to an
address `validateURL`, the registration's own check, accepts; it never follows
a 301, 302 or 303, a redirect past the cap, or one to an address the rule
refuses. Tests: a 307 and a 308 to a relative address on the same receiver
delivered, the POST, the body and a signature that verifies arriving at the
end; 301, 302 and 303 failed with the address never asked; a 307 to
`http://receiver.example/hooks/` failed at the 307; a receiver answering 307 to
itself was asked four times (the attempt and three hops) before the attempt
failed. The CHANGELOG lists the change under Breaking changes, with a check to
run before upgrading.

**Y-D1, payload shapes.** The walker took a payload map's literal for the
whole payload whatever happened to the map after it, so a reassignment from a
helper, a `maps.Copy` and a builder whose second branch grows a map before
returning it each passed the census and the string gate with the added key and
value unread. It now fails closed: a builder must hand back a map literal from
every return, and a local payload map may only be defined from a literal,
grown by index assignment and handed to the event; anything else is a failure
naming the file and the function. No publisher in the tree tripped it. A
probe publisher in a sixth package using `maps.Copy`:

```
<the probe file>, zzProbe: the payload of [order.placed] is not in a shape this census reads — …
--- FAIL: TestTheTopicFieldsAreEveryPayloadsFields
--- FAIL: TestEveryPayloadValueIsAString
```

`TestAPayloadThisCensusCannotReadIsRefused` holds the five shapes: the two it
reads, the three it refuses.

**Y-D2, table rows.** A row whose first cell was empty, a topic without
backticks or a dash was skipped. Inside the table every row is now the
separator, recognized by its own shape, or a row whose first cell is
backticked topics and nothing else.

**Y-D3, the error last_error keeps.** It is cut at two hundred bytes and
repeated the delivery's URL before the address, so a realistic address was
cut off. The error now leads with the status and the absolute address
(`redirect 302 to "https://…" not followed: …`), and the test checks the
address survives `truncateError` for a hundred-byte URL and address.

**Y-D4, the rule for receivers in other languages.** The package
documentation's step 3 and the served description now say an empty secret
verifies nothing.

**Y-D5, the clash code.** `codeSchemaNameConflict`'s documentation now says it
also reports one webhook name described by two modules, and that the whole
document then answers 500, a choice for a name that is a topic a receiver
looks up rather than a component name to prefix.

Mutants of this round, after a green baseline, `-count=1`, the tree restored:

| Mutant | Killed by |
|---|---|
| every 3xx followed | `TestARedirectThatDropsThePostIsAFailedAttempt` (301, 302, 303) |
| the registration rule not asked | `TestARedirectPastTheRegistrationRuleIsAFailedAttempt` |
| the hop cap removed | `TestARedirectChainStopsAtTheCap` |
| the hop cap one short | `TestARedirectChainStopsAtTheCap` |
| no redirect followed at all | `TestARedirectThatKeepsThePostIsFollowed`, `TestARedirectChainStopsAtTheCap` |
| the signature header dropped on a followed redirect | `TestARedirectThatKeepsThePostIsFollowed` |
| the error leading with the delivery's URL again | `TestARedirectThatDropsThePostIsAFailedAttempt` |
| a local payload map read by its literal alone | `TestAPayloadThisCensusCannotReadIsRefused` |
| a builder read by any one literal return | `TestAPayloadThisCensusCannotReadIsRefused` |
| the event's Data not counted as a readable use | `TestAPayloadThisCensusCannotReadIsRefused`, `TestEveryPayloadValueIsAString`, `TestTheTopicFieldsAreEveryPayloadsFields`, `TestTheEventTableIsThePublishedPayloads` |
| a row with an empty first cell taken for the separator | `TestTheEventTableParseRefusesWhatItCannotRead` |
| the first cell not held to backticked topics | `TestTheEventTableParseRefusesWhatItCannotRead` |

Twelve mutants, twelve killed.
