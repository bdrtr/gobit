# ADR 0435 — A webhook receiver builds against a published contract

**Summary:** The outbound webhook's signature, headers and envelope are published as `core/webhook`, which the sender signs, heads and marshals every delivery with, and the topics and the fields each carries are described in the served `/openapi.json` under `webhooks`, from the values the plugin sends with.
It costs ten published names and one method on `openapi.Doc`, kept until 1.0.0, and `plugins/webhookout`'s nine signature names become deprecated aliases.

- **Status:** Accepted; amended by [0439](0439-an-id-gobit-hands-out-opens-nothing-at-the-storefront.md), whose served webhooks describe the cart topics without `cart_id`
- **Date:** 2026-10-10
- **Extends:** [0026](0026-the-published-surface-is-fourteen-packages.md), whose surface gains `core/webhook`; [0035](0035-the-schema-vocabulary-is-published.md), whose document gains `webhooks`

Measurement: [measurements/0435](../measurements/0435-what-a-receiver-had-to-spell.md)

## Context

A marketplace running as a separate process beside gobit received its events
from `webhook-out` and verified them with `plugins/webhookout`'s
`VerifySignature`, `SignedFields` and five header constants, which their godoc
said were exported for that receiver, while ADR 0026 promises `core/` alone.
The envelope's seven fields were documented on an unexported type. The topics
and fields it acted on were listed in `docs/extending.md`, which no test read,
and by `GET /admin/v1/webhooks/`, which asks for `webhook:read` (D271). It also
compared a parcel status, `delivered`, and the session cookie's name,
`gobit_session`.

## Decision

The outbound webhook's wire — `SignatureVersion`, the five header names,
`SignedFields`, `Sign`, `VerifySignature` and the envelope `Delivery` — is
published as `core/webhook`, which `plugins/webhookout` signs, heads and
marshals every delivery with, keeping its old names as deprecated aliases. The
topics and the fields each carries are published as an HTTP path is, in the
served `/openapi.json`, whose new `webhooks` section the plugin describes from
the `ForwardedTopics`, `TopicFields` and `Delivery` it sends with, and
`docs/extending.md`'s payload table joins the census that already holds
`TopicFields` to the publishers.

## Consequences

- A Go receiver imports one package that needs only the standard library, but
  its `go.mod` then requires gobit's and its `go 1.26.9`. It decodes into
  `webhook.Delivery` and spells no header or envelope field; `VerifySignature`
  verifies nothing under an empty secret. Any receiver reads the same shapes
  from `/openapi.json`, which asks for no key, wherever `webhook-out` runs.
- Renaming or removing a topic, a field, a header or an envelope field, or
  changing the signature, is a breaking change; a new topic or field is not,
  and `data` is left open to one. The request's headers and bytes did not change.
- The envelope's seven fields are required, since gobit writes the body;
  `data`'s are strings and not required, since a receiver's field list narrows
  them (ADR 0218). ADR 0363's rule is for bodies gobit reads. A test holds
  every forwarded topic's payload values to the type `string`.
- The publishers keep their own constants; the census holds `TopicFields` and
  the table to them. A webhook name two modules describe stops the document,
  as a component name does (ADR 0036).
- `delivered_at` is set exactly when a parcel is delivered and is in the
  document, so a receiver needs no status value; it forwards the request's
  `Cookie` header to `GET /store/v1/auth/session` rather than naming a cookie
  an installation may rename. Status values are not enumerated (known limits).
- Every webhook entry is marked `x-internal`: openapi-generator, which `make
  openapi-client` runs, renders a 3.1 webhook as a call to the server, and
  skips a marked one in clients and servers alike.
- The sender follows a 307 or 308, three hops at most and only to an address a
  registration would accept; any other redirect is a failed attempt naming
  where it pointed (D272).
- The nine aliases are constants, a type alias and two functions, never a
  variable; removing them is a breaking change of its own, with no date.

## Rejected

- Topic and field constants in `core/webhook`: some forty commerce names under the Go promise, and a constant compiled from one release says nothing about the server a separate process talks to.
- Publishing `plugins/webhookout`: its tables, job and admin types would be promised with the form.
- A receiver module under `contrib/`: it needs a tag of its own, which `contrib/identity-session` still lacks, and the sender would require it.
- A verifier that also checks freshness: a promise no receiver asked for; the window stays the receiver's.
- Standard Webhooks' headers and signing string: every registered receiver would break; a second scheme arrives under a new `SignatureVersion`.
- An AsyncAPI document: a second document beside the OpenAPI 3.1 one gobit serves, which has `webhooks`.
- Typing `Data` as `map[string]string`: it is the bus's payload as stored and sent, and a test, not a conversion in the sender, holds its values to strings.
- Enumerating status values in the document: every module's DTOs, wider than this fault.
