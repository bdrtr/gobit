# Observability and security — measured 2026-09-05

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

The checklist: slog structured logging, OpenTelemetry traces, Prometheus
metrics, JWT + refresh or opaque sessions, argon2id, rate limiting, input
validation, CSRF, PII in logs, and a KVKK retention/erasure flow designed from
the start.

### In place

1. **Structured logging with slog**, everywhere, with a request id on every line
   and a level chosen from the status (5xx error, 4xx warn, else info). An arch
   test audits the log assertions themselves.

2. **OpenTelemetry traces** over OTLP gRPC. There is no default endpoint on
   purpose — left empty, tracing is off and the process opens no connection —
   and `OTEL_EXPORTER_OTLP_INSECURE=true` is REFUSED at startup outside
   development, because traces carry paths, identities and error messages.

3. **Rate limiting**, in memory and in Redis, scoped per surface.

4. **Input validation**: unknown JSON fields are refused rather than ignored,
   bodies are size-bounded, and the errors are typed so the status is chosen by
   the error class rather than by the handler.

5. **CSRF**, in two layers and only where a cookie exists: the panel's cookie is
   SameSite=Strict AND its state-changing requests check the Origin header,
   which is what closes the subdomain case SameSite leaves open. The admin API
   needs neither, because it takes identity from a header only (ADR 0011) — a
   browser cannot be made to send one cross-site.

6. **PII in logs is better than the checklist assumes.** Request and response
   BODIES are never logged. The access log masks the query string against a
   deny-list that already carries the personal markers — mail/posta, phone/
   telefon/gsm/msisdn, iban, tckn, vkn, cvv/cvc, pin — as well as the credential
   ones, matched case-insensitively and by substring. The deny-list's cost is
   written down where it is defined: a parameter named tomorrow is logged
   unmasked until the name is added, and an allow-list was impossible because
   core cannot know a module's filter names (Principle 2.4).

   Error reports go further: `errorreport/policy.go` splits attributes into
   those that may travel and those that may not, and REPORTS which were removed
   rather than dropping them silently.

7. **Sensitive configuration is refused rather than warned about**: an
   unencrypted collector, a short signing secret and a non-loopback profiling
   listener all stop the startup in a shared environment.

### Present, but not the way the checklist names it

1. ~~**Metrics are OTLP, not Prometheus — and in the DEFAULT install there are
   no metrics at all.**~~ **BUILT 2026-09-08: ADR 0046 — metrics leave by SCRAPE
   and OTLP keeps the traces.** Only the second half of that heading survives:
   with `METRICS_ADDR` empty, which is still the default, no meter provider is
   built and the two `core/http` instruments record into OTel's no-ops. The
   first half is now false. ~~There is a meter provider and an exporter
   (`otlpmetricgrpc`) and an export interval~~ **Corrected 2026-09-07: only when
   an OTLP endpoint is configured.** `observability.Setup` returned before it
   built either provider when `OTEL_EXPORTER_OTLP_ENDPOINT` was empty, and that
   variable has no default — so a stock
   installation had no meter provider, and the instruments recorded into no-ops.
   The stated flat is what a reader planning a scrape would have believed. ~~Either
   way there is no `/metrics`
   endpoint for Prometheus to scrape. An installation that wants Prometheus
   points a collector at the OTLP endpoint and scrapes the collector; one that
   wants gobit to expose the endpoint itself does not have that today.~~ **It has
   it now:** `METRICS_ADDR` opens a listener of its own that answers `/metrics`
   in the Prometheus text format, the OTLP push exporter and
   `METRIC_EXPORT_INTERVAL` are gone, and the two signals have two switches, so
   an address that mentions metrics is what turns metrics on. The endpoint is
   unauthenticated and may be bound off loopback; the address chosen is the
   whole guard.

2. **There is no refresh token, and the reason is a design that already
   exists.** The admin token is a JWT, and revocation works through a SESSION
   ANCHOR: a token issued before the identity's anchor is refused, and a logout
   or a password change moves the anchor. So the token is stateless AND
   revocable, which is the property a refresh token is usually introduced to
   buy. What is missing is the other property — per-device revocation — and that
   is a written refusal (`auth/service/session.go`), not an oversight.

   The open question is the TTL: without a refresh flow, `JWT_TTL` is a straight
   trade between how long a leaked token lives and how often an operator signs
   in. Nothing in the repository states what that trade should be.

3. **Passwords are bcrypt, not argon2id.** The cost is configurable and the
   verification path is constant-time against a dummy hash so a missing user and
   a wrong password take the same time. bcrypt is not a defect — it is the
   weaker of the two against GPU attack, and the module's own reasoning about
   hashing is written for API keys (SHA-256, and why NOT bcrypt there) rather
   than for passwords.

### Gaps

1. **There is no KVKK/GDPR erasure or export flow, and deletion is SOFT
   everywhere.** `DeleteCustomer` stamps `deleted_at`; nothing hard-deletes and
   nothing anonymises. ~~The word KVKK appears exactly once in the repository, in
   a notification test, observing that not storing a second copy of the
   recipient address keeps the number of places to clean small — which is the
   right instinct and the only trace of the requirement anywhere.~~ **Corrected
   2026-09-06: it appears twice in the code, and the load-bearing one is not the
   test.** Both are in the notification module and both make the same argument:
   the schema comment in the module's `000001_notification_init` migration, under
   the heading that the recipient address is NOT STORED, says a second copy
   raises the number of places that have to be cleaned up on an erasure request,
   and `TestTheLogCarriesNORecipientAddressCOLUMN` holds that at the schema level
   so code that does not write the address today cannot write it tomorrow. Both
   strings predate this section by five days, so the count was wrong when it was
   written rather than overtaken. It is still the right instinct and still the
   only trace of the requirement in the code.

   Personal data is currently in at least seven places: `customer` (e-mail,
   names, phone), `customer_address`, `orders.email`, `carts.email`,
   `auth_user`, `notification_deliveries` (by reference, not address), and
   `invoices` (buyer name, e-mail, address, tax number).

   What it would touch: a core capability modules opt into — the same shape the
   link registry and the Query layer already have — because the alternative is
   an erasure that each module implements its own way and one of them forgets.

2. **An invoice is a RETENTION OBLIGATION that conflicts with erasure, and
   nothing writes the conflict down.** The invoice module's whole design says
   the document is immutable and its number may never leave a hole (ADR 0024),
   and the document carries the buyer's name, address and tax number. A KVKK
   erasure request cannot delete it: the legal retention period wins. That is
   almost certainly the correct answer — and it has to be a written decision,
   because the alternative is somebody implementing erasure later, deleting an
   invoice row, and putting the hole in the series that ADR 0024 exists to
   prevent.

3. **No guard keeps personal data out of a log line.** No line carries an e-mail
   or a name today — that was checked, not assumed — but it holds by discipline
   rather than by construction. The query-string deny-list is enforced in code;
   what an individual module writes into its own log is not checked by anything,
   and this repository already has an arch test for the shape of log assertions,
   so the machinery to check it exists.

4. **Nothing states how long anything is kept.** Idempotency records and the
   audit log have retention settings; carts, orders, notification deliveries and
   the audit log itself have no stated lifetime. A retention policy is the half
   of KVKK that is not erasure, and it is the half that has to be designed
   before there is data to apply it to.

---
