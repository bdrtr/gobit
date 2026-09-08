# Turkey-specific — measured against the brief, 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The brief: e-Fatura/e-Arsiv in the core; the domestic carriers (Yurtici, Aras,
MNG, PTT) behind one interface with tracking webhooks; iyzico/PayTR/Param plus
installment-table calculation; and KVKK consent records with ready-made
download/delete endpoints.

**e-Fatura is the one already answered.** The document, its lines, its parties
and its gap-free numbering landed this session (ADR 0024); what remains is the
transmission, which needs the merchant's own certificate and an integrator
contract and is therefore a plugin's job, not the framework's. The provider slot
is the shape it plugs into.

### Carriers: the contract is four methods and tracking is not one of them

`FulfillmentProvider` has exactly `ID`, `Quote`, `Create`, `Cancel`. There is no
label method and no label type; there is **no `Track()` and no status poll** —
`TrackingNumber` and `TrackingURL` exist only as OUTPUT FIELDS of `Create`.

Three things a Turkish carrier integration would hit immediately:

- **`QuoteInput` cannot express what a Turkish carrier prices on.** It carries
  `OptionID`, `CurrencyCode`, `CountryCode`, `TotalWeight` in grams, `ItemCount`
  and an untyped `Data`. There is no destination postal code, no il/ilce (province/district), no
  origin address and no dimensions — and domestic carriers price on **desi**
  (volumetric) and on district. All of it would have to travel through the
  untyped bag. **Still true, and DECIDED 2026-09-08: ADR 0065 — the input is
  not widened, because the blocker is the producer rather than the struct.** Two
  facts this bullet did not reach: `product_variant` carries a weight and no
  box, and the one field of this shape the input already has is handed a literal
  zero by the only trusted producer. See
  [0065](0065-carrier-quote-input.md).
- ~~**The status vocabulary is four values** — pending, shipped, delivered,
  canceled — pinned by a database CHECK. There is no "in transit", no "at
  branch", no "delivery attempt failed", and no **"returned to sender" (iade)**,
  which is a real carrier state a shop must act on.~~ **The iade half was closed
  2026-09-06.** `returned` is the fifth value, terminal, with a moment that
  mirrors it exactly. The transit statuses stay absent and that is now a
  DECISION rather than an omission: "at branch" and "delivery attempt failed"
  are positions on a journey that the module records nowhere and no consumer
  asks for, while a return changes what the shop must do next.
- ~~**The state machine is strict and one-way**, and a carrier's event stream is
  not. `DeliverAction` from `pending` is a CONFLICT — delivered may not skip
  shipped — so a webhook that arrives out of order or twice hits an error rather
  than converging.~~ **Corrected 2026-09-06, and the measurement is worth
  keeping: the module tolerated REPEATS and refused REORDERING.** The whole
  table was printed by a throwaway probe rather than read by eye — a second
  ship, deliver or cancel landed on a no-op, while pending + deliver and
  delivered + ship were both conflicts. The two out-of-order pairs converge now;
  see D24.

One provider ships: the manual one, which makes no network call and returns
whatever tracking number the caller passed it. There is no carrier plugin.

`MarkShipped` and `MarkDelivered` exist and are admin-scoped, and their godoc
already names the intended source: *"THE PROVIDER IS NOT CALLED: this method
records the fact the carrier REPORTED (a webhook or an administrator action)."*
~~**The word "webhook" appears exactly once in the entire non-test codebase — in
that comment.**~~ **Re-measured 2026-09-06: 131 occurrences in 11 non-test Go
files.** The inbound callback ring (ADR 0028) and the outbound sender (C5)
landed in between. What has NOT changed is the thing that sentence was really
about — an inbound carrier event still has no way into this module, which is now
recorded on the C6 row with the measurement behind it.

### ~~There is no inbound webhook machinery, and the one working callback is unguarded~~ — built 2026-09-05 (B1, D1)

**Everything below this heading was true when it was measured and the paragraph
that follows is kept for the argument it makes, not as a description of today.**
`CallbackRegistry` in the core HTTP package is the class this passage asked for:
a registered callback route gets a quota, a body limit, a timeout, a signature
check enforced before anything reads the payload, and a replay window derived
from the signed fields. PayTR was converted onto it at the same URL, and
`TestEveryStateChangingRouteIsGuarded` now fails a write bound outside the
guarded prefixes. What a carrier still lacks is not the door but the room behind
it — see the C6 row.

No webhook registry, no signature middleware, no replay/dedup store, no delivery
receipts. The PayTR callback is the only working example and **none of it is
reusable**: its path is a plugin constant, its handler is a method on the
plugin's module, its HMAC lives in the plugin package, and its response protocol
is a bare text token because PayTR reads the body rather than the status.

More importantly, that path sits OUTSIDE the guarded prefixes — deliberately,
because PayTR carries neither a publishable key nor a bearer token. The
consequence, measured: `/paytr/callback` gets **no auth, no rate limit, no
idempotency middleware, no audit and no CORS**. Its only protection is the HMAC
check inside the handler, and there is no global request-body size limit
anywhere in the core.

Four carriers plus e-invoice plus payment callbacks means five more such paths.
**A guarded inbound-callback class — signature verification, replay window, body
limit, rate limit, audit — is the thing to build once**, and it is the same
machinery every item in this brief needs.

### Installments: there is nowhere for a second amount to exist

No installment, BIN, card-bank or per-option-total concept exists. The only
occurrences of the word are PayTR's own wire fields.

The contract blocks it in two places at once:

- **There is no return path for an installment quote.** `Session`, `AuthResult`
  and `SessionInspection` each carry a single scalar amount. There is no list
  type, so "these are the 3/6/9/12-month options and their totals" cannot come
  back from a provider.
- **The customer-paid amount is forced equal to the order total by four
  independent guards**, including database CHECK constraints. A vade farki — an
  installment surcharge the customer pays and the merchant does not receive — has
  nowhere to live in the money model.

That second point is the real one, and it is shared with the marketplace item:
both need the model to admit that **what the customer pays and what the merchant
receives can differ.** Today it cannot, and that is enforced in the schema.

No iyzico and no Param provider exist; Stripe is a declared skeleton.

### KVKK: zero consent records, and no ADR covers privacy at all

- **No consent record of any kind.** No table, no column, no timestamp of
  agreement — for marketing, for terms, for anything.
- **None of the twenty-eight ADRs covers privacy.** ADR 0008 governs the customer
  identity trust boundary, which is authentication, not data protection.
  Re-counted 2026-09-06: `docs/adr/` holds 0001 through 0028, and the three that
  landed after this bullet was written — the published surface, the composition
  root and the inbound callback ring — do not touch data protection either, so
  only the numeral moved.
- **No export endpoint, and a customer cannot request their own deletion** — the
  store surface has no delete handler; the only delete is admin-only.
- **`DeleteCustomer` is a pure soft delete**, and deliberately partial: group
  memberships are left in place with a comment saying they will cascade "when the
  record is one day really deleted" — a real delete that nothing performs.
- **Deleting a customer notifies nobody.** The customer module publishes no
  events at all, so no other module can learn it must clean its own copy — and
  Principle 2.2 forbids the foreign keys that would cascade.

Personal data sits in ten tables across eight modules and a plugin.

Two decisions come before any schema:

1. **The invoice retention conflict** (already recorded): the document carries
   the buyer's name, tax number and address, and its whole design says it is
   immutable and its numbering may never have a hole.
2. **What gobit IS, legally.** A data controller, or a library whose embedder is
   the controller? ADR 0025 makes gobit a library, which points at the second —
   and that answer changes what the framework owes from "implement consent" to
   "give the embedder the hooks and the erasure contract".

---
