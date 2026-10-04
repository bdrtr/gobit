# Changelog

The format follows [Keep a Changelog](https://keepachangelog.com/en/1.1.0/),
and versioning follows [Semantic Versioning](https://semver.org/).

Throughout `0.x`, **breaking changes may arrive in minor releases**: the API
surface is not fixed yet, and an endpoint may move for the sake of a better
design. It is fixed with `1.0.0`.

## [Unreleased]

### Breaking changes

- **A parcel on a return option names the return it brings back** (ADR 0384,
  D234). **For integrators:** `POST /admin/v1/fulfillments` takes `return_id`;
  a return option without one, or an outgoing option with one, answers 422
  `fulfillment_option_direction_mismatch`, and
  `POST /admin/v1/orders/{id}/fulfillments` on a return option answers 422
  where it used to open a parcel. An unknown option answers 404 before the
  dispatch bound is asked. **For operators:** migration 000006 adds
  `fulfillments.return_id`.

- **A passkey whose counter does not advance is suspended** (ADR 0382, D232).
  **For integrators:** `identitypasskey.Credentials.Used(ctx, credentialID)` is
  replaced by `SignedIn(ctx, identitypasskey.Assertion)`, which compares the
  assertion's signature counter with the stored one under a lock and records
  the count, the backup state, the latched user verification and the moment; a
  store bound through `Options.Credentials` implements it as its godoc says.
  `identitypasskey.KeyNotices` gains `SendPasskeySuspended`, sent once by the
  sign-in that suspends a key; a messenger bound through `Options.KeyNotices`
  implements it. `POST /store/v1/auth/passkey/sign-in/finish` answers 403
  `identity_passkey_key_suspended` to a device-bound key whose counter did not
  advance and to every later sign-in with it, 401 `identity_passkey_refused` to
  the same count again within four minutes, a finish sent twice among them,
  and 500 `identity_passkey_unavailable` when the sign-in cannot be recorded;
  it issued a session in all three cases. `GET /store/v1/auth/passkey/keys`
  carries `suspended_at`, a suspended key is removable and is not counted as a
  way in, and an account holding one is listed even when whether it has
  another way in cannot be checked, its key that signs in not removable for
  `identity_passkey_unavailable`. **For operators:** migration
  000003 adds `passkey_credentials.suspended_at`, and a suspension is logged at
  WARN as "identity-passkey: a key's signature counter did not advance, so the
  key is suspended".

- **A shopper does not change their address unproven** (ADR 0376, D224).
  **For integrators:** `PUT /store/v1/customers/{id}` no longer takes
  `email`; a body carrying it answers 422 `customer_invalid_body` and writes
  nothing. Nothing on the storefront proved the shopper owned the new
  address, which receives the account's mail. The operator's
  `PUT /admin/v1/customers/{id}` keeps the field.

- **A request body requires no field** (ADR 0363, D211). **For
  integrators:** the request bodies of `/openapi.json` list no required
  field, so a client generated from it sends only the fields it sets; a
  field the service needs is refused at run time with its reason, as
  before. The request forms of `CustomerSegmentCondition`,
  `CustomerSegmentRule`, `ErasureRequest`, `OrderAddress` and
  `ProductBundleComponent` are published as `CustomerSegmentConditionInput`,
  `CustomerSegmentRuleInput`, `ErasureRequestInput`, `OrderAddressInput` and
  `ProductBundleComponentInput`; regenerate a client. Responses are
  unchanged.

- **An operator's writes reach only an operator's cart** (ADR 0299, D200).
  **For integrators:** `POST /admin/v1/carts/{id}/line-items`, the two address
  writes, the shipping method writes and `POST /admin/v1/carts/{id}/complete`
  answer 409 `cart_opened_by_shopper` on a cart the storefront opened, and on
  every cart opened before ADR 0296. **For operators:** the panel's cart page
  offers no form on a shopper's cart; a telephone order in progress across the
  upgrade is opened again.

- **Production registers no manual provider** (ADR 0283, D194). **For
  operators:** with `APP_ENV=production` the `manual` payment provider, which
  authorizes and captures whatever the caller names, is no longer registered: a
  shopper could complete a cart with it and receive an order recorded as paid
  with nothing paid. Development and staging keep it. A production shop takes
  payment through a provider plugin, the gift card and the balance tenders.

- **A second factor is changed only with itself** (ADR 0264, D182, D183).
  **For integrators:** `DELETE /admin/v1/auth/mfa` is gone; the removal is
  `POST /admin/v1/auth/mfa/remove`. It and `POST /admin/v1/auth/mfa` on an
  account that holds a confirmed factor take `{"code": "..."}`, the six digits
  the confirmed factor shows now; without it they answer 403
  `auth_mfa_required`, with a wrong one `auth_mfa_code_wrong`, and a wrong code
  counts toward the account's lock (`auth_mfa_locked`). **For operators:** the
  three endpoints on one's own factor no longer need `auth:read`, so every
  operator who can sign in can protect their account.

### Fixes

- **A tax rate can be tried on past orders** (ADR 0387). **For API consumers:**
  `GET /admin/v1/tax-rates/{id}/trial?from=&to=` with `rate_bps`, repeated
  `rule=reference:reference_id` and repeated `drop_rule` taxes every taxable
  line of the period's uncanceled orders in the rate's country with today's
  tables and with the change, from the amount the cart sent; it answers per
  currency `charged`, `baseline`, `trial`, `lines_reached` and
  `lines_changed`, the hundred orders the change moves most, and what it
  assumed. A change a write would refuse answers 409
  `tax_trial_change_refused` or `tax_stack_exceeds_base`, a province's rate
  409 `tax_trial_rate_unreached`, a rate under an external provider 409
  `tax_trial_provider_external`, a malformed change 422
  `tax_trial_invalid_change`, a `rate_bps` outside 0 to 10000 422
  `tax_invalid_input`, and a missing or malformed `from` or `to`, or a
  future `to`, 422 `tax_trial_invalid_period`. Both ends are RFC 3339 and in the past; a
  period that ends before it starts or is longer than 93 days answers 422
  `cart_workflow_trial_invalid`, one holding more than 5,000 orders 422
  `cart_workflow_trial_too_wide`, and one whose orders hold more than
  100,000 taxable lines 422 `tax_invalid_input` once they are read. It needs
  `order:read` beside `tax:read`. Nothing is written. **For integrators:** the cart flows'
  `Taxes` surface gains `CompareRateJSON`, so a `tax.interop` registered
  without it fails wiring.

- **A stack member is not raised past its line** (D237). **For API
  consumers:** `PUT /admin/v1/tax-rates/{id}` with a `rate_bps` that makes the
  rate's stack take more than the line answers 409 `tax_stack_exceeds_base`,
  as creating such a stack always did, and the panel's correction of a rate
  refuses it with the stack's message; before, the value was written, and a
  cart line whose floored stack components summed past its amount then failed
  with 500.
- **The order page credits, changes a delivery and corrects the address**
  (ADR 0388, D238). **For operators:** an order's page lists its credits and,
  under `order:write`, credits any order but a canceled one with a reason and
  a note; an operator who may write orders sees the order's deliveries with
  what each costs, and on a pending order puts one on another option the shop
  quotes at the price shown, refused when that price moved; and a pending
  order's shipping address is corrected from a form prefilled with it, the
  country fixed, refused when the address was corrected since unless it says
  what the order ships to now, and a refused form keeps what was typed. The
  same form sent twice acts once. **For integrators:** a credit may name the
  credited total read, refused with `order_credit_moved`; the fulfilling flow
  lists the options an order's delivery can be put on and a change may name
  the price it was shown, refused with `fulfilling_quote_moved`; a correction
  may name the shipping address row read, which the order entity publishes as
  `shipping_address_id`, refused with `order_address_revised` unless it says
  what the current row says. The admin API is unchanged.

- **A completed order says so on the bus** (ADR 0386, D235). **For integrators:**
  completing an order, through the API, the panel or the interop surface,
  publishes `order.completed` (`order_id`, `completed_at`), written into the
  outbox in the completion's transaction; a completion whose event cannot be
  written is refused. The webhook plugin forwards it. Archiving publishes
  nothing, and there is no transition hook beside the bus. **For operators:**
  the notification module mails the order's address with template
  `order.completed`; the SMTP provider is asked only once `SMTP_TEMPLATE_DIR`
  holds `order.completed.tmpl`, the default `log` provider logs one more
  warning per completed order, and another provider is asked for the template
  as for any other. A failed completion mail is resent like a confirmation.
  **For provider authors:** `core/provider` gains the optional
  `TemplateHolder` (`HoldsTemplate(name string) bool`); a notification
  provider that refuses a template it has no copy for implements it, or every
  completed order becomes a failed delivery and two retries.

- **Goods a customer sends back travel in a parcel of their own** (ADR 0384,
  D234): the parcel is bounded by its return, not refused as more than the
  order owes, and is bound to no order, so the dispatch bound and both stock
  targets never count it. A return that awaits no goods answers 409
  `fulfillment_return_not_awaited`, and a parcel's record carries `return_id`.

- **An operator generates a VAPID key with the binary** (ADR 0383, D233).
  **For operators:** `gobit webpush-key` prints a new web-push signing key
  pair, the private half as the `WEBPUSH_VAPID_PRIVATE_KEY=` line and the
  public half as a comment to check the running plugin against (the storefront
  still reads its `applicationServerKey` from `GET /store/v1/webpush/vapid-key`);
  it needs no configuration. Every plugin error for a missing or malformed key
  names it, where one named a command that did not exist and the others named
  none.

- **An account is told its passkeys changed** (ADR 0381). **For
  integrators:** `contrib/identity-passkey` takes an optional
  `Options.KeyNotices`; bound, it is called with the customer id and the key's
  id after a key the account did not hold is registered and after a key is
  removed, and the installation finds the address. A notice that cannot be
  sent is logged and the request still succeeds.

- **An account notice outlives the caller, not the mailer** (D231). **For
  integrators:** `contrib/identity-session` sends `AccountNotices` on a context
  the caller hanging up does not cancel, ended after
  `identitysession.DefaultNoticeTimeout` (fifteen seconds). A mailer that needs
  longer queues the message and returns.

- **A passkey is not written over under its id** (D229). **For integrators:**
  `POST /store/v1/auth/passkey/register/finish` answers 401
  `identity_passkey_refused` to a key that names one the account holds with
  another public key, and to one naming another account's key, which answered
  500; the account's own key sent again answers 204 and writes nothing. A
  caller holding the session could replace the owner's key, or rewrite its
  flags so the owner's device stopped signing in.

- **The environment template and the Makefile are English** (D227). **For
  operators:** the comments of `.env.example` are English; its settings are
  unchanged. **For contributors:** `make seed` takes `PRODUCTS` and `MULTI`,
  and `make openapi-client` takes `CLIENT_LANG`, where they took `URUNLER`,
  `COKLU` and `DIL`; the old names are ignored without a word.

- **An account is told what changed** (ADR 0379). **For integrators:**
  `contrib/identity-session` takes an optional `Options.AccountNotices`; bound,
  it is called with the account's address after a password reset or change and
  with the old and the new address after an account moves. A notice that
  cannot be sent is logged and the request still succeeds.

- **The panel corrects a tax rate** (ADR 0378). **For operators:** each rate
  on the Taxes screen corrects its name and its rate, typed as a percent, for
  an operator holding `tax:write`; a rate someone else changed since the page
  was drawn is refused and the form comes back with what was typed.
  **For integrators:** the tax module provides `tax.admin` in the container.

- **A shopper moves their account by proving the new address** (ADR 0377).
  **For integrators:** `contrib/identity-session` mounts
  `POST /store/v1/auth/email` with `{"new_email", "current_password"}`, which
  sends a link to the new address and answers 202, and
  `POST /store/v1/auth/email/confirm` with `{"token"}`, which moves the account
  there. It is mounted when `Options.AddressProof` is bound and the bound
  `Accounts` implements `AddressChanges`; the customer module offers
  `ChangeCustomerEmail` on its cross-module surface for that. Migration 000005
  adds `customer_address_changes`.

- **A signed-in shopper changes their password** (ADR 0375). **For
  integrators:** `contrib/identity-session` mounts `POST /store/v1/auth/password`
  with `{"current_password", "new_password"}`; a wrong current password answers
  403 `identity_session_current_password_wrong`, and a success ends the
  shopper's other sessions and renews this one. A credential store you bind
  yourself offers it by implementing `CustomerCredentials`.

- **A replaced password ends the sessions before it** (ADR 0374, D222). **For
  integrators:** `contrib/identity-session` refuses a session cookie issued
  before the customer's password was last reset or replaced, and
  `POST /store/v1/auth/sessions/revoke-others` ends every other session of the
  signed-in shopper, renewing the caller's. Migration 000004 adds
  `customer_credentials.sessions_valid_from`; a credential store you bind
  yourself takes part by implementing `SessionAnchors`. A cookie issued before
  this release keeps working until the customer's first reset.
  `GET /store/v1/auth/session` answers 500 `identity_session_unavailable` when
  that moment cannot be read. **For operators:** replacing a customer's
  password through `PUT /admin/v1/customer-credentials` signs them out
  everywhere.

- **A guest checkout is not an account** (D221). **For integrators:**
  `examples/starter` lets a shopper who once bought as a guest register with
  the same address, and records an account it opens as one (`has_account`);
  an `identitysession.Accounts` implementation answers
  `CustomerIDForEmail` with the customer whose ACCOUNT the address is, never a
  guest record.

- **A shopper resets a forgotten password** (ADR 0373). **For integrators:**
  `contrib/identity-session` mounts `POST /store/v1/auth/password-reset` and
  `.../confirm` when `Options.PasswordReset` is bound; the request answers 202
  for any address and mails a single-use link to an account's, and the
  confirmation replaces the password and signs the person in. **Migration:**
  identity-session's 000003 adds `customer_password_resets`.

- **A buyer's review says they bought it** (ADR 0372). **For integrators:**
  a storefront review whose request proves a customer who bought the product
  answers `verified_purchase: true`, in the listing and in the admin reads; the
  customer is not stored. **Migration:** the review module's 000004 adds the
  column.

- **An identity that proves nobody refuses** (ADR 0371, D220). **For
  operators:** with `contrib/identity-session` bound, a storefront request
  naming a customer with no session, an expired one or a forged one answers
  401 `identity_refused`, where it answered 500 `internal_error`. **For
  integrators:** `corehttp.ProvenCustomerIfAny` returns the customer a request
  proves, or nobody, and `core/identitytest.Contract` fails an identity that
  answers a request carrying nothing as unavailable or internal.

- **One binding resolves the storefront's customer identity** (ADR 0370).
  **For operators:** the payment, customer, order, cart and b2b modules log
  `customer identity bound` with a `module` attribute when an identity is
  bound, where three of them logged their own messages and two nothing; the
  warning with nothing bound is unchanged.

- **An absent identity is warned of as the installation answers it**
  (D219). **For operators:** with no customer identity bound, the b2b and
  cart modules now warn that a request naming a customer is refused, as it
  has been since ADR 0125, and warn that it is believed only where
  `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` is set; `docs/security.md`
  says the same.

- **The address book keeps a province** (ADR 0369, D218). **For
  integrators:** a customer's saved address takes and answers `province`, the
  unit under the country (an il in Turkey), on the storefront and admin address
  endpoints and under the customer provider's address keys, so a storefront
  copies it to the cart with the rest; an address saved before holds it empty.
  **For operators:** the panel's address forms ask for it, a correction
  compares it, and an erasure empties it. **Migration:** the customer module's
  000007 adds the column.

- **A module keys a client as the installation does** (ADR 0368, D217).
  **For operators:** behind a proxy `TRUSTED_PROXY_HOPS` now reaches
  `contrib/identity-session`'s registration limit, so each shopper has a
  quota of their own rather than the whole shop sharing the proxy's. **For
  integrators:** `corehttp.ClientKeyName` names the installation's client
  key in the container; `identitysession.Options.LimitKey` overrides it.

- **identity-session describes its request bodies** (D216). **For
  integrators:** the sign-in, registration, verification and credential
  routes of `contrib/identity-session` describe their bodies in
  `/openapi.json`, so a generated client can send them; regenerate a client
  generated before.

- **A customer lists their own orders** (ADR 0367). **For integrators:**
  `GET /store/v1/customers/{id}/orders` answers a page of the proven
  customer's orders, newest first, refusing another customer's list with
  403 `identity_mismatch` and an installation with no customer identity
  with 401 `identity_not_bound`.

- **A storefront asks whom its session proves** (ADR 0366, D215). **For
  integrators:** with `contrib/identity-session` bound, `GET
  /store/v1/auth/session` answers `customer_id` and `expires_at` for the
  session cookie, uncached, and 401 `identity_session_none` when the request
  proves nobody; a storefront that signed a shopper in reads the id the
  customer routes take from it.

- **An order line names its product** (ADR 0365, D214). **For
  integrators:** an order line carries `product_title`, its product's title
  as it was when the order was placed, in the admin and store order records
  and on the order line entity; an order placed before carries none. **For
  operators:** the order page and an invoice row print the product beside
  the variant, "Kenya AA — 1 kg / Filtre", rather than the variant alone.

- **A credential that could not be checked is not refused** (ADR 0364,
  D213). **For integrators:** while the database holding the identities is
  out of reach, `/admin/v1` and `/store/v1` answer 503 or 500 with the code
  `auth_unchecked` rather than 401, so a storefront does not take an outage
  for a revoked key. **For operators:** the panel keeps the session through
  such an outage and says so, rather than signing everyone out.
  `corehttp.AuthenticatorFailure` tells the two apart for an embedder's own
  guard.

- **The panel corrects a region** (ADR 0362). **For operators:** each row
  of the Regions screen corrects its region's name, whether taxes are
  computed for it and its tax rate under `region:write`. **For
  integrators:** `region.admin` corrects a region from the terms read,
  refused with `region_revised` when they changed since.

- **The order list narrows to a status** (ADR 0361). **For operators:**
  the order list shows the pending, completed, archived or canceled orders
  alone, beside its other boxes.

- **The panel moves a default and removes an address** (ADR 0360). **For
  operators:** each address on a customer's page is made their default
  shipping or billing address, or removed, under `customer:write`. **For
  integrators:** `customer.admin` moves a default and removes an address
  as the API does.

- **The panel adds a customer's address** (ADR 0359). **For operators:** a
  customer's page adds an address under `customer:write`, as their default
  shipping or billing address when ticked. **For integrators:**
  `customer.admin` adds an address as the API's create does.

- **A customer's page lists their orders** (ADR 0358). **For operators:**
  a customer's page shows their newest orders to an operator who may read
  the orders, and the order list narrows to one customer's orders.

- **The panel lists the payments** (ADR 0357). **For operators:** a
  Payments screen lists every order's payment under `payment:read`, the
  money held and still to be captured or recorded first, then by status,
  each with its order for an operator who may read the orders.

- **The panel lists the parcels** (ADR 0356). **For operators:** a Parcels
  screen lists the parcels of every order under `fulfillment:read`, the
  ones still to be shipped first, then by status, each with its carrier
  and tracking, and its order for an operator who may read the orders.

- **The panel lists the taxes** (ADR 0355). **For operators:** a Taxes
  screen shows each country's and province's tax rates, the default
  marked, under `tax:read`.

- **The panel lists the regions** (ADR 0354). **For operators:** a Regions
  screen shows each region's currency, tax rate, whether taxes are computed
  and the countries it covers, under `region:read`.

- **The panel makes a sales channel** (ADR 0353). **For operators:** the
  Sales channels screen makes a channel under `admin`, enabled or not; a
  name another channel holds is refused. **For integrators:**
  `auth.admin` makes a channel as the API's create does.

- **The panel corrects the sales channels** (ADR 0352). **For operators:**
  a Sales channels screen lists the channels under `auth:read`, and each
  row renames, describes or disables its channel under `admin`; a channel
  another operator corrected meanwhile is refused. **For integrators:**
  `auth.admin` corrects a channel from the terms read, refusing
  `auth_sales_channel_revised`.

- **The panel makes an API key** (ADR 0351). **For operators:** the API
  keys screen makes a secret key with the privileges ticked, or a
  publishable one attached to the sales channels ticked, under `admin`,
  and shows its token once. **For integrators:** `auth.admin` makes a key,
  an empty privilege list making no administrator's key.

- **The panel lists and revokes the API keys** (ADR 0350). **For
  operators:** an API keys screen lists the keys integrations call the API
  with under `auth:read`, the open ones first, with their privileges and
  when they were last used, and revokes one under `admin`. **For
  integrators:** `auth.admin` lists the keys with their tokens redacted and
  revokes one.

- **The panel removes a user** (ADR 0349). **For operators:** a user's
  page removes them under `admin`, their sessions ending with them; the
  last administrator is kept, and an operator cannot remove themselves
  from the panel. **For integrators:** `auth.admin` removes a user as the
  API's delete does.

- **The panel invites a user** (ADR 0348). **For operators:** the Users
  screen invites a colleague under `admin`, with the privileges ticked and
  none when none is; they set their own password from the invitation, and
  their page sends it again. **For integrators:** `auth.admin` opens and
  invites a user, an empty privilege list opening no administrator.

- **The panel changes a user's privileges** (ADR 0347). **For operators:**
  a user's page, linked from the Users screen, shows who they are under
  `auth:read`, and an operator holding `admin` ticks the privileges they
  hold; privileges another operator changed meanwhile are refused. **For
  integrators:** `auth.admin` changes a user's privileges from the set
  read, refusing `auth_user_scopes_revised`.

- **The last administrator keeps admin** (ADR 0346, D209). **For
  integrators:** `PUT /admin/v1/users/{id}` with scopes that leave out
  `admin`, and `DELETE /admin/v1/users/{id}`, answer 409
  `auth_last_administrator` when the user is the last live one holding
  admin. **For operators:** give admin to another user before taking it
  from the last one; a shop can no longer lock itself out of its own
  administration.

- **The panel lists the users** (ADR 0345). **For operators:** a Users
  screen lists who operates the shop under `auth:read`, each with their
  privileges and whether they have proven a second factor, those without
  one on a tab of their own. **For integrators:** `auth.admin` lists a page
  of the users.

- **The panel shows an invoice and moves its status** (ADR 0344). **For
  operators:** an invoice's page, linked from its order and from the
  Invoices screen, shows the parties, rows and totals under `invoice:read`
  and records that it was sent, accepted, rejected or canceled under
  `invoice:write`; an invoice moved meanwhile is refused. **For
  integrators:** `MoveInput.From` names the status the document was read
  in, refused with `invoice_status_moved` when it has left it; the admin
  API's move is unchanged.

- **The panel lists the invoices** (ADR 0343). **For operators:** an
  Invoices screen lists the shop's documents under `invoice:read`, the
  latest first, every status or one, each with its number, buyer, total,
  status and why. **For integrators:** `invoice.admin` lists a page of the
  documents in a status.

- **The panel corrects a customer's address** (ADR 0342). **For
  operators:** each address on a customer's page corrects its name,
  company, lines, city, postal code, country and phone under
  `customer:write`; an address another operator corrected meanwhile is
  refused. **For integrators:** `customer.admin` corrects an address from
  the one read, refusing `customer_address_revised`.

- **The order page writes off a line's units** (ADR 0341). **For
  operators:** each line of a pending order writes off some of its units
  under `order:write`, with a reason and a note, and their stock comes
  back; the same form sent twice writes off once. **For integrators:** a
  line cancellation may name the units spoken for when the line was read,
  refused with `order_line_moved` when that moved.

- **The order page completes and archives an order** (ADR 0340). **For
  operators:** a pending order's page marks it completed and a completed
  one's archives it under `order:write`. **For integrators:**
  `order.admin` completes and archives an order as the API does.

- **The order page cancels an unpaid order** (ADR 0339). **For operators:**
  a pending order's page cancels it under `order:write`, with a reason, and
  its units' stock comes back; an order with money collected is refused.
  **For integrators:** `order.admin` cancels an order as the API's cancel
  does.

- **The panel changes how much a discount gives** (ADR 0338). **For
  operators:** a promotion's page changes its discount's value under
  `promotion:write`, a percentage typed as a percent and a fixed amount in
  its currency's decimals; a discount changed meanwhile is refused. **For
  integrators:** `promotion.admin` changes a discount's value from the type
  and the value read, refusing `promotion_discount_revised`.

- **The panel corrects a customer's name and phone** (ADR 0337). **For
  operators:** a customer's page corrects their first name, last name and
  phone under `customer:write`; a customer another operator corrected
  meanwhile is refused. **For integrators:** `customer.admin` corrects a
  customer's contact from the one read, refusing `customer_contact_revised`.

- **The panel writes the store profile** (ADR 0336). **For operators:** a
  Store profile screen shows who the shop is under `settings:read` and
  writes it under `settings:write`, so a new shop issues its first invoice
  without the API; a profile another operator wrote meanwhile is refused.
  **For integrators:** the settings module registers `settings.admin`.

- **The order page issues the order's invoice** (ADR 0335). **For
  operators:** an order's page names its invoice and issues one under
  `order:write`, on a series the shop numbers on (offered with
  `invoice:read`) or a new one named apart, the buyer defaulting to the
  order's billing address. **For integrators:** `order.admin` names and
  issues an order's invoice, and the invoice module registers
  `invoice.admin`, which lists the series.

- **The panel writes a shipping option** (ADR 0334, D208). **For
  operators:** the Shipping options screen writes an option on a registered
  provider and one of the newest profiles, in a region and its currency or
  in every region, under `fulfillment:write`. **For integrators:**
  `fulfillment.admin` lists its providers and profiles and writes an option.

- **The panel lists and revises the shipping options** (ADR 0333). **For
  operators:** a Shipping options screen lists the options with their
  provider, profile, region and fee under `fulfillment:read`, and each row
  renames its option, sets a flat option's fee and keeps it off the
  storefront under `fulfillment:write`; an option another operator revised
  meanwhile is refused. **For integrators:** `fulfillment.admin` revises an
  option from the terms read, refusing `fulfillment_shipping_option_revised`.

- **The panel opens a parcel on the delivery chosen** (ADR 0332, D207). **For
  operators:** an order sold several deliveries asks on its page which one a
  parcel goes on; before, the panel could open no parcel on such an order.
  **For integrators:** `order.admin` lists an order's deliveries, and its
  `OpenParcel` takes the delivery to open on.

- **The panel revises a campaign and its budget limit** (ADR 0331). **For
  operators:** each row of the Campaigns screen revises its campaign's name,
  description, window and budget limit under `promotion:write`, so an
  exhausted budget is raised where it is read; a campaign another operator
  revised meanwhile is refused rather than written over. **For
  integrators:** `promotion.admin` revises a campaign from the terms read,
  refusing `promotion_campaign_revised`.

- **The panel revises a price list's title and window** (ADR 0330). **For
  operators:** each row of the Price lists screen revises its list's title,
  description and window under `pricing:write`; a list another operator
  revised meanwhile is refused rather than written over. **For
  integrators:** `pricing.admin` revises a list from the terms read, refusing
  `pricing_price_list_moved`.

- **The panel renames and re-ranks a customer group** (ADR 0329). **For
  operators:** each row of the Customer groups screen renames and re-ranks its
  group under `customer:write`; a group another operator revised meanwhile is
  refused rather than written over. **For integrators:** `customer.admin`
  revises a group from the name and rank read, refusing
  `customer_group_moved`.

- **The panel publishes and ends a price list** (ADR 0328). **For
  operators:** a price list's row publishes a draft, ends an active list or
  reopens an ended one under `pricing:write`; a list another operator moved
  meanwhile is refused rather than moved back.

- **The panel prices a variant on a list** (ADR 0327). **For operators:** a
  variant's page lists its prices on price lists with the customers each is
  for, and adds a price on a list for every customer or for some customer
  groups, and removes one, under `pricing:write`. **For integrators:**
  `pricing.admin` reads, adds and removes a set's list prices, and pricing
  names the `customer_group_id` attribute, pinned against the cart's.

- **The panel writes and lists the price lists** (ADR 0326). **For
  operators:** a Price lists screen lists the lists with their type, status
  and window, and writes one under `pricing:write`. **For integrators:** the
  pricing module's price list service, repository and input checks answer in
  English.

- **The panel attaches evidence to a claim** (ADR 0325). **For operators:**
  a claim on the order's page lists its evidence, and a photograph sent from
  the page is stored and bound to the claim under `order:write` and
  `file:write`. **For integrators:** the file module registers a
  `file.admin` surface, and `order.admin` reads, binds and removes a claim's
  evidence.

- **The panel ships an order** (ADR 0324). **For operators:** an order's page
  opens a parcel on the delivery the order was sold under `order:write`, and
  marks a parcel shipped with its tracking, delivered, back undelivered or
  canceled under `fulfillment:write`. **For integrators:** the fulfillment
  module registers a `fulfillment.admin` surface, and `order.admin` opens a
  parcel.

- **The panel writes and lists the customer groups** (ADR 0323). **For
  operators:** a Customer groups screen lists the groups newest first with
  their rank and writes one under `customer:write`. **For integrators:** the
  customer module's input checks answer in English.

- **The panel puts a customer into groups** (ADR 0322). **For operators:** a
  customer's page names their groups, and an operator holding
  `customer:write` puts the customer into a group or takes them out. **For
  integrators:** the customer module registers a `customer.admin` surface.

- **The panel limits a promotion to customer groups** (ADR 0321). **For
  operators:** a promotion's page offers the customer groups to an operator
  who may read the customers and writes a `customer_group_id any_in` rule
  over the chosen. **For integrators:** the customer module opens a
  `customer_group` read entity (id, name, rank), and the customer module's
  registration file is in English.

- **The panel puts a promotion into a campaign** (ADR 0320). **For
  operators:** a promotion's page offers the live campaigns and puts the
  promotion into one, or out of any, under `promotion:write`; a promotion
  another operator moved meanwhile is refused rather than moved back.

- **The panel writes and lists the campaigns** (ADR 0319). **For operators:** a
  Campaigns screen lists each campaign's window and how much of its budget is
  used, and writes one under `promotion:write`. **For integrators:** the
  campaign service's and repository's messages are in English, and a taken
  campaign identifier is refused as "a campaign with the identifier … exists"
  rather than with the constraint's name.

- **The order page lists the order's notifications** (ADR 0318). **For
  operators:** with `notification:read`, an order's page lists what was sent
  for it with the provider's reason, and links to the Notifications screen on
  the order, where a failed confirmation is sent again.

- **The panel lists the notifications** (ADR 0317). **For operators:** a
  Notifications screen lists the failed deliveries first with the provider's
  reason, finds an order's by its id, and sends a failed order confirmation
  again under `notification:write`.

- **The panel shows a product's history** (ADR 0316). **For operators:** the
  product page links to its revisions, newest first with what each changed,
  and an older one is restored under `product:write`; a product written since
  the page was read is refused rather than overwritten.

- **A promotion is limited to categories in the panel** (ADR 0315). **For
  operators:** a promotion's page limits a discount on items to chosen
  categories and their subcategories, names the categories of its rules, and
  removes a rule, under `promotion:write`; choosing categories needs
  `product:read` as well.

- **The panel writes a coupon** (ADR 0314). **For operators:** the Promotions
  screen writes a draft coupon worth a percentage or an amount off, with an
  optional usage limit, under `promotion:write`, and opens its page; the
  coupon goes on sale when it is published.

- **The panel shows a promotion** (ADR 0313). **For operators:** each row of
  the Promotions screen opens a page with the discount, the rules, the
  campaign and the latest twenty uses, under `promotion:read`. **For
  integrators:** the promotion module's migration 000005 replaces the index on
  `promotion_redemption (promotion_id)` with one on `(promotion_id, id)`.

- **An operator switches a promotion's status in the panel** (ADR 0312).
  **For operators:** the Promotions screen publishes a draft, pauses an active
  promotion and resumes an inactive one under `promotion:write`; a promotion
  another operator moved first is refused with the status it is in now.

- **The panel lists the promotions** (ADR 0311). **For operators:** a
  Promotions screen lists the active, draft and inactive promotions with how
  often each was used against its limit, under `promotion:read`.

- **A variant begins to keep its stock in the panel** (ADR 0310). **For
  operators:** a variant without an inventory item offers a button that has
  one made and linked, after which the stock form counts it at each location;
  it needs `product:write` and `inventory:write`.

- **A variant takes a price in the panel** (ADR 0309). **For operators:** the
  variant page adds a base price in a currency the variant has none in,
  creating and linking its price set when it has none; it needs
  `product:write` and `pricing:write`.

- **The customer page lists the customer's addresses** (ADR 0308). **For
  operators:** the panel's customer page lists the customer's addresses and
  marks the defaults. **For integrators:** the `customer` query provider
  offers the `addresses` field.

- **The panel creates a product and its variants** (ADR 0307). **For
  operators:** the product list links to a form that creates a draft product,
  and the product page adds a variant with a title and an optional SKU; a new
  variant's prices and stock are still set up over the admin API.

- **A telephone order chooses its offline method from a list** (ADR 0306).
  **For operators:** the telephone order's completion offers the shop's
  offline methods as a list to an operator holding `payment:read`; without it
  the method is typed as before.

- **A telephone order chooses its channel by name** (ADR 0305). **For
  operators:** the telephone order's line and completion forms offer the
  enabled sales channels by name to an operator holding `auth:read`; without
  it the channel's id is typed as before.

- **A customer's cart starts from their default address** (ADR 0304). **For
  operators:** a telephone cart opened for a customer draws its address forms
  with the customer's default shipping address until one is saved. **For
  integrators:** the `customer` query provider offers the
  `default_shipping_address` field.

- **A telephone order takes a billing address** (ADR 0303). **For
  operators:** the telephone order's cart page writes the billing address,
  drawn with the shipping address until one is written, and both address
  forms ask for the company. **For integrators:** the `cart` query provider
  offers the `billing_address` field.

- **The customer list finds a customer by e-mail** (ADR 0302). **For
  operators:** the panel's customer list takes an e-mail, in any case, and
  lists the account and the guest records holding it.

- **An abandoned cart is deleted after the shop's period** (ADR 0301). **For
  operators:** `CART_RETENTION_DAYS` names how many days an open cart is kept
  after its last change; the hourly `cart-retention` job then deletes it for
  good with its lines, addresses, shipping methods and coupon codes. Zero, the
  default, keeps every cart; a completed cart is never deleted.

- **An operator corrects their own cart** (ADR 0300). **For operators:** the
  telephone order's cart page removes a line and discards the cart, which
  then leaves the open carts. **For integrators:**
  `DELETE /admin/v1/carts/{id}/line-items/{line_item_id}` and
  `DELETE /admin/v1/carts/{id}` on a cart an operator opened; a shopper's
  answers 409 `cart_opened_by_shopper`.

- **An order names the operator who placed it** (ADR 0298). **For
  operators:** the panel's order list has a box for the orders an operator
  placed, and an order's page says which operator placed it. **For
  integrators:** an order completed through `POST /admin/v1/carts/{id}/complete`
  stores the caller's id; `GET /admin/v1/orders/{id}` returns it as
  `placed_by`, `GET /admin/v1/orders` and the `order` query provider take
  `placed_by_operator=true|false`, and the provider offers the `placed_by`
  field. The storefront's order does not carry it, and an operator's cart a
  shopper completes names no operator.

- **A telephone order finds the caller by e-mail** (ADR 0297). **For
  operators:** the telephone order's page finds the customer records holding
  the e-mail a caller gives, in any case, and the form that opens the cart
  offers them by name, the account first and chosen; it needs `customer:read`
  beside the cart's write, and without it the customer's id is typed as before.

- **A cart names the operator who opened it** (ADR 0296). **For operators:**
  the telephone order's page lists the open carts operators opened, the twenty
  newest, each with its caller, its opener and its total, to an operator who
  holds `cart:read`. **For integrators:** a cart opened through
  `POST /admin/v1/carts` stores the caller's id; `GET /admin/v1/carts` and the
  `cart` query provider take `opened_by_operator=true|false`, and the provider
  offers the `opened_by` field. The cart's JSON does not carry it.

- **An operator may choose an admin-only shipping option** (ADR 0295). **For
  operators:** the telephone order's shipping list includes the options marked
  admin-only, such as a pick-up at the desk. **For integrators:**
  `GET /admin/v1/carts/{id}/shipping-options` lists them and
  `POST /admin/v1/carts/{id}/shipping-methods` accepts them; the storefront's
  listing and write still do neither.

- **The order list filters the orders awaiting their payment** (ADR 0294).
  **For operators:** the panel's order list has a box for the orders still
  awaiting their payment — not canceled, and collected below their total less
  their credits, partly paid ones included. **For integrators:**
  `GET /admin/v1/orders?awaiting_payment=true|false` and the `order` query
  provider's `awaiting_payment` filter; refunds are not added back, so an order
  paid and later refunded is not listed.

- **A telephone order finds a variant by its title** (ADR 0293). **For
  operators:** the telephone order's cart page searches the catalog by product
  title and the add form offers the found variants by name; it needs
  `product:read` beside the cart's write, and without it the variant's id is
  typed as before.

- **A cart lists the shipping options it can take** (ADR 0292). **For
  integrators:** `GET /store/v1/carts/{id}/shipping-options` lists the options
  the cart can take, each priced for the cart's own subtotal, item count and
  weight, so an option with a rule such as "free over 500" is listed when the
  cart meets it — the fulfillment module's eligibility endpoint, which takes
  those facts from the client, leaves such options out. Every option listed is
  one the shipping method write accepts. `GET /admin/v1/carts/{id}/shipping-options`
  is the same list under `cart:read`. **For operators:** the telephone order's
  shipping form is a list of those options with their prices.

- **The operator's completion compares a total of zero** (D199). **For
  integrators:** `POST /admin/v1/carts/{id}/complete` with `expected_total: 0`
  on a cart that does not total zero is refused with 409
  `checkout_workflow_total_mismatch`; zero used to skip the comparison there as
  it does on the storefront. The storefront's completion is unchanged.

- **The panel completes a telephone order** (ADR 0291). **For operators:** the
  cart's page writes the caller's shipping address, chooses the shipping option
  and places the order with an offline method against the total the page
  shows, then opens the order; a total that moved, or a method that would be
  captured, is refused on the page. **For integrators:** the `cart` query
  provider offers `shipping_address` and `shipping_methods`.

- **The panel opens a telephone order** (ADR 0290, D198). **For operators:**
  the panel's telephone order section opens a cart for a country and a caller
  and adds lines the catalog prices in the sales channel the operator names;
  the cart's page shows its lines and totals. The address, the shipping method
  and the completion are still on the admin API. **For integrators:** the
  `cart` query provider offers `lines` and takes an `id` filter.

- **An unpaid offline order expires by its method's wait** (ADR 0289). **For
  operators:** `PAYMENT_OFFLINE_WAIT_DAYS=bank_transfer:3` gives an offline
  method a wait, and the `offline-order-expiry` job cancels, every quarter hour,
  the order whose session of that method is still authorized past it in a
  payment that captured nothing: its stock comes back and its session is
  closed. A method left out never expires, which is every method by default;
  leave cash on delivery out, since it is paid after the parcel has left. A
  wait for a method not offered, or outside 1 to 365 days, stops the startup.

- **A canceled order holds no payment** (ADR 0288, D197). **For operators:** the
  shop's cancel of an order now closes every session of its payment still
  authorized — an offline method's promise, or a card's hold left by a checkout
  that died before capturing — so a canceled order no longer offers to record a
  transfer. **For integrators:** both cancels, the shop's and the checkout's
  compensation, publish `order.canceled` (`order_id`, `canceled_at`), and the
  outbound webhook plugin forwards it.

- **The panel records an offline payment** (ADR 0287). **For operators:** the
  order page names each offline method's session still awaiting its money,
  with the method and the amount, and an operator holding `payment:write`
  records it as received with one button: the session is captured whole and
  the order's paid total rises. A card's session is refused. **For
  integrators:** the `payment_collection` query provider offers `awaiting`,
  a list of `session_id`, `provider_id` and `amount`.

- **An operator completes a telephone order** (ADR 0286). **For operators:** the
  cart's admin surface now writes the shipping and billing addresses
  (`PUT /admin/v1/carts/{id}/shipping-address`, `…/billing-address`), adds and
  removes the shipping method (`POST`/`DELETE …/shipping-methods`) and completes
  the cart (`POST /admin/v1/carts/{id}/complete` with `sales_channel_id`,
  `payment_provider_id` and `expected_total`), all under `cart:write`. The
  completion takes only an offline method (ADR 0284) and answers 422
  `checkout_workflow_offline_method_required` for any other; the order is
  placed owing its total.

- **The shop's cancel gives the stock back** (ADR 0285, D195). **For
  operators:** `POST /admin/v1/orders/{id}/cancel` now writes off every unit of
  the order not yet returned or written off, as line cancellations under the
  cancel's reason, and the units come back to the shelf; before, the units the
  checkout had deducted stayed off it. An order that owes an offline method's
  money and is never paid is the ordinary case.

- **An offline method is paid when the shop says so** (ADR 0284). **For
  operators:** `PAYMENT_OFFLINE_METHODS` names the offline methods a shopper may
  choose — `bank_transfer,cash_on_delivery` — each a payment provider of its
  own. The checkout authorizes it and does not capture it: the order is placed
  owing that part, and `POST /admin/v1/payment-sessions/{id}/capture` records
  the money when it arrives, raising the order's paid total. A gift card or a
  balance beside it is still captured at the checkout. Reconciliation leaves
  these sessions out. **For integrators:** the completion's response carries
  `outstanding`, what the order still owes.

- **The panel lists a category with its subcategories** (ADR 0282). **For
  operators:** the catalog filter has a "with its subcategories" box beside the
  category. **For integrators:** the `product` query provider takes
  `category_tree_id`.

- **Raising a line asks the channel again** (ADR 0281). **For integrators:**
  `PATCH /store/v1/carts/{id}/line-items/{line_item_id}` raising the quantity
  of a line whose product is no longer in the key's sales channels answers 404,
  as adding it would; lowering it, removing it and completing the cart are
  unchanged.

- **A price and a count name what they were drawn with** (ADR 0280). **For
  operators:** saving a variant's price or a location's physical count in the
  panel over a value that changed since the page was drawn — another
  operator's save, a sale taking a unit — is refused and the page shows the
  value as it is now, instead of writing the old figure back.

- **Two price edits to one set no longer lose one of them** (D193). **For
  operators:** saving a variant's price in one currency while another currency
  of the same variant is saved, from the panel or a catalog import, keeps both
  changes; before, one of the two was silently written back.

- **The panel opens a return and a replacement with their detail**
  (ADR 0279). **For operators:** the order page's return form takes each
  line's part of the refund beside its quantity, and its replacement form
  takes a variant the order never sold with its units, sent as an item of its
  own.

- **Every module with a schema answers for what it keeps** (ADR 0278, D192).
  **For operators:** `GET /admin/v1/personal-data` lists the file module's
  upload names and addresses, the fulfillment module's parcel data, metadata,
  replay keys and tracking numbers and links, the notification module's
  failed-send errors, and the shop profile's country; a disclosure reports
  those three modules as unable to attribute their rows, and an erasure as
  keeping them.

- **A failed notification keeps no address in its log** (D191). **For
  operators:** the error of a failed delivery reads as the provider wrote it
  with the recipient and any e-mail address or phone number replaced by
  `<address>`.

- **The payment module answers for what it keeps about a customer**
  (ADR 0277). **For operators:** `GET /admin/v1/personal-data` lists the
  payment module's holdings, a disclosure carries a customer's payment
  collections with their sessions and refunds, and their store credit and
  loyalty entries and sessions, and an erasure reports the payment module as
  retained with what it kept and why. A request with an e-mail address and no
  customer id is answered as unresolvable there, since a guest's payment names
  nobody.

- **The invoice declaration names the address an erasure matches** (D189).
  **For operators:** `invoices.buyer_email_folded` is declared, and an
  invoice's refusal of erasure lists it among what it kept.

- **An order's later records are in its personal-data answers** (D188).
  **For operators:** the order module declares a replacement's note, a
  credit's reason and note, a claim photograph's caption and a line
  cancellation's reason and note as open text; a data subject's disclosure
  now carries them under their order, and an erasure report lists them among
  what it kept.

- **A session names the browser that opened it** (ADR 0276). **For
  operators:** `GET /admin/v1/auth/sessions` lists each session's
  `user_agent`, and the panel's Sessions screen shows it as the Browser
  column, so the session a lost laptop holds can be told from the others. A
  session opened before the upgrade shows none until it expires. Migration
  000006 of the auth module adds the column.

- **A webhook receiver sets its rate** (ADR 0275). **For operators:**
  `POST /admin/v1/webhooks` and `PATCH /admin/v1/webhooks/{id}` take
  `max_per_minute` (1 to 10000; zero on a change lifts it), the listing shows
  it, and each delivery pass sends a receiver at most that many of its due
  deliveries, oldest first; the rest wait for the next pass with no attempt
  counted. Migration 000003 of `plugins/webhookout` adds the column.

- **A store credit names the order it compensates** (ADR 0274). **For
  operators:** `POST /admin/v1/store-credits` takes an optional `order_id`,
  every history row carries it, and `GET /admin/v1/store-credits` takes
  `order_id` to list the credits issued for one order. Migration 000015 of the
  payment module adds the column.

- **The bus keeps the message it gives up on** (ADR 0273). **For operators:**
  on the Redis bus, a message that emptied every consumer that took it is kept
  in the stream `<prefix>-dead-letters` instead of being dropped with a log
  line; the `outbox-relay` job fails while that pile is not empty, and
  `gobit deadletters` lists it after the outbox's pile and redrives or discards
  a letter named by its stream id (`-confirm` repeats it). **For integrators:**
  `core/eventbus` publishes `RedisConfig.DeadLetterStream`,
  `ReadRedisDeadLetters`, `RedriveRedisDeadLetter`, `DiscardRedisDeadLetter`,
  `RedisDeadLetter`, `RedisDeadLetterReport` and `CodeDeadLetterFailed`.

- **The panel opens an order's after-sales records, and an operator's return
  names its lines** (ADR 0272, D186). **For operators:** with `order:write` the
  order page opens a return with a quantity per line, a claim, an exchange and
  a replacement. A return opened through `POST /admin/v1/orders/{id}/returns`
  used to name no goods and restocked nothing when received; it now takes
  `lines`. **For integrators:** the admin return body takes
  `lines: [{order_line_item_id, quantity, refund_amount}]`.

- **The panel acts on an order's after-sales records** (ADR 0271). **For
  operators:** with `order:write`, the order page receives a return at a
  location, refunds it, settles a refund claim, funds or refunds an exchange,
  dispatches a replacement and withdraws any of them, each under the API's own
  conditions. **For integrators:** the order module registers `order.admin`.

- **An order's after-sales records are read like its lines** (ADR 0270).
  **For operators:** the panel's order page lists the order's returns, claims,
  exchanges and replacements, newest first, with their status, money, lines
  and moments. **For integrators:** the read layer answers `order_return`,
  `order_claim`, `order_exchange` and `order_replacement`, each with the
  `order_id` filter required.

- **A balance pays part of an order** (ADR 0269). **For integrators:** the
  storefront completion takes `pay_first_with`, naming `store_credit`,
  `loyalty_points` or both: after the gift card, each holds what the
  customer's balance has of what is still unpaid, and `payment_provider_id`
  pays the rest or is not asked. A balance paying alone still declines a
  shortfall. A store credit or points session holds part of its amount when
  its payment data says `"partial": true`. **For operators:** the checkout's
  execution record names every hold that paid first in `first_holds` and the
  other captures in `other_payment_ids`, where it named a gift card's in
  `gift_card_session_id` and `gift_card_payment_id`. Migration 000014 of the
  payment module adds `partial` to the store credit and points session tables.

- **The panel shows a person's sessions** (ADR 0268). **For operators:** the
  panel's Sessions screen, open to everybody who can sign in, lists your open
  sessions with the current one marked and closes one or every other.

- **An admin session can be closed alone** (ADR 0267). **For operators:**
  `GET /admin/v1/auth/sessions` lists your open sessions with the current one
  marked; `POST /admin/v1/auth/sessions/{id}/revoke` closes one and
  `POST /admin/v1/auth/sessions/revoke-others` closes all but the one you are
  using. **For integrators:** a session token now carries `jti`, the session
  it names, and `corehttp.Principal` carries `SessionID`; a token signed before
  this change names none and is accepted until it expires. Migration 000005 of
  the auth module adds `auth_session`.

- **The panel enrolls a second factor** (ADR 0266). **For operators:** the
  panel's Second factor screen, open to everybody who can sign in, enrolls an
  authenticator (the key is shown once, with an `otpauth://` link), proves it,
  and replaces or removes it with its current code; an account no privilege
  opens a screen for lands there. **For integrators:** the auth module
  registers `auth.admin`, the panel's surface for the person's own factor.

- **An installation can require a second factor** (ADR 0265). **For
  operators:** `ADMIN_SECOND_FACTOR_REQUIRED_FROM` takes an RFC 3339 moment;
  from it, an administrator who has not proven an authenticator signs in but
  holds no privilege until they enrol through `POST /admin/v1/auth/mfa`, and
  `GET /admin/v1/users?second_factor=false` lists who still owes one. **For
  integrators:** every user record carries `second_factor`, and
  `GET /admin/v1/auth/me` carries `second_factor_owed`.

- **The OpenAPI document names each operation's privilege** (ADR 0263). **For
  integrators:** every admin operation's security requirement lists the scopes
  its route demands, for example `bearerAuth: ["product:read"]`, and the `mcp`
  verb's tools end their description with them. `core/http` publishes
  `ScopeDemandedBy`, which reads the privilege off a `RequireScope` guard.

- **The local integration lane no longer fails on a stopping reaper**
  (ADR 0262). **For contributors:** every Makefile recipe that starts
  containers sets `TESTCONTAINERS_RYUK_RECONNECTION_TIMEOUT=5m`, so the next
  package finds the shared reaper running; a lane's leftover containers are
  removed five minutes after it ends.

- **A storefront category lists its subcategories** (ADR 0261). **For
  integrators:** the storefront product listing and its facets take
  `category_tree_id`, and GraphQL's `products` and `productFacets` take
  `categoryTreeId`: a category's products and those of every category below
  it, each once. `category_id` still lists what is filed directly.

- **A panel page shows another module's data only under that module's
  privilege** (ADR 0260, D179). **For operators:** the product and variant pages
  show a variant's prices only to an operator holding `pricing:read` and its
  stock only to one holding `inventory:read`, and name the missing privilege
  otherwise; an operator who held `product:read` alone saw both before and needs
  the two grants to see them again. A refused price or stock form draws the
  variant page only for an operator holding `product:read`.

- **A category promotion can reach the subcategories** (ADR 0259). **For
  operators:** a target rule on `category_tree_ids` with `any_in` matches a
  product filed under any of the named categories or their subcategories; a
  rule on `category_ids` still matches direct membership only. **For
  integrators:** the product read-layer record publishes `category_tree_ids`.

- **Store credit can expire** (ADR 0258). **For operators:**
  `POST /admin/v1/store-credits` takes `expires_at`. From that moment the
  balance no longer counts what the credit still holds, and the
  `store-credit-expiry` job (every 15 minutes) writes an `expire` row that takes
  it back. Spending draws on the soonest-expiring credit first, money a session
  holds is not taken, and a refund into credit never expires. The payment
  journal books an expiry as `store_credit_expire`. Migration: payment `000013`.

- **A secret is compared in constant time, and a gate says so** (ADR 0257).
  **For contributors:** in a file that imports `crypto/hmac`, `crypto/subtle`,
  a SHA package, `crypto/rand` or argon2, comparing two values with `==` or
  `bytes.Equal` fails `internal/arch` unless the comparison is listed as
  holding no secret; compare a secret with `hmac.Equal` or
  `subtle.ConstantTimeCompare`.

- **An order's address correction is trimmed as the cart's address is** (D178).
  **For operators:** `PUT /admin/v1/orders/{id}/shipping-address` trims each
  field and refuses one over 512 bytes, so a change of whitespace alone no
  longer writes a correction; a cancellation's reason is trimmed too.

- **An empty text on an update clears the field** (ADR 0256, D177). **For
  integrators:** on `PATCH /admin/v1/products/{id}`, `PATCH /admin/v1/variants/{id}`
  and `PATCH /admin/v1/product-categories/{id}`, an empty `subtitle`,
  `description`, `thumbnail`, `material`, `origin_country`, `sku`, `barcode`,
  `ean` or `upc` now clears the field. It used to be ignored, or written as an
  empty string, which made a second variant's emptied SKU a duplicate. A client
  that sent an empty string to mean "no change" has to leave the field out.
  Every other value is trimmed.

- **A panel route is its method and its path** (ADR 0255). **For contributors:**
  the panel's scope table names each route by method and path and lists the
  open ones with no privilege; binding a route it does not list stops the panel
  from being built, so a POST added to a read path no longer inherits the read
  privilege.

- **A program built on gobit calls itself by its own name** (ADR 0254). **For
  embedders:** `gobit.New().Name("shop")` makes the usage text and the command
  lines the operator subcommands print say `shop migrate status` rather than
  `gobit migrate status`; left empty it is `gobit`. A project `gobit new`
  writes names itself after the last element of its module path.

- **A customer reads their own store credit and points** (ADR 0253). **For
  integrators:** `GET /store/v1/customers/{id}/store-credit/balance` and
  `GET /store/v1/customers/{id}/loyalty-points/balance` answer the balance for
  a `currency_code` when the request proves the customer in the path; with no
  customer identity bound they refuse with 401, and naming somebody else is
  refused with 403.

- **An order line says what became of it** (ADR 0252). **For operators:** the
  order page prints each line's units asked back and written off, and what each
  parcel holds. **For integrators:** the `order_line_item` read-layer entity
  offers `asked_back_quantity` and `canceled_quantity`, and the `fulfillment`
  entity offers `items`; a read asking for every field now carries them.

- **An order's page in the panel shows its payment and its parcels** (ADR 0251).
  **For operators:** the order page prints the payment collection's status,
  amounts and when its money moved, and each parcel's status, tracking and
  moments. An operator without `payment:read` or `fulfillment:read` is told which
  privilege shows them; the page reads neither without it.

- **A parcel's items, a price set's prices and a price's rules come back in the
  order they were written** (D175). **For integrators:**
  `GET /admin/v1/fulfillments/{id}`, the fulfillment list, the price set and
  price reads on both surfaces and the price history listed them in an order
  set by their ids' random tails. Migrations: fulfillment `000005`, pricing
  `000005`.

- **An order's page in the panel lists what was sold** (ADR 0250, D174). **For
  operators:** the order page prints the order's lines with their quantities and
  amounts, the shopper's words and a gift card mark, in the order they were
  written, each add-on under its line. **For integrators:** the
  `order_line_item` read-layer entity lists one order's lines in the order they
  were written; it listed them in the order of their ids' random tails.

- **The published API document was partly Turkish.** **For integrators:** the
  summaries and descriptions `/openapi.json` carries for the b2b, customer,
  payment, promotion, region and tax routes are now English — 276 strings,
  which include the tool descriptions a model client reads (ADR 0161). No path,
  operation, parameter name or schema changed.

- **Every sale a shopper can build is now checked as a property** (ADR 0249,
  D172). **For contributors:** property-based tests draw their inputs with
  `pgregory.net/rapid` on every ordinary run; a failure prints its draws,
  shrunk, and `-rapid.failfile` replays the file it writes under
  `testdata/rapid/`. The first property found the cart's and the order's
  multiplication accepting a zero price times a negative quantity, which no
  caller reached. **For embedders:** `pgregory.net/rapid` moves from v1.2.0,
  already in the module graph, to v1.3.0.

- **An invoice filed a row that did not multiply, and could not say its prices
  included their tax** (D171, ADR 0248). **For operators:** a tax-inclusive
  order's invoice says `prices_include_tax`, and its rows keep the sticker as
  the unit price and the net as the subtotal. **For integrators:** `POST
  /admin/v1/invoices` takes `prices_include_tax` and now refuses a row whose
  subtotal is not its unit price times its quantity (less its tax where the
  flag is set), which it filed before. Migration: invoice `000005`.

- **A gift card was taxed when it was sold, and the goods it bought were taxed
  again** (D170, ADR 0247). **For operators:** a gift card line carries no tax,
  so a card costs its value; the goods it pays for are taxed as before. A
  checkout whose cart taxed a card, which happens only when the catalog could
  not be read for the totals, answers `checkout_workflow_gift_card_taxed` and
  the next attempt computes them again. Orders placed before keep their taxed
  card lines.

- **Nothing could be sold in a market whose prices include their tax** (D169,
  ADR 0246). The cart refused to write the totals of every such cart with a
  non-zero rate, since each line's subtotal is the sticker less its tax and
  three checks held it to unit price times quantity. **For operators:** a tax
  region with `prices_include_tax` now sells, and the shopper pays the sticker.
  **For integrators:** the cart and the order answer `prices_include_tax`;
  where it is true, a line's `unit_price` is the sticker and its `subtotal` is
  what is left of `unit_price` times `quantity` once its `tax_total` is taken
  out. The promotion trial and a new delivery's quote read the goods as unit
  price times quantity. Migrations: cart `000007`, order `000038`.

- **A notification an attempt left pending could never be sent again**
  (D168, ADR 0245). **For operators:** the resend endpoint also takes an order
  confirmation left pending for longer than thirty seconds, which is an attempt
  that died before it could write its outcome; a younger one answers 409, since
  it may still be sending.

- **The defect ledger counted the files that cite it, and the count was five
  times stale** (D167). **For contributors:** `docs/gaps.md` no longer states a
  number nothing recomputes.

- **A failed order confirmation could not be sent again** (D166, ADR 0243).
  The module said resending was an operator's decision and gave the operator no
  way to make it. **For operators:** `POST
  /admin/v1/notifications/{id}/resend` (scope `notification:write`) sends a
  failed order confirmation again, rebuilt from the order; a sent one, or
  another template, answers 409 `notification_not_resendable`. The log line of
  a skipped repeat now says the notification was attempted before rather than
  that it was sent.

- **A price or an invoice written after a wait was dated before the write it
  waited for** (D165, ADR 0242). **For operators:** of two price replacements
  made at once, the price history's latest entry is now the price the set
  holds, so the storefront's reduction compares against the right one; the
  same holds for a price list's status; and an invoice number is never dated
  before the number before it. An invoice issued across midnight on the 31st
  is numbered and dated in the new year.

- **A write that waited for a lock was stamped before the write it waited
  for** (D164, ADR 0241). **For operators:** of two delivery changes made at
  once, the one written last is now the order's delivery, which the parcel
  opens with; two address corrections made at once no longer answer the second
  with a 500; an exchange completed right after its funding reads as completed
  in the order's history; and the stock ledger's newest movement again carries
  what the level counts. Order migration 000037 changes two column defaults.

- **A handler's error was dropped while four handlers said it was retried**
  (D163, ADR 0240). A fault during a write-off's put-back, a payment summary's
  update, an order confirmation or a search index write was logged and lost.
  **For operators:** such a fault is now tried twice more within a second and a
  quarter before it is logged.

- **The returns flow's error lines went nowhere** (D162). Built with no logger,
  it fell back to a discard handler, so "the refund was made only in part; a
  human has to finish it" and its other error lines were never written in a
  running installation. **For operators:** they now reach the application's log,
  under `workflow=returns`.

- **Rows an order writes together came back in an order nobody wrote**
  (D161). A return's lines, a replacement's lines, a line's write-off with its
  add-ons' and an order's deliveries are each written in one transaction and
  were read by their creation time and then their id, whose tail is random; a
  replacement's dispatch sets its lines aside in that order, so which line a
  dispatch refused half way had already held was chance. **For operators:**
  these list as they were written, the ring's write-off before its add-ons' and
  the deliveries as the cart held them, and a cart's personal-data dossier lists
  its lines the same way. Order migration 000035 gives the four tables `seq`,
  as ADR 0233 gave an order's lines.

- **The ADR index said "current" for five amended or superseded records**
  (D160). 0022 had been superseded by 0121, and 0057, 0085, 0174 and 0234 were
  amended or partly superseded, each saying so in its own header. **For
  contributors:** a gate now requires every record a header names on an
  `Amended by` or `Superseded by` line in that record's index status.

- **A withdrawn replacement kept the units it had set aside** (D159, ADR 0237).
  A dispatch refused on a later line left the earlier lines' units reserved,
  and withdrawing the replacement released none of them. **For operators:**
  `POST .../replacements/{replacementId}/cancel` now gives those units back
  before it withdraws the record, and answers `409
  returns_workflow_stock_not_released` when the units already left for a
  parcel (dispatch again to finish it). Without the returns flow bound the
  endpoint fails closed, as the dispatch does.

- **The complexity table's copies had kept numbers ADR 0219 moved** (D158).
  The `DefaultMaxComplexity` godoc and `docs/api-surfaces.md` printed the
  calibration from before the attributes. **For contributors:** a copy of the
  table is held to the pinned one by
  `TestTheComplexityTableCopiesMatchThePinnedOne`; a field added to the schema
  and the calibration moves the copies in the same change.

- **An order's lines were read in an order nobody wrote** (D157). Lines written
  together — an order's lines, a cart line and its add-ons, a merge — share a
  moment, and the tie was broken by the random end of their ids, so the order
  read, the invoice and the cart listed them shuffled. **For API clients:** an
  order's and a cart's `items` now come back in the order they were written
  (ADR 0233).

- **The channel audit never saw the related products' route** (D156). It read
  a route's path only from a literal, and the route was registered on a
  concatenation. **For contributors:** a route path in a module or a plugin is a
  whole string literal or a constant of one; anything else fails
  `TestEveryRoutePathIsReadByTheAudit` in `internal/arch`.

- **The facet counts' document described a body the read never writes**
  (D155). `GET /store/v1/sales-channels/{sales_channel_id}/product-facets` was
  described as a page with `count`, `offset` and `limit` required while it
  answers only `data`, and its channel segment and `403` were not described.
  **For API clients:** a client generated from the document gets a list
  envelope of `data` alone.

- **A cart could grow past its line ceiling** (D154). The ceiling of 100 lines
  asked whether the added variant was already in the cart, so the same variant
  with new `properties` (ADR 0223) opened a line past it, and a merge opened
  lines with no ceiling at all; a cart past pricing's 1,000 lines could never be
  priced again. **For storefront clients:** adding a new line to a full cart is
  refused with `422` and `cart_workflow_line_limit_reached` whatever the line's
  properties, and a merge that would pass the ceiling is refused with the same
  code and moves nothing (ADR 0227).

- **Three GraphQL types escaped the schema's field gates** (D153). The gates
  read a list of types written by hand, and `ProductList`, `ProductAttribute`
  and `AttributeOption` were never on it. **For contributors:** every object
  type of the storefront schema must now be named in `bindings()` in
  `internal/modules/product/graph/schema_test.go`, with the Go fields it leaves
  out and why.

- **A cart line's note never reached the order** (D152). The storefront's
  add-line `metadata`, described as a gift note or a personalization, was
  dropped by the checkout: its order snapshot had no field for it. Each line's
  metadata now reaches the order line.

- **The product service's fake store dropped most of an edit** (D151). It
  applied five of the product patch's sixteen fields, so a service test of an
  edit to the material, the weight, the collection or the type passed against a
  write that never happened. It applies all sixteen now.

- **A rollback test passed on the wrong refusal** (D148). A migration's error
  carries the down file's text, which names the refusal it drops. **For
  contributors:** rollback tests assert the constraint as the server quotes it,
  `check constraint "…"` or `unique index "…"`.

- **A job built and never registered passed the registration gate** (D147).
  **For contributors:** `TestEveryJobIsRegisteredInTheCompositionRoot` now
  counts a job only when its `Definition` is handed to the registry's `Add`.

- **A flow could be dropped from production with every test green** (D146).
  The end-to-end ground wires its flows by hand. **For contributors:** an arch
  gate now fails when the ground wires a flow `internal/app` does not.

- **Two payment ledger gates counted the ledgers by hand** (D145). A new ledger
  left both green without being read. **For contributors:** a payment ledger's
  insert query and its table are now required to be named in
  `internal/arch/loyalty_ledger_test.go`, and the gate fails until they are.

- **Pricing's godoc named a caller that did not exist** (D144). It said the
  product module creates a price set for every new variant. **For plugin
  authors:** creating a variant creates no price set; `CreateEmptyPriceSet` is
  called by a catalog import pricing a variant that has none (ADR 0207).

- **The panel's price form overwrote quantity tiers** (D143, ADR 0206). Saving
  a variant's price in a currency set every base price in it to the amount, a
  price for ten or more included, and the page offered the same form for a tier
  and for an open list's price, whose save changed the base price instead.
  **For operators:** the variant page edits the price at one unit only and
  lists the others with the quantities and list they apply to; a set with two
  prices at one unit in a currency is edited through the admin API, and the
  panel's write for it answers 409 `pricing_unit_price_ambiguous`.

- **Eleven migration tests rolled back the database their package shares**
  (D141, ADR 0201). Three of them asserted afterwards that a module table held
  no rows: every other test's rows in it had just been dropped with the schema.
  **For contributors:** a test that rolls a migration back takes its database
  from `internal/testdb`, and an architecture gate refuses a `MigrateDown`
  against a package's shared address.

- **An exchange could be funded with anybody's money** (D142). The funding
  accepted any payment collection holding the difference, including one opened
  for another order or the checkout's own. **For API consumers:** the
  collection named in `POST /admin/v1/orders/{id}/exchanges/{exchangeId}/funding`
  must now be opened with the order's id as its `reference`, and one that paid
  for a delivery change is refused with `order_payment_collection_taken`.

- **The order module's migration test rewound other tests' rows** (D141). It
  dropped the shared schema believing it ran first, and seven files ran before
  it. It runs in a database of its own now.

- **An order's addresses were kept and read by nothing** (D140). Since B11 the
  order stores the shipping and billing addresses its cart carried, so that it
  could say where it went and an invoice could print a buyer; no API field
  returned them and the invoicing flow never read them. Closed for both by
  ADR 0193, and for a carrier's label by ADR 0194.

- **Two analytics tests raced the event bus** (D139). They read the funnel
  as soon as a cart was completed, while the plugin's row was still being
  written on another goroutine, and failed on CI once. They now wait for their
  own events.

- **The lanes ran whatever Go the machine had** (D138). An allocation budget
  failed locally and passed on CI for the same commit, because the machine had
  moved to go1.27.1 and CI runs go1.26.6. **For contributors:** every make lane
  now runs the release go.mod names, and a budget run under another release
  refuses and says which.

- **Two personal data audits read only their module's first migration** (D137).
  The customer and cart modules check their declaration against the schema, and
  both read `000001` alone: a personal column added by a later migration passed,
  `customer_group.rank` was never looked at, and the cart's whole
  `cart_promotion_code` table was invisible. Both now read every up migration,
  including `ALTER TABLE … ADD COLUMN`.

- **`errors.HasKind(nil, errors.KindInternal)` was true** (D136). `KindOf`
  reads an unclassified error as internal and read nil the same way, so a test
  asserting an internal failure passed when nothing failed; the b2b service's
  constructor test passed with both of its refusals removed. **For embedders:**
  `HasKind(nil, kind)` is now false for every kind; `KindOf(nil)` is unchanged.

- **The payment module's migration test passed by file order** (D135). It
  rolled the schema back in the database every test shares, and 000006 refuses
  to roll back a point ledger holding a spend row; it ran before every test that
  spends points only because of its file name. It runs in a database of its own
  now, as pricing's does.

- **The GraphQL cost ceiling was calibrated two fields light** (D134). The
  "every field" document had not selected the product's `typeId` since ADR 0101
  nor the image's `altText` since ADR 0104; a sentence asked for it and nothing checked.
  A gate now compares the document with the generated schema.

- **ADR 0182 said `go run` stamps no version** (D133). Only `go run .` in a
  checkout does; `go run` of a module at a version stamps that version. The
  refusal of `gobit new` now names `go run` inside a checkout.

### Decisions

- **A workflow step has no observer** (ADR 0385). **For contributors:** the
  saga engine takes no hook, observer or step callback, and an architecture
  test pins the engine's imports and every place it accepts code it does not
  own. A step's timing and attempts are in `workflow_execution_steps`, read by
  SQL; a saga that wants a span opens it in its own steps. A second saga on the
  engine reopens the decision, and a test fails the day it lands.

- **A count is read in English** (ADR 0380, D228). **For contributors:** the
  count gate reads the number a document states for a population in digits and
  English words alone. A count written in Turkish is read by no gate, and the
  language gate refuses Turkish prose before this one would read it.

- **A bundle variant is replaced from its catalog parts** (ADR 0244). **For
  operators:** an exchange's replacement that names a bundle variant, a gift box
  the order never sold, is recorded with the parts the catalog gives the box at
  that moment and sent from them, where it used to be refused for having no
  inventory item. A failed catalog read records nothing and answers with
  `order_catalog_read_failed`. Without the query layer the old refusal stands.

- **A failing handler is called again** (ADR 0240). **For plugin authors:** an
  event handler that returns an error is called at most twice more, after a
  quarter of a second and then a second, in both bus backends, before the error
  is logged and the event counted as processed; an error of
  `errors.KindInvalid` and a panic are not repeated. Return an error for a fault
  that may pass and nil for one that never will, and keep the handler
  idempotent: the second call may follow a first that did part of the work. On
  the Redis backend the stream's next message waits for the retries.

- **A canceled parcel recalls its replacement** (ADR 0239). **For operators:**
  canceling the parcel a replacement left in (`POST
  /admin/v1/fulfillments/{id}/cancel`) puts the units it took off the shelf back
  where they left from, sends the replacement back to `requested` and reopens
  the claim, or the exchange, which goes back to `funded` when its difference
  was collected; a source another replacement settled stays settled.
  Dispatching the replacement again opens a new parcel. The replacement read
  carries `recalls`, how many of its parcels were canceled. Order migration
  000036 adds `order_replacements.recalls`. **For contributors:** the inventory
  interop gains `RecallReplacement(reservationID)`, the order interop
  `ReplacementOfParcel` and `RecallReplacement`, and the returns flow subscribes
  to `fulfillment.canceled`.

- **A bundle is replaced from its parts** (ADR 0238). **For operators:** a
  replacement of an order line that sold a bundle is dispatched from the parts
  the line sold, whatever the bundle is made of by then: each part is set aside
  from its own stock for the boxes times its units, and withdrawing the
  replacement gives every part back. The replacement read carries `parts`
  (`[{"variant_id": ..., "quantity": 2, "reservation_id": ...}]`) on such an
  item, and the dispatch's `sent_units` counts the parts' units. An item that
  names a bundle variant instead of a line is still refused. Order migration
  000034 adds `order_replacement_item_parts`. **For contributors:** the order
  interop's `RecordReplacementReservation` takes the variant whose units the
  promise holds.

- **The panel edits a variant's bundle** (ADR 0236). **For operators:** a
  variant's page in the admin panel lists what a bundle is made of, and
  `/admin/ui/products/{id}/variants/{variantID}/bundle` replaces its parts, one
  per line as a SKU or an id followed by how many one bundle holds (one when
  the line says nothing). The form needs the product write privilege and
  carries the product's version: a save made after somebody else saved the
  product comes back unsaved.

- **A bundle sells from its parts** (ADR 0235). **For storefront clients:** a
  bundle variant's `in_stock` (and GraphQL `inStock`) is true when every
  component can supply its units for one bundle, and a bundle can be ordered.
  An order line that sold a bundle carries `components`
  (`[{"variant_id": ..., "quantity": 2}]`, one bundle's parts) on the admin and
  storefront order reads. **For operators:** checkout reserves each component
  for the line's quantity times its units; writing off a bundle line,
  canceling its parcel and receiving it back put each component back in the
  same multiple, as the line was sold. Order migration 000033 adds
  `order_line_items.components`. **For contributors:** the variant record
  publishes `bundle_components`, and the checkout's execution record names a
  component's reservation by `variant_id` and a component it left unreserved
  under `unreserved_components`.

- **A variant names what it is made of** (ADR 0234). **For operators:**
  `PUT /admin/v1/variants/{id}/bundle` with
  `{"components": [{"variant_id": ..., "quantity": 2}]}` makes a variant a
  bundle of up to 20 variants of other products; the bundle has to be counted,
  not sold past zero and linked to no inventory item, and a component cannot be
  deleted while a bundle holds it (`409`). **For storefront clients:** a
  variant carries `bundle_components`, and GraphQL `bundleComponents`. A bundle
  reads out of stock and checkout refuses it until its stock is read from its
  parts.

- **An order keeps the order of its lines** (ADR 0233). **For contributors:**
  `order_line_items` and `cart_line_items` carry `seq`, an identity the database
  fills; a read of a cart's or an order's lines orders by `created_at, seq`.

- **The panel edits a product's add-ons** (ADR 0232). **For operators:** the
  product page lists the add-ons its lines take, and "Edit add-ons" replaces
  the list, one variant per line by its SKU or its id; a SKU no variant carries
  is refused by name and nothing is saved. The form needs the product write
  privilege.

- **The GraphQL storefront reads a product's add-ons** (ADR 0231). **For
  storefront clients:** `Product.addOns { variantId product { ... } }` answers
  what `GET /store/v1/sales-channels/{sales_channel_id}/products/{id}/add-ons`
  answers; each product selecting it costs as much as a root query against the
  complexity ceiling, as `related` does.

- **An add-on goes back with its line** (ADR 0230). **For storefront clients
  and operators:** a return request naming a line that carries add-ons has to
  name each add-on at the same quantity, and an add-on only beside its line;
  otherwise it is refused with `422` and `order_add_on_follows_its_line`. The
  refund of each line stays the operator's, nothing included. A write-off
  (`POST /admin/v1/orders/{id}/line-cancellations`) of a line writes off its
  add-ons too, each with its own record and `order.line_canceled` event, and an
  add-on cannot be written off alone.

- **An add-on is a line of its own** (ADR 0229). **For storefront clients:**
  `POST /store/v1/carts/{id}/line-items` (and the admin add) takes
  `"add_ons": [{"variant_id": ..., "properties": {...}}]`, each a variant the
  line's product accepts (ADR 0228), refused with
  `cart_workflow_add_on_not_accepted` otherwise. Each opens a line with
  `parent_line_id`, priced by its own price set; the add-ons are part of the
  line, follow its quantity and are removed with it, and writing or removing an
  add-on alone is refused with `422` and `cart_line_is_an_add_on`. An order line
  carries `parent_line_item_id`. **For order consumers:** the order line entity
  has a `parent_line_item_id` field.

- **A product names the add-ons its lines take** (ADR 0228). **For operators
  and storefronts:** `PUT /admin/v1/products/{id}/add-ons` with
  `{"variant_ids": [...]}` sets up to 20 variants of other products — an
  engraving, a gift wrap — that the product's cart lines may carry;
  `GET /store/v1/sales-channels/{sales_channel_id}/products/{id}/add-ons`
  answers each visible one's `variant_id` and `product`. A cart line does not
  carry an add-on yet; the decision binding one to its line follows.

- **The cart counts the lines it opens** (ADR 0227). **For contributors:** the
  line ceiling is `MaxLineItems` in `internal/modules/cart/service`, asked in
  `openLine` under the cart's lock by both paths that open a line; the cart
  workflow's `MaxLineItems`, `CodeCartLineLimit` and snapshot check are gone,
  and a test refuses a line created anywhere but `openLine`.

- **The GraphQL storefront counts what it lists** (ADR 0226). **For storefront
  clients:** the schema answers `productFacets`, which takes the `products`
  filters `q`, `collectionId`, `categoryId`, `tagId`, `optionValue`,
  `variantIds` and `attributes` and answers per attribute what
  `GET /store/v1/sales-channels/{sales_channel_id}/product-facets` answers, and
  `optionValues(limit:, offset:)`, the page of
  `/store/v1/sales-channels/{sales_channel_id}/option-values`. Both read the
  channels the publishable key carries, as `products` does. Every attribute a
  facet count filters on costs as much as a root query against the complexity
  ceiling.

- **The GraphQL storefront reads the vocabulary** (ADR 0225). **For storefront
  clients:** the schema answers `collections`, `categories(parentId:)`, `tags`
  and `productAttributes`, the same reads as `GET /store/v1/collections`,
  `/categories`, `/tags` and `/product-attributes`, so the ids and handles the
  `products` filters take can be read without leaving GraphQL. The pages go by
  offset with `count`, `offset` and `limit`; each query costs its page size
  times its selection against the complexity ceiling, and the attributes the
  catalog's ceiling of 100.

- **A plugin names the releases it works with** (ADR 0224). **For plugin
  authors:** a plugin may implement `core/plugin.CoreRequirement`, returning a
  range such as `">=v0.9.0 <v0.11.0"`; `Registry.Install` refuses it with
  `plugin_core_unsupported` when the binary was built with a release outside
  the range, and with `plugin_core_range_invalid` when the range cannot be read,
  before any plugin is set up. An unstamped build (`go run`, `go test`, a
  `replace`) installs it and logs that the range was not checked. **For Go
  callers:** the new published package `core/version` reads the library's
  release from the build (`Library`, `Of`, `Module`); `core/plugin` now imports
  `golang.org/x/mod`, which a module requiring gobit already has in its graph.

- **A cart line carries what the shopper wrote** (ADR 0223). **For API
  consumers:** the storefront and admin add-line bodies take `properties`, up to
  ten names with texts (a name up to 64 characters, a text up to 500), refused
  with 422 `cart_line_properties_invalid` otherwise; the same variant with other
  properties is another line, and the same properties raise the line already
  there. Cart lines and order lines answer `properties`, and the
  `order_line_item` read entity offers them. **For operators:** cart migration
  000004 re-keys the one-line-per-variant index and refuses to roll back while a
  cart holds one variant on two lines; order migration 000030 adds the column.

- **A product write names the version it read** (ADR 0222). **For API
  consumers:** `GET`, `POST` and every write that revises a product (its
  PATCH, restore, variants, options, option values, images and attribute
  values) answer the product's version as `ETag`; such a write sent with
  `If-Match: "7"` is refused with 412 `product_version_mismatch` when the
  product has moved on. No header, or `*`, asks nothing. A malformed
  `If-Match` is refused with 422. **For Go callers:** `core/errors` gains
  `KindPreconditionFailed`, `PreconditionFailed` and `IsPreconditionFailed`,
  answered as 412, and `core/http` gains `HeaderOnSuccess`. **For panel users:** the edit form saves at the version it
  was opened at and says so when somebody saved first.

- **A product keeps its revisions** (ADR 0221). **For API consumers:** an admin
  product carries `version`; `GET /admin/v1/products/{id}/revisions` lists its
  revisions newest first with `changed` and `request_id`, `GET
  .../revisions/{version}` returns one with its `snapshot`, and `POST
  .../revisions/{version}/restore` writes back its own fields, collection, type,
  tags, categories and attribute values, answering `product` and `dropped`. A
  write to one product's content now waits for another to the same product.
  **For operators:** product migration 000012 adds `product.version` and the
  `product_revision` table; a product written before it gets its first revision
  at its first write since.

- **A price list can be tried on past orders** (ADR 0220). **For API
  consumers:** `GET /admin/v1/price-lists/{id}/trial?from=&to=` prices the
  goods of every uncanceled order placed in the period with today's ladder,
  without the list and with it as if active with no window, whatever its
  status; it answers per currency `baseline`, `trial` and `charged`, the
  hundred orders the list changes most, and what it assumed. Both ends are
  RFC 3339 and in the past, the period at most 93 days and 5,000 orders; it
  needs `order:read` beside `pricing:read`. Nothing is written.

- **A product carries typed attributes** (ADR 0219). **For API consumers:**
  `POST/GET /admin/v1/product-attributes`, `PATCH/DELETE
  /admin/v1/product-attributes/{id}`, `POST .../{id}/options` and `DELETE
  /admin/v1/product-attribute-options/{id}` define store-wide `number`,
  `boolean` and `select` attributes; `PUT /admin/v1/products/{id}/attributes`
  replaces a product's values. Products carry `attributes`. The storefront
  listing takes a repeated `attribute` parameter (`material:cotton,wool`,
  `width:10..120`, `waterproof:true`), GraphQL `products` takes `attributes`
  and `Product` has `attributes`, `GET /store/v1/product-attributes` is the
  vocabulary, and `GET /store/v1/sales-channels/{id}/product-facets` counts the
  products per value. **For operators:** product migration 000011 adds three
  tables; at most 100 attributes and 200 options per select.

- **A webhook receiver can narrow what it gets** (ADR 0218). **For API
  consumers:** a receiver registered at `POST /admin/v1/webhooks/` takes
  `filters` (per topic, payload fields and the values one of which an event
  must carry) and `fields` (per topic, the payload fields it is sent); the
  listing returns both and `topic_fields`, the names they may use. `PATCH
  /admin/v1/webhooks/{id}` changes topics, filters, fields or description and
  keeps the URL and the secret. A request body may be 64 KB. **For operators:**
  webhookout migration 000002 adds the two columns; rolling it back keeps the
  receivers and sends them every event of their topics whole. **For plugin
  authors:** a publisher that adds a payload field fails webhookout's census
  until `TopicFields` lists it.

- **A customer group can be a segment** (ADR 0217). **For API consumers:**
  `PUT /admin/v1/customer-groups/{id}/segment` gives a group a rule of ANDed
  conditions over `has_account`, `account_age_days`, `country_code` (the default
  shipping address), `order_count` and `net_spend` (with `currency_code` and
  `window_days`); `DELETE` on the same path takes it away and keeps the members;
  `POST /admin/v1/customer-segments/preview` counts the customers a rule would
  take in. Groups carry `segment` and `segment_evaluated_at`. While a group is a
  segment, adding or removing a member by hand is refused with 409
  `customer_group_segment_managed`; at most 50 groups are segments. **For
  operators:** a new hourly `customer-segments` job writes every segment's
  members; customer migration 000006 adds the rule, and rolling it back forgets
  the rules and keeps their members. **For plugin authors:** `order.interop`
  answers `CustomerOrderTotalsJSON`, each customer's order count and net spend
  per currency.

- **A wishlist item can ask for its price** (ADR 0216). **For API consumers:**
  `PUT /store/v1/customers/{id}/wishlist/{variant_id}/price-alert` (proven
  customer) with `{"region_id": "..."}` saves the variant if needed and marks
  its price in that region with the request's sales channels; `DELETE` on the
  same path takes the mark off. Wishlist items carry `price_alert`, and a marked
  item `price_alert_region_id` and, once recorded, `price_alert_currency_code`
  and `price_alert_amount`. **For operators:** the `stock-alert` job records the
  unit price the customer's cart would be charged at the first pass after the
  mark and mails the customer once when it falls below that, clearing the mark;
  its line counts the prices recorded. Customer migration 000005 adds the mark,
  and rolling it back forgets the price marks. **For plugin authors:** a
  notification provider has to know `wishlist.price_drop` (the stock mail's data
  plus `currency_code`, `previous_amount`, `amount`), and the cart workflow
  answers `QuoteUnitPrices`. The mark is declared personal data.

- **A wishlist item can ask for its stock** (ADR 0215). **For API consumers:**
  `PUT /store/v1/customers/{id}/wishlist/{variant_id}/stock-alert` (proven
  customer) saves the variant if needed and marks it with the request's sales
  channels; `DELETE` on the same path takes the mark off. Wishlist items carry
  `stock_alert`. **For operators:** a new `stock-alert` job mails the customer
  once when the storefront shows a marked variant back in stock after having
  shown it out, and clears the mark; customer migration 000004 adds the mark,
  and rolling it back forgets the marks. **For plugin authors:** a notification
  provider has to know `wishlist.back_in_stock` (data `variant_id`,
  `variant_title`, `product_id`, `product_title`, `product_handle`), and
  `product.interop` answers `VariantsInStock`. The mark is declared personal
  data.

- **A gift card can expire** (ADR 0214). **For operators:**
  `PAYMENT_GIFT_CARD_VALIDITY_DAYS` (default 0, never; at most 36500) gives
  every sold card, and every issued card whose operator names no moment, that
  many days; a new `gift-card-expiry` job closes a card whose moment has come,
  voiding what it held as ADR 0213's close does. Payment migration 000012 adds
  the moment; rolling it back stops while a card has one. **For API
  consumers:** `POST /admin/v1/gift-cards` takes an optional `expires_at`, which
  has to be ahead, and gift card responses carry `expires_at`. A card past its
  moment is answered 409 `payment_gift_card_expired` at checkout and refuses a
  refund. **For plugin authors:** the `gift_card.issued` notification carries
  `expires_at`, empty for a card that never expires.

- **An operator closes a gift card, and a card line is final** (ADR 0213). **For
  API consumers:** a return request or a line write-off naming a line that sold
  gift cards is answered 409 `order_gift_card_line_final`. `POST
  /admin/v1/gift-cards/{id}/disable` (payment:write, body `{"reason": ...}`)
  closes a card: what it held is voided, the answer carries `disabled_at` and
  `disable_reason`, and a card a payment still holds is answered 409
  `payment_gift_card_held`. A closed card's code is answered 409
  `payment_gift_card_disabled` at checkout, a refund onto it is refused with the
  same code, and it gets no new code. The payment journal books a closed card's
  balance as `gift_card_void` (issued cards, back to `gift_card_granted`) or
  `gift_card_forfeit` (sold cards, to a new `gift_card_forfeited` account).
  **For operators:** payment migration 000011 adds the close; rolling it back
  stops while a closed card exists.

- **A sweep issues the gift cards a lost delivery did not** (ADR 0212). **For
  operators:** a new job, `gift-card-sweep`, runs every five minutes and issues
  the cards that the paid orders of the last week sold and no capture delivery
  issued, mailing their codes as the capture does; its line in `gobit jobs`
  counts the cards issued and the orders still waiting for their capture. Order
  migration 000029 adds a partial index on the gift card lines. A late capture
  or a sweep no longer issues cards for a canceled order. **For plugin
  authors:** the read layer's `order_line_item` accepts an `is_giftcard` filter.

- **An order books a sold gift card as a debt** (ADR 0211). **For API
  consumers:** order lines carry `is_giftcard`, copied from the product when
  the order is placed, on both order views. `GET /admin/v1/order-journal`
  credits an order's gift card lines to a new `gift_card` account rather than
  `sales`, the account the payment journal debits when a card is spent. A
  checkout whose products cannot be read is refused as one whose variants
  cannot. **For plugin authors:** the read layer's `order_line_item` publishes
  `is_giftcard`, and the gift card sale flow issues cards from it rather than
  from the catalog. **For operators:** order migration 000028 adds the column;
  lines written before it read `false`.

- **A sold gift card is issued when its order is paid** (ADR 0210). **For API
  consumers:** buying a product flagged `is_giftcard` issues, once the order's
  collection is fully captured, one gift card per unit worth the line's unit
  price, and mails its code to the order's address with the notification
  template `gift_card.issued` (data: `code`, `amount`, `currency_code`,
  `order_id`). `POST /admin/v1/gift-cards/{id}/code` (payment:write) replaces a
  card's code and answers the new one once; the balance stays. Gift card
  responses carry `source` (`issued` or `sold`) and `code_changed_at`. Money
  captured through a gift card no longer earns loyalty points. **For plugin
  authors:** a notification provider has to handle `gift_card.issued` to
  deliver sold cards' codes; nothing else holds them. **For operators:**
  payment migration 000010 adds the card's source and sale; rolling it back
  stops while a sold card exists.

- **A gift card pays first and a provider the rest** (ADR 0209). **For API
  consumers:** `POST /store/v1/carts/{id}/complete` takes an optional
  `gift_card_code`: the card holds what it has, up to the total, and
  `payment_provider_id` pays the rest, which is not asked for when the card
  covers the order. A code that cannot pay is refused before the order opens,
  and `payment_provider_id` cannot be `gift_card` beside a code. A gift card now
  holds what it has instead of declining: alone, a card short of the total is
  answered 409 `checkout_workflow_payment_underauthorized` and its hold is
  released. A refund of a split order goes back onto the card first.

- **A gift card is a code with a balance** (ADR 0208). **For API consumers:**
  `POST /admin/v1/gift-cards` (payment:write) issues a card holding an amount in
  one currency, with a required reason, and answers with its code ONCE; `GET
  /admin/v1/gift-cards`, `/admin/v1/gift-cards/{id}` and `/{id}/entries`
  (payment:read) read the cards, their balances and their history, never the
  code. A storefront pays with a card by completing a cart with
  `payment_provider_id: "gift_card"` and `payment_data: {"code": "..."}`; a guest
  may. A code that opens no card is refused with 422 `payment_gift_card_unknown`
  and a card in another currency with 409 `payment_gift_card_currency`, both
  before an order is opened. A card has to cover the whole order for now. The
  payment journal gains the `gift_card` and `gift_card_granted` accounts and the
  `gift_card_issue` kind. **For operators:** payment migration 000009 adds three
  tables, and rolling it back stops while a card exists. **For plugin
  authors:** the `gift_card` provider is registered in every installation.

- **An import writes its prices through pricing** (ADR 0207). **For API
  consumers:** a `variant_price_<currency>` cell in a file sent to `POST
  /admin/v1/products/imports` now sets the variant's base price at one unit in
  that currency, in minor units, and gives a variant without a price set one;
  a quantity tier and a list price keep their amounts, and an empty cell leaves
  the currency as it is. A file with price columns requires `pricing:write` as
  well as `product:write` (403 without it), and is refused with 422
  `product_import_prices_unavailable` in an installation without the pricing
  module. A cell that is not a whole number, or a price on a row naming no
  variant, refuses that row. **For plugin authors:** pricing's service gains
  `SetUnitBasePrices`, which never deletes a price; `SetBasePrices` still
  replaces the set.

- **A catalog import is a record a job works through** (ADR 0205). **For API
  consumers:** `POST /admin/v1/products/imports` takes a CSV file (text/csv,
  up to 32 MiB) in any subset of the export's columns and answers 202 with the
  import; `GET /admin/v1/products/imports/{id}` reports its status, its rows
  done, created, updated and failed, and the first thousand refused rows by
  line. A row finds its product by `product_id` or `product_handle` and its
  variant by `variant_id`, `variant_sku` or `variant_options`, writes its
  non-empty cells that differ, and creates what it does not find; price
  columns are read and not applied yet. A file over 1 MiB is sent without
  `Idempotency-Key`, which refuses a larger body with 422 `body_too_large`.
  **For operators:** product migration 000010 adds `product_import`, and the
  `product-import` job runs every minute.

- **The catalog leaves as CSV** (ADR 0204). **For API consumers:** `GET
  /admin/v1/products/export` (optionally `?status=`) streams the catalog as
  `text/csv`: a row per variant, a row for a product with none, tag and
  category ids joined with `|`, metadata and options as JSON, a formula-like
  cell prefixed with `'`, and a `variant_price_<currency>` column per currency
  a region sells in holding the base price at one unit in minor units. It
  requires both `product:read` and `pricing:read`. A failure after the first
  row drops the connection.

- **An exchange's difference is on the books** (ADR 0203). **For API
  consumers:** `GET /admin/v1/order-journal` gains two kinds: a funded exchange
  is an `exchange_funded` entry at its funding that debits `receivable` and
  credits `sales`, and a refund naming an exchange is an `exchange_refunded`
  entry the other way. Over the two journals an exchanged order now closes.
  **For operators:** order migration 000027 adds an index.

- **The conformance kit checks a shipping provider** (ADR 0202). **For
  embedders:** `core/providertest.Fulfillment(t, p, in)` opens a shipment
  through the provider with a destination made of markers, repeats it and
  cancels it twice, and reports a provider that returns the destination in its
  data, opens a second shipment for a repeated idempotency key, or fails a
  second cancel. It needs the provider to reach a stub of its carrier or a
  sandbox. **For contributors:** an in-tree provider has to run the suite of
  its own contract, not the identity check alone.

- **A dearer delivery is paid before it changes** (ADR 0200). **For API
  consumers:** `PUT /admin/v1/orders/{id}/shipping-methods/{shippingMethodId}`
  takes `payment_collection_id`: a collection opened with the order's id as its
  reference, in the order's currency, for exactly the difference and captured.
  Without one a dearer option is still refused with
  `order_delivery_costs_more`, whose `details` now carry the option, its
  amount, the difference and the currency. A delivery change carries its
  `payment_collection_id`, and the order journal books a paid change as a
  `delivery_upgraded` entry that debits `receivable` and credits `shipping`.
  **For operators:** order migration 000026; its rollback refuses a database
  holding a paid change. **For embedders:** the payment interop gains
  `CollectionReference`, and the fulfilling flow resolves `payment.interop`.

- **A delivery can be changed before it ships** (ADR 0199). **For API
  consumers:** `PUT /admin/v1/orders/{id}/shipping-methods/{shippingMethodId}`
  with `{"shipping_option_id": …}` puts the delivery on another option at the
  fulfillment module's price for the order, while no parcel of the order is
  pending, shipped or delivered, and answers with the admin order record. A
  cheaper option writes the difference off as a credit line with the reason
  `delivery_change`; a dearer one is refused with `order_delivery_costs_more`.
  Each shipping method in both order reads now carries its `id` and its
  `changes`; the timeline gains `order.delivery_changed`, and the order journal
  books a cheaper delivery as a `delivery_changed` entry that debits
  `shipping`. **For operators:** order migration 000025 adds
  `order_delivery_changes`. **For embedders:** the order interop's
  `ShippingOptionOf` answers the delivery's current option, and gains
  `DeliveryFactsJSON` and `ChangeDeliveryJSON`.

- **An order remembers the delivery it was sold** (ADR 0198). **For API
  consumers:** both order reads carry `shipping_methods` (`shipping_option_id`,
  `name`, `amount`), the cart's methods as the checkout priced them, adding up
  to `shipping_total`; empty for an order placed before. `POST
  /admin/v1/orders/{id}/fulfillments` may leave out `shipping_option_id` when
  the order was sold exactly one method. **For operators:** order migration
  000024 adds `order_shipping_methods`. **For embedders:** the cart's snapshot
  carries each method's `shipping_option_id` and `name`, and the order interop
  gains `ShippingOptionOf`.

- **An addition travels in its parent's parcel** (ADR 0197). **For API
  consumers:** `PUT /admin/v1/orders/{id}/fulfillments/{fulfillmentId}` binds an
  addition to a pending parcel of the order it adds to and answers with its
  shipments; `409 order_ships_alone`, `order_not_pending`,
  `order_addition_parent_not_pending`, `order_ships_elsewhere`,
  `fulfilling_parcel_not_parents` or `fulfilling_parcel_not_waiting` refuse it.
  Nothing is sent to the carrier. **For operators:** the `order_fulfillment`
  link is widened from one-to-many to many-to-many at startup; the previous
  release then refuses to start against the same database (ADR 0116). **For
  embedders:** the order interop gains `ShippingParentOf`, the fulfilling
  interop `ShipInParcel`, and a canceled parcel's units go back against the
  bound order whose line they are.

- **The panel shows where an order goes** (ADR 0196). **For operators:** the
  panel's order page shows the shipping and billing addresses, when the
  shipping address was last corrected, the order an addition adds to and the
  additions of an order. **For embedders:** the order read-layer entity offers
  `adds_to_order_id` (also a filter), `shipping_address`, `billing_address` and
  `shipping_address_corrected_at`; the three address fields are read in one
  batch per page, only when asked for.

- **A shipping address can be corrected before it ships** (ADR 0195). **For API
  consumers:** `PUT /admin/v1/orders/{id}/shipping-address` (`order:write`)
  takes the whole corrected address and answers with the admin order record.
  It is refused with `409 fulfilling_parcel_underway` while a parcel is
  pending, shipped or delivered, `order_address_not_correctable` for an order
  that is not pending or was erased, `order_address_missing` for one with no
  shipping address, and `order_address_country_changed`. The address the order
  was placed with is kept; the timeline, storefront included, gets an
  `order.shipping_address_corrected` entry that carries no address. **For
  embedders:** the order interop gains `CorrectShippingAddressJSON` and the
  fulfilling interop `CorrectShippingAddress`. **For operators:** order
  migration 000023 adds `order_addresses.superseded_at` and makes the one-per-type
  index partial; its rollback refuses a database that holds a correction.

- **A carrier is told where a parcel goes** (ADR 0194). **For plugin
  authors:** `core/provider.CreateFulfillmentInput` carries `Destination
  *provider.Address`, the shipping address of the parcel's order, for every
  parcel opened through an order (the admin order endpoint and a claim's or
  exchange's replacement); nil when the order has none. A provider hands it to
  the carrier and must not return it in `Fulfillment.Data`, which the parcel
  stores and no erasure empties. `Data` no longer claims to carry the address.
  **For embedders:** the order interop gains `ShippingAddressJSON`, and the
  fulfillment interop's `CreateFulfillment` takes the destination as a fifth
  argument.

- **An order says where it went** (ADR 0193). **For API consumers:**
  `GET /admin/v1/orders/{id}`, and the cancel, complete and archive answers,
  carry `shipping_address` and `billing_address`; the storefront's
  `GET /store/v1/orders/{id}` does not. `POST /admin/v1/orders/{id}/invoice`
  fills an empty buyer `name`, `address` and `country_code` from the order's
  billing address, each on its own: the company's name when the address names
  one, the person's otherwise, and the address in lines. A field the body sends
  is kept, and an order with no billing address fills none of them. **For
  embedders:** the order interop's `OrderInvoiceJSON` carries `billing_address`.

- **An order can add to another** (ADR 0192). **For API consumers:**
  `POST /store/v1/carts` and `POST /admin/v1/carts` take `adds_to_order_id`;
  the cart carries it, and its checkout places an ordinary order whose
  `adds_to_order_id` names that order, with its own lines, total, payment and
  invoice. The named order must be pending, of the cart's customer and currency,
  and not an addition itself: `409 order_addition_needs_customer`,
  `order_addition_customer_mismatch`, `order_addition_currency_mismatch`,
  `order_addition_parent_not_pending` or `order_addition_parent_is_addition`,
  when the cart is opened and again at checkout, before any payment.
  `GET /admin/v1/orders?adds_to_order_id=` lists an order's additions. Two carts
  that do not add to the same order do not merge (`409 cart_addition_mismatch`).
  **For embedders:** the order interop gains `CheckAddition`, which the cart
  workflows resolve from `order.interop` when it is registered; without it a
  cart naming an order is not opened. **For operators:** order migration 000022
  adds `orders.adds_to_order_id` and cart migration 000003 adds
  `carts.adds_to_order_id`.

- **The catalog reads a list of variants** (ADR 0191). **For API consumers:**
  the storefront product listing takes a repeated `variant_id` query parameter
  and GraphQL's `products` a `variantIds` argument, up to 100 ids, and returns
  the products that own them, once each and whole, under the listing's other
  rules. It is how a wishlist is shown in one read.

- **A customer keeps a wishlist** (ADR 0190). **For API consumers:**
  `GET /store/v1/customers/{id}/wishlist`, and `PUT` / `DELETE
  /store/v1/customers/{id}/wishlist/{variant_id}`, both repeatable, reached by
  the proven customer alone; up to 200 variants, and a new one past that is
  `409 customer_wishlist_full`. The operator reads it at
  `GET /admin/v1/customers/{id}/wishlist` (`customer:read`). **For controllers:**
  the saved variant ids are declared personal data; a disclosure lists them and
  an erasure deletes them. **For operators:** customer migration 000003 adds
  `customer_wishlist_item`.

- **A refund's cause gives back revenue** (ADR 0189). The order journal books a
  refund that names one of the order's returns as `sales_returns`, and one that
  names a claim as `claim_allowances`, each against `receivable` at the refund's
  amount and moment, so the payment and order journals together close a returned
  order. A refund naming an exchange or nothing is not an entry. **For
  embedders:** the payment interop gains `CausedRefundsJSON`, and the order
  module resolves it from `payment.interop` when it is registered.

- **The order module keeps derived books** (ADR 0188). **For operators and
  accountants:** `GET /admin/v1/order-journal?from=&to=[&currency_code=]`
  (`order:read`) returns each order placed, order canceled and credit line in the
  window as a balanced entry over `receivable`, `sales`, `sales_discounts`,
  `tax_payable`, `shipping` and `credit_allowances`, and a trial balance.
  `receivable` is the payment journal's account, so the two together close an
  order that was paid, canceled or written off; a returned order's receivable
  stays open by its refund until the revenue it gave back is read. **For
  operators:** order migration 000021 adds three time indexes.

- **A refund names its cause** (ADR 0187). Every refund row the returns
  workflow makes carries the id of the return, claim or exchange that caused
  it, in the transaction that writes the row. **For API consumers:** the admin
  refund listing's `reference`; the order module's payment movements carry it
  as `reference`. **For embedders:** the payment interop's `RefundCollection`
  takes a fifth argument, the cause's id, and a port declaring it must add it.
  **For operators:** payment migration 000008 adds `refunds.reference`.

- **The payment module keeps derived books** (ADR 0186). **For operators and
  accountants:** `GET /admin/v1/payment-journal?from=&to=[&currency_code=]`
  (`payment:read`) returns every capture, refund, store credit grant and loyalty
  grant in the window as a balanced debit/credit entry over six accounts —
  `receivable`, `provider_clearing` (per provider), `store_credit`,
  `store_credit_granted`, `loyalty`, `loyalty_granted` — and a trial balance per
  currency. Nothing is written; holds and releases are not entries. A window over
  93 days or 10,000 movements is refused. **For operators:** payment migration
  000007 adds four time indexes.

- **A contract price names its buyer** (ADR 0185). **For merchants:** an
  override price list whose price carries the rule `customer_id eq …` is that
  customer's contract, and one ruled on `company_id` is the contract of every
  employee of that b2b company; a contract outranks the buyer's segment price
  at the same list priority, even when the segment price is cheaper, and a
  customer's own contract outranks their company's. **For embedders:** the
  cart's rule context carries `customer_id` for a cart with a customer and
  `company_id` for a company's employee, so promotions can be written for them
  too; the b2b interop gains `CompanyOfCustomer`. **For API consumers:**
  `GET /admin/v1/price-sets/{id}/calculate` given `attr_customer_id` or
  `attr_company_id` ranks as a cart does.

- **The GraphQL product names its neighbors** (ADR 0184). **For API
  consumers:** `Product.related(type: cross_sell | up_sell | substitute)` returns
  what `GET …/products/{id}/related?type=` returns, in the same order and with
  the same products left out. Each product that selects it counts as a database
  round trip against `GRAPHQL_MAX_COMPLEXITY`: a product page with all three
  lists passes, the field under a page of 50 products does not.

- **A stranger starts with one command** (ADR 0183). The README opens with
  `go run github.com/bdrtr/gobit/cmd/server@latest new shop`, and a gate holds
  its package path and verb to the tree. **For operators:** a binary built
  without `-ldflags` reports the version the Go toolchain stamped — its tag, its
  pseudo-version, `+dirty` for a tree with changes — in the startup log, the
  OpenAPI document and on every trace, instead of `dev`. **For embedders:**
  `Version("")` now means that stamp; the generated `main.go` and the starter
  leave `version` empty, and a project that passes `"dev"` keeps printing it.

## [0.9.0] — 2026-09-25

Every item **is one line and names its decision**. The rationale, the
measurement and the opposing reading are not here: the decision lives in its
record under `docs/adr/`, the discussion that produced it in the commit
message that brought it, and the numbers under `docs/measurements/`. This
section was cut down from 4,604 lines to this list on 2026-09-09 — the full
narrative remains in the git history. Fifty-two decisions were added in bulk
on 2026-09-09: all of them had been made and none had been announced, and
nothing asked about it — `TestTheChangelogNamesEveryUnreleasedDecision` now
asks (ADR 0098).

### Breaking changes

Legitimate in a minor version throughout `0.x` (see the head of this file).
Collected on the day of the cut by diffing v0.8.0 against this tree: the
OpenAPI documents of both binaries, `gorelease` over the importable packages,
the `env:` tags, the scope bindings and the event payloads. Each item names what
a v0.8.0 user has to change.

**HTTP clients**

- **The storefront catalog reads moved under the sales channel** (ADR 0044):
  `/store/v1/sales-channels/{sales_channel_id}/products[/{id}]`, and the search
  plugin's `/store/v1/sales-channels/{sales_channel_id}/search`. A channel the
  key does not hold is 403, and so is a key bound to no channel.
- **`POST /store/v1/carts/{id}/shipping-methods` refuses `amount` and `name`**
  (ADR 0021): the server prices the option, and unknown fields are refused.
- **The storefront `GET /store/v1/price-sets/{id}` has a body of its own**
  (ADR 0167): `prices[].rules` is gone.
- **The customer profile and address book need a bound identity** (ADR 0043):
  without a verifier (`contrib/identity-session` or your own) they answer
  `401 identity_not_bound`. A storefront request that names a customer —
  `customer_id` on a cart, the B2B reads — answers the same unless
  `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM=true` (ADR 0125). Guest carts are
  unaffected.
- **`POST /admin/v1/promotions/compute` requires `unit_amount` on every item**,
  with `unit_amount × quantity = amount` (ADR 0112).
- **Schema component names carry their module** (ADR 0036): a generated
  client's type names change and the client must be regenerated.
- **A panel screen requires its module's privilege** (ADR 0156): an operator
  holding neither it nor `admin` gets 403 where v0.8.0 let them in.
- **Error MESSAGES are English**; codes and JSON keys are unchanged.

**Operators**

- **The pool refuses a session that is not READ COMMITTED** (ADR 0166, D123):
  a database or role defaulting to REPEATABLE READ or SERIALIZABLE no longer
  starts.
- **`JWT_TTL` above 24h is refused outside `APP_ENV=development`** (ADR 0031).
- **`METRIC_EXPORT_INTERVAL` is gone and metrics are no longer pushed over
  OTLP** (ADR 0046): set `METRICS_ADDR` and scrape `/metrics`. Traces still use
  OTLP.
- **Some migrations fail on data v0.8.0 allowed:** a promotion rule with no
  values (ADR 0169), two option values of one option that fold alike
  (ADR 0039), and a hand-stamped duplicate where the order, payment,
  fulfillment, inventory and region tables drop `deleted_at` and rebuild their
  unique indexes over every row (ADR 0054). Rows hidden by hand come back.
- **Drain checkouts before upgrading** (ADR 0109): the checkout saga gained a
  step, and a record the old binary left half-way needs manual recovery; check
  `gobit stuck` first.

**Embedders and plugin authors**

- **`plugins/errorsentry`, `plugins/paymentstripe` and `plugins/searchpg`:**
  `Setup` takes `*core/plugin.Host`, whose v0.8.0 counterpart lived under
  `internal/` and could not be built outside the tree; `searchpg.SearchPath` has
  the channel segment. Everything else importable today was not importable in v0.8.0.
- **A plugin's inbound callback is registered with a verifier** (ADR 0028), and
  no plugin route may sit under the panel's address (ADR 0157); both are
  startup refusals.

**Behavior a client may notice**

- An event can arrive twice with the same id (the outbox, ADR 0023); dedupe on
  the id. The Redis bus redelivers a message a dead consumer held (ADR 0162).
- A storefront cart write reprices the cart, so its total includes shipping and
  `expected_total` has to match it (ADR 0173).
- `discountable=false` is no longer discounted (ADR 0048), customer-group
  prices reach storefront carts (ADR 0049), `paid_total` is filled (ADR 0022),
  and a mixed-rate cart is taxed per line.

### Fixes

- **Nothing bound the database to the isolation level the locks rely on**
  (D123). Every lock that guards a sum is correct only under READ COMMITTED;
  with the server, the database or the role set to REPEATABLE READ, the
  integration lane failed forty tests across fifteen packages, five of them
  silently: eight orders against a spending limit that covers one, sixteen
  cancellations on a three-unit line, 10,000 of credit on a 6,100 order, a
  category cycle, a stock write to a location being closed. Closed by
  ADR 0166.

- **The S3 tests were red only in CI again, for D80's reason** (D121). The
  registry D80 moved to (`quay.io/minio/minio`) no longer serves either the
  pinned tag or `latest`, and this machine was holding a year-old copy. MinIO
  does not distribute the community server as an image anywhere it can be
  pulled without credentials; by moving to the same vendor's second address,
  D80 had put the same exposure one hostname away. The tests now run against
  RustFS 1.0.0, and two of its properties were measured before it was trusted:
  dropping the line ending of the canonical header block turns the signature
  tests red, and skipping the bucket policy turns anonymous reads red. The rig
  no longer carries any vendor's name.

- **The scaffold test's offline-tidy premise was wrong from the start**
  (D122). The generated project's graph differs from the checkout's: it wants
  the test-only dependencies of its dependencies, and the checkout's own
  `go mod download` never fetches them. The test was green only on caches
  warmed by a networked tidy; when go.sum changed CI's cache key, it started
  cold and failed. The tidy now goes through the module proxy, as a user's
  does, and the build stays offline.

- **The balance of a customer with no rows could be spent twice** (D118). Both
  balance ledgers locked the customer's ROWS before summing the balance, and a
  customer with no rows locked nothing; money committed between the lock and
  the sum was a row nobody held, the second authorization locked without
  waiting for it, and both spent — in the end taking a 100-point balance down
  to −100. ADR 0152 had written this down as "not a hole"; under ADR 0165 the
  interleaving writer is the automatic earn that runs on every named capture.
  The lock is now ON THE BALANCE: a transaction-scoped advisory lock keyed on
  the customer and the currency, on the argument of the order module's
  spending lock. Every lock test in the tree funded the customer BEFORE the
  rival locked; the new test also runs the shape where the money arrives while
  the lock is held, and builds the rival with the repository's own function
  rather than an SQL copy of the lock.

- **The isolation level the balance lock rests on was named nowhere** (D119).
  The waiting authorization's sum is a fresh snapshot only under READ
  COMMITTED; the payment repository opened every transaction at the server's
  default, and a role or a database can make that REPEATABLE READ — there,
  with the lock in place, the balance is spent a second time. `WithTx` now
  opens the level by name, and the witness is a pool whose connections start
  in REPEATABLE READ. The same unnamed level sits under the other modules'
  locks too; measurement 0165 names it.

- **The setting wire that registers the person-bound tenders had no witness**
  (D120). The composition root hands
  `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` to the payment module INVERTED;
  had the negation been dropped, the balance tenders would be registered on an
  installation where anyone can type a customer name, and every lane would
  have stayed green. Two smoke processes now read the storefront's provider
  list: the one with the setting on must not offer credit and points, and the
  stock installation must offer both.

- **The hourly reconciliation reported store credit's sessions as "cannot be
  asked" and its ledger as "verified by nothing"** (D117). The provider did
  not implement `SessionInspector`; the manual provider, of the same shape,
  did — the two precedents contradicted each other, and the points tender had
  to pick one. For a ledger that writes in the same transaction, the
  "unverified" sentence is wrong: the hole the reconciliation closes (the
  provider took the money, the module's commit failed) cannot form here; the
  hold is rolled back together with the module. The shared machine now answers
  `InspectSession` for both tenders from its own session table, and the report
  says "the two agree" for these sessions.

- **The credit ledger had two writers and no gate held the pair** (D116).
  `IssueCredit` and the store-credit provider write to the same
  `payment_store_credit_entries` table, and no test would have caught a sixth
  writer; the points ledger's gate, for its part, accepted a SINGLE writer, so
  there was no tool for a ledger with a second writer.
  `TestEveryPaymentLedgerWriteEntersThroughANamedDoor` now looks at both
  ledgers at once and DERIVES the second door not from a list of function
  names but from the identity the tender's `ID()` method returns — written
  with names, the same-named methods of the manual and PayTR providers would
  have got in too. A renamed or deleted tender turns the check red rather than
  leaving a door that opens for no one, and every door — the named function
  and the tender alike — has to be seen actually WRITING.

- **Three counts had gone stale** (D115). The payment module's package godoc
  said "the only provider out of the box is manual" (wrong since ADR 0152, and
  two short counting points); the provider conformance gate's godoc said
  "twelve providers" (there were thirteen packages, fourteen with the tender);
  `api/describe.go` said "the ONLY endpoint that reads the query string"
  (there are five readers). No gate checks any of them — the line carries no
  path to the population. All three sentences now name the MECHANISM rather
  than the number: what is registered at registration time, a population that
  stays above a floor, the readers described in their own file — the class of
  D102/D110/D111, the same repair.

- **The checkout godoc said the same cart could not be retried after a failed
  attempt** (D114). The "# Idempotency" section of
  `internal/workflows/checkout/doc.go` wrote this down as "an accepted cost";
  yet the engine (`workflow.StatusFailed` RELEASES the key), its test and
  `docs/known-limits.md` say the exact opposite, and pgstore clears the key in
  the same UPDATE. Six copies, none of them gated, three wrong, and all three
  in the package that describes the decision. All three were rewritten — the
  copy that describes the decision has to be the copy the engine keeps — and
  the e2e now completes a refused cart with the SAME cart through another
  tender: for points, "not enough points, pay by card" is the ordinary path,
  and no lane walked it.

- **A guest who chose a person-bound tender on the storefront got a 500**
  (D113). The store-credit provider refused an ownerless session with an
  INTERNAL ERROR — on the reasoning that the caller is the module ("it opened
  a collection for nobody and chose a tender that belongs to someone") — but
  on the storefront the one choosing is the shopper who read a list containing
  that tender, and `core/http` turns an internal error into a 500. The points
  tender would have inherited the same error. The two tenders' shared machine
  now returns CONFLICT (409): the request is well-formed; what refuses it is
  the state it meets. The old test pinned only the error code, not the HTTP
  status.

- **The identifier-prefix test held five of the eight prefixes** (D112).
  `TestIdentifierPrefixesAndOrdering` was a hand-written map: store credit's
  two prefixes (ADR 0152) and the points ledger's (ADR 0164) were added to
  `ids.go` but not to the test, and a ninth would have gone invisible the same
  way. The population is now READ from the `IDPrefix` constants in `ids.go`,
  and the test refuses a shorter map — the shape every hand-listed population
  in this repository needs sooner or later.

- **Five sentences put the module population at seventeen; the tree holds
  eighteen** (D110). The count gate takes a claim under check only when the
  population's PATH is on the same line as the number, and that is a measured
  decision: the bare "number + plural noun" shape is dominated by subsets —
  `internal/arch` alone holds thirty ("two modules share a schema", "three
  modules", "one module"). So the gate was NOT WIDENED — loosening it by
  directory would have pulled in exactly those sentences, which is the
  "exemption list with a check bolted on" shape the gate's own godoc refuses.
  The reading separated three kinds of sentence: the DATED measurement
  ("measured on 2026-09-07, the matrix named fifteen modules") states what was
  true on its date and was left as it is; the UNNEEDED number ("all 17 modules
  carry both an api and a service directory", "seventeen of the repository's
  seventeen modules are registered", "a file of the same name exists in
  seventeen modules at once" — two copies) was something the sentence did not
  need and was deleted, so it can never rot again; and the INFORMATION-BEARING
  number ("FOURTEEN of the seventeen module api packages, the other three
  define them with constants") became a pinned record: the packages left
  outside the derived scope are now asserted against a record that names each
  one's reason. The pin found what the prose had missed: `settings/api` had
  silently dropped out of the scope, and for a reason DIFFERENT from the three
  named — it never builds an `openapi.Parameter` and never reads a query
  parameter, so, unlike them, nothing is left unchecked.

- **The body of an event that travels two paths was written by hand TWICE**
  (D109). `order.placed` is published directly for speed and written to the
  outbox for assurance, and both carry the same DERIVED event id — its
  payload, though, was two separate nine-key map literals, identical on the
  day I read them and with nothing comparing them. Adding a field to one copy
  would have given a single event id two bodies, and which one a subscriber
  saw would have depended on whether the fast path or the relay got there
  first. What makes this a ledger row is WHERE the rule stood: the payment
  module has built its payload in one place since the day it was written, and
  its godoc pointed at the order module BY NAME as "the shape it avoids" — the
  rule was right, was written down, and was enforced nowhere. Measured before
  the fix: six module services publish, five build the body from a named
  function, one did not; five topics travel two paths. The gate now walks
  every non-test Go file, matches an outbox write with an `eventbus.Event`
  literal by TOPIC within the package, and refuses a pair whose payload
  expressions are not the same constructor call; proven with three mutations,
  one of them putting the original literal back. Two stale sentences fell out
  of the same reading: the accusation in the payment godoc (now a description
  of the gate) and the analytics plugin's "two of the eight keys", written for
  a payload that carries nine.

- **A gate built an assertion on a side effect that runs AFTER the thing it
  waited for** (D108).
  `TestRedisIntegrationTakesOverWhatADeadConsumerWasHolding` (ADR 0162) waited
  for the taken-over event to reach the handler and then asserted that the
  pending list was empty; yet the ACK runs in a `defer` AFTER the handler
  RETURNS, and the handler returns as soon as it has written to a buffered
  channel. So the assertion could run before the ACK — and in CI it did, one
  push after the test went in green. The race is proven by CONSTRUCTION, not
  by reproduction: on the machine that wrote the test it did not fail once in
  five runs with `-race`, which is exactly why it got in. The fix is not
  tolerance but ORDER: `Shutdown` waits for the consumption loop, and the loop
  cannot return until the dispatch it is in — ACK included — has finished. The
  failure message now also writes the pending entry's OWNER: under the killed
  consumer's name it says the takeover never happened, under this process's
  name it says it happened but was not ACKed, and the old message could not
  tell the reader which.

- **An integration test connected to a database this repository never
  started** (D107). `TestTheToolListIsThisInstallationsOwnSchema` (ADR 0161)
  called `config.Load()` without supplying `DATABASE_URL`, so it connected to
  whatever answered on localhost:5432 — the development database on the
  machine that wrote the test. Locally nine lanes passed green; on the CI
  runner it went red with `connection refused` and main broke. Its assertions
  were not wrong (the tool list is derived from the router tree, not from
  data); what was wrong was that the INSTALLATION under test came from the
  environment. Every neighboring scenario in the package already starts its
  own container and hands its DSN to the environment (`migrateDSN`), and this
  test now uses that shape. The class is the fixture's: a gate whose subject
  is "this installation" has to START the installation, or what it measures is
  the developer's machine.

- **Nothing stopped production code from importing a TEST package** (D106).
  The rule lived in a single godoc — the GraphQL handler's capture writer,
  which says httptest "belongs to the test binary" and writes nine lines by
  hand for exactly that reason. Proven by mutation before the fix: an httptest
  import added to a file in the PUBLISHED tree passed lint and every arch
  gate. A gate now refuses `net/http/httptest`, `testing`, `testing/fstest`
  and `testing/iotest` in production files. The exemption is DERIVED, not
  LISTED: a package that no non-test file imports is test support, whatever
  its name — this covers the published conformance package `core/identitytest`
  and the local `internal/benchbudget` without naming either, and catches the
  day one of them starts being reached from production. Because the derivation
  itself carries weight, its size is checked in BOTH directions: broken so as
  to count everything as test support, it swallowed a real violation and left
  the gate green — measured — and it now fails when the exemption count
  widens.

- **The gate that refuses an unprotected state-changing route counted only
  chi's VERB methods** (D103). `Handle`, `HandleFunc` and `Mount` — all three
  bind EVERY method, POST included — were outside the population. The result
  was a gate that refused the honest form of the smuggling and accepted the
  sneaky one: `r.Post("/mcp", h)` placed on the composition root failed as
  "bound outside any protected prefix", while `r.Handle("/mcp", h)` passed —
  though both bind a POST that nothing verifies, subjects to a quota or
  records. It was found while measuring where the MCP server could be mounted,
  not by READING the gate but by trying to PUT something there. Nothing was
  hiding in the tree: the only production `Handle`/`HandleFunc` calls are in
  the operator and profiling muxes, and those files do not import chi, so they
  were outside the population anyway; `callbacks.Mount(router)` takes a SINGLE
  argument, and the collector's existing arity rule leaves it out without
  exempting it by name. This is the very class the gate itself was written
  against — a rule whose checked population is narrower than the sentence it
  states — and in a gate that exists because an unverified POST turned a
  payment to "paid".

- **CI never linted the separate modules** (D100, D102). The Lint job ran the
  root through `golangci-lint-action`, then ran `make vuln`; it never ran
  `make lint` — which is the target that walks the separate modules. The
  action lints the module it stands in, and this repository is SIX modules:
  the root, three examples, two contrib trees. The only linter that reached
  `examples/` and `contrib/` was the pre-push hook, and `git push --no-verify`
  bypasses it. The job file's OWN comment already stated the rule, one step
  further down: "runs through `make vuln`, not a bare command, so that the
  target the developer runs locally is the target CI runs". Lint now does too,
  and the version has one home instead of two — the action pinned its own copy
  beside the Makefile's. A gate refuses the action BY NAME and looks for four
  targets on `run:` lines; its first version searched the whole file, and a
  COMMENT mentioning `make vuln` satisfied it, so deleting that step stayed
  green. In the same round the "in THREE modules" count in the vuln target's
  comment was corrected too (D102): the list had grown to six, the sentence
  had stayed at three — a hand-written count goes silently wrong when what it
  counts grows.

- **The panel's assets were never refetched, and the ones behind a privilege
  were open to SHARED caches** (D94, D95). The stamp went only into the ETag,
  the address never changed, and the response says `immutable` — so the
  browser was being told not to ask for a year. The defect was written in its
  own godoc: it said the stamp derives from the bytes and ended with "so the
  operator's browser refetches exactly when the file really changes", yet no
  conditional request is ever made and the writer has no `If-None-Match`
  branch. The test beside it carried the same claim IN ITS NAME and verified
  only the ETag. The stamp now sits IN THE ADDRESS too, so when the bytes
  change the address changes and `immutable` becomes honest. The second is a
  defect the previous commit produced itself: ADR 0156 put the reviews script
  and every registered screen's script behind a privilege, while
  `Cache-Control` had stayed `public` — an invitation for an intervening proxy
  to hand those bytes to a caller the panel refuses.
  `core/http.WritePrivateAsset` is published, and the panel chooses between
  the two writers by consulting the SAME scope table that sets up the REFUSAL,
  so a path that gains a privilege stops being publicly cacheable in the same
  edit. What is protected is not the bytes — they are the same from one
  installation to the next — but the RULE: an endpoint whose refusal something
  in front of it can answer has not set a rule. The gate walks the router,
  finds every response that carries a stamp, and asserts BOTH directions.
- **The order screen's reason for not showing the lines was wrong** (D96), in
  two copies. The sentence "the read layer joins across LINKS, not WITHIN a
  module" is wrong in both halves: the order line is a read entity in its own
  right, accepts an `order_id` filter, and the panel's OWN sales report reads
  it from the same surface one file away. The same sentence appears twice more
  for CUSTOMER addresses, and there it is TRUE — the customer module publishes
  a single entity, with no address entity — so the wording was not changed
  everywhere; each copy's SUBJECT was told apart.

- **One hundred thirty-five test files sat where NO lane that runs without
  Docker could see them** (D88). An unsatisfied build constraint makes a file
  invisible to the Go toolchain: `go build ./...`, `go vet ./...`,
  `go test ./...` and `golangci-lint run ./...` SKIP it, and the only thing
  that compiled those files until now was `make test-integration` — the lane
  that needs a container and takes minutes, so the one a person runs LAST.
  When `Interop.CreateCollection` was widened for ADR 0152, three call sites
  in `payment_integration_test.go` no longer fit, and nothing said so except a
  `go vet -tags integration ./...` run by hand. `.golangci.yml` now carries
  `integration` in `run.build-tags`. What surfaced as soon as the tag was
  turned on: fifty-two British spellings against the US spelling rule, of the
  kind refused everywhere since ADR 0012, ten gocritic findings, a swallowed
  error and a helper that is never called — sixty-four findings in
  thirty-eight files, none of them new and all of them invisible. The tag list
  is deliberately ONE entry long: `smoke` files start real processes, `load`
  files are benchmarks, both have their own lane, and neither has been
  something a refactor silently broke.

- **A closed limit was still published as a LIMIT** (D81). ADR 0136 turned the
  cross-module METHOD SET into a compile-time check; yet both "known limits"
  documents still opened with "Cross-module signatures are not checked at
  compile time". Worse: `docs/mimari.md` contradicted ITSELF — section 5 had
  been corrected in the previous commit, while section 12's table and a
  sentence inside section 5 had been left behind. The tree was SWEPT, not
  spot-fixed: the sentence "the compiler does not check" has twenty copies,
  and FIFTEEN OF THEM ARE TRUE and were deliberately left untouched — their
  subject is the JSON SCHEMA or the VALUES crossing the boundary, which the
  pin does not touch. The distinction is the whole row: the signature is
  checked, the MEANING is not. And `docs/mimari.md`'s own "Known limits"
  section now says it is a SUBSET carrying ten of the twenty-nine items.

- **The integration lane was green on this machine thanks to a year-old
  CACHE** (D80). CI failed with "pull access denied for minio/minio"; the
  message reads like a missing tag, but it is not — Docker Hub closes both the
  pinned tag and `latest` to anonymous pulls, so the problem is not the tag
  but the REPOSITORY. The same tag is served without credentials from MinIO's
  own registry (`quay.io/minio/minio`). The pinning was never the problem;
  what changed was the access BENEATH the pin — a class a pin cannot defend
  against. The reason every local run passed was the twelve-month-old cache;
  the proof that the fix is real is that the `quay.io/...` name was not in the
  cache: the test HAD to pull, and it did.

- **The integration lane failed one run in twenty from a DATA RACE in a test
  fake** (D79). The fake counts how many questions are asked of the shipping
  boundary — which is what proves a retry asks NOTHING — and the counter was
  incremented unguarded under a test that opens two parcels in two goroutines.
  A race by construction; the lane's silence was the detector reporting only
  what it saw. Measured at HEAD: one run in twenty. The counter is also READ
  from the test's goroutine, so the reader was guarded too — fixing only the
  writer would have left half of it standing.

- **The architecture narrative called every flow a saga, and the gate that
  checks counts AGREED with it** (D78). Of the seven packages under
  `internal/workflows`, only one uses the saga engine; the document taught the
  reader that every flow has an execution record, a compensation chain and an
  idempotency key. The shape never mentioned was the one that cost two
  defects: the flow driven entirely by the bus, which nothing resolves. Worse
  than the prose was the CHECK — the count dictionary treated `saga` as a
  synonym of `workflows` and checked it against the directory count, so the
  sentence "seven sagas" would have been approved. The population is now
  derived from imports, and the two names are separate entries. Two more
  sentences in the same document were stale: that the proof of the binding is
  the e2e test (whereas since ADR 0136 it is a compile-time pin) and that
  `<module>.interop` is "for sagas/core" (whereas modules resolve each
  other's).

### Decisions

- **An installed binary starts a project** (ADR 0182). `gobit new` in a binary
  built without the Makefile's build facts requires the library at the version
  the Go toolchain stamped into it, so `go install
  github.com/bdrtr/gobit/cmd/server@v0.9.0` gives a binary whose projects
  require v0.9.0; it refuses only for `go run`, a tree with uncommitted changes
  or a replaced library. **For embedders:** `golang.org/x/mod`, already in the
  graph, is now a direct require.

- **The panel edits a product's neighbors** (ADR 0181). The admin panel's
  product page lists the related products, each in the operator's order, and
  marks one the storefront leaves out. "Edit related products" opens a form
  with one handle (or id) per line for each kind, and saves all three lists or
  none. **For read-layer consumers:** the `product` record carries
  `cross_sell_ids`, `up_sell_ids` and `substitute_ids`, filled only when asked
  for or when the whole record is read. **For embedders:** the product module's
  admin surface gains `SetProductRelations(ctx, id, map[kind][]ref)`.

- **A product names its neighbors** (ADR 0180). A product holds an ordered
  list of up to 50 other products for each of `cross_sell`, `up_sell` and
  `substitute`. **For API consumers:** `GET /admin/v1/products/{id}/relations`
  reads every kind, and `PUT /admin/v1/products/{id}/relations/{type}` replaces
  one kind with `{"product_ids": [...]}`. A list naming a missing or deleted
  product, the product itself, a duplicate or more than 50 entries is refused
  whole. The storefront reads one kind at
  `GET /store/v1/sales-channels/{sales_channel_id}/products/{id}/related?type=`.
  It answers 404 for a product it may not show, and it leaves out a related
  product that is a draft or bound to another channel, so a launch can be
  lined up before it happens. Deleting a product takes it off every list.
  **For operators:** migration 000009 adds the `product_relation` table.

- **A product can be scheduled to leave** (ADR 0179). A draft or a published
  product takes an `archive_at` beside `publish_at`; the `scheduled-publish`
  job archives what is due after publishing what is due, and each change gets
  the `product.updated` event the same change by hand gets. **For API
  consumers:** `PUT /admin/v1/products/{id}/schedule` now REPLACES the whole
  schedule — `publish_at` and `archive_at` are both optional, one left out is
  taken off, and an empty body is refused. The panel's form gains "Archive at
  (UTC)". **For operators:** migration 000008 adds the column and two
  constraints.

- **The panel schedules a draft** (ADR 0178). The admin panel's product form
  has a "Publish at (UTC)" field and the product page shows the moment; saving
  a draft with a moment schedules it and saving it with none takes the
  schedule off. A moment that is malformed, past, or set on a product not saved
  as a draft is refused before anything is written. The product record in the
  read layer carries `publish_at`.

- **A draft can be scheduled** (ADR 0177). `PUT /admin/v1/products/{id}/schedule`
  with a `publish_at` in the future gives a draft its launch moment, and
  `DELETE` on the same address takes it off; a new job, `scheduled-publish`,
  publishes the due drafts every minute and each gets the `product.updated`
  event a publication by hand gets. Admin product bodies gain `publish_at`;
  storefront bodies never carry it. **For operators:** migration 000007 adds
  the column and a constraint that only a draft may carry a moment; publishing
  or archiving a scheduled draft by hand clears it.

- **A promotion can be tried on past orders** (ADR 0176). A new admin endpoint,
  `GET /admin/v1/promotions/{id}/trial?from=&to=`, prices a promotion —
  typically a draft — against the orders placed in a period as if it had been
  published, and reports per currency what it would have added to the
  discounts those orders actually got, with the orders it would have
  discounted most. It writes nothing, lists what it had to assume (today's
  catalog and customer groups, no cart metadata, no usage limit or campaign),
  and needs `order:read` beside `promotion:read`. A period is at most 93 days
  and 5000 orders.

- **A payment that can never be made opens no order** (ADR 0175, D130). The
  checkout now asks the payment module before the saga whether the chosen
  provider is registered and, for store credit and loyalty points, whether the
  cart names a customer. Before, both refusals came at the payment step, after
  the order was placed and `order.placed` published, and the compensation
  canceled it. The status codes and error codes are unchanged; the refusal
  message is now the payment module's own.

- **The example shop checks out** (ADR 0174). `examples/storefront` gains a
  fourth page, `/shop/checkout`: an e-mail and a shipping address, the options
  the cart's region serves, a payment provider, and the completion with the
  cart's own total as `expected_total`. A shopper who comes back replaces the
  delivery rather than adding a second. Every store call the script makes is
  written once as a verb and the template the route is bound under, and
  `TestTheShopCallsOnlyBoundRoutes` refuses one the tree does not bind.

- **A storefront write leaves the cart priced** (ADR 0173, D129). Adding or
  removing a shipping method, removing a line, writing either address, the
  e-mail or the customer, and a merge now recompute the cart's totals before
  they answer; before, each left `totals_stale` true and nothing on the store
  surface could refresh it, so a shopper who chose a delivery was shown a total
  without it and the completion refused the `expected_total` they approved.
  `POST /store/v1/carts/{id}` and `POST /store/v1/carts/{id}/merge` answer with
  the repriced cart. `docs/first-run.md` now creates a shipping option and
  ships its order, so its total is 81700. **For embedders wiring the cart
  module by hand:** `api.Flows` gains `Repricing`, and the six writes refuse
  without it.

- **A holding says what erasure does to it** (ADR 0172). `personaldata.Holding`
  gains `OnErasure` — `Emptied` or `Kept` — and every holder declares it for
  every column; `Declaration.KeptOnErasure` and `Declaration.Paths` read what an
  erasure keeps off the declaration, and the holders that kept a second list
  now derive it. `GET /admin/v1/personal-data` publishes `on_erasure` on every
  holding, so a privacy notice can say which data goes on request and which
  stays. **For embedders with their own holders:** a `Holding` that does not
  set `OnErasure` declares neither, and the repository's own gate refuses one.

- **An order can be read as it stood** (ADR 0171). A new admin endpoint,
  `GET /admin/v1/orders/{id}/as-of?at=<RFC 3339>`, answers an order at a past
  moment: its status, its money (total, credited, captured, refunded and
  outstanding, summed from the payment movements rather than read from the
  order's overwritten summary), what had been canceled of each line, and the
  status of every return, claim, exchange, replacement and parcel then. What
  the records cannot answer is said to be unknown: the status of an order
  archived before archiving was dated, and a contact erased since. A moment
  that has not happened or that precedes the order is refused with a 422.

- **An order's timeline tells every movement** (ADR 0170). Each capture and
  each refund is its own entry with the amount it moved; before, the timeline
  showed only the first capture and the last refund, each carrying the
  lifetime total, so two partial refunds read as one refund of their sum.
  Line cancellations (with a new `quantity` field), credits, replacements,
  an exchange's funding and the erasure of the order's personal data are new
  entry kinds. The storefront timeline gains the line cancellations and the
  replacements; the money and the erasure stay admin-only. **For API
  consumers:** a money entry's `ref_id` now names the payment or the refund
  it came from, not the payment collection. The payment collection's query
  entity offers a new `movements` field.

- **A CHECK constraint answers true or false** (ADR 0169). An integration test
  applies every migrations directory in the tree — core, modules, plugins and
  contrib — and evaluates each of the 349 CHECK constraints over NULL, the
  literals it names and sample values of its columns; a constraint that
  answers NULL for some row lets that row through, and fails the test. The
  first run found the promotion rules' `promotion_rule_values_check` written
  with `array_length`, which passes an empty array: a new promotion migration
  replaces it with `cardinality`. **For operators:** that migration fails on
  an installation that already holds a promotion rule with no values, which
  the service never wrote; it names the constraint.

- **An order line remembers the price it was charged** (ADR 0168). Pricing's
  bulk answer names the price row its ladder picked and, for a list price, the
  list and its type; the cart's totals round and the checkout's plan carry them
  into the order, and each line keeps them. Both order views publish them as
  `price_origin`, absent when unknown — every line sold before the upgrade, and
  a line placed from a plan written before it. The price id stays readable in
  pricing's history after the row is replaced (ADR 0167). An incoherent origin
  is refused as a 400 and by a CHECK constraint.

- **A price keeps its history** (ADR 0167). Every write to a price, a rule, a
  set or a list appends a snapshot of what the price ladder reads, in the same
  transaction, and the unchanged ladder run over those snapshots says what a
  set charged at any past moment — a sale window that opened with no write
  included. A storefront price names its list's type, and the sale price
  charged now carries `reduced_since` and `lowest_prior_amount`, the lowest
  price of the thirty days before the reduction, on the product listing and the
  price set endpoint alike; each is absent when the history cannot state it. A
  new admin endpoint, `GET /admin/v1/price-sets/{id}/price-history`, answers
  what a set charged over a window and which price and list each stretch came
  from. ADR 0047 is amended: a replaced price is still deleted, and what it was
  is kept in the snapshot before. The history starts at the upgrade.

- **A connection that does not start in READ COMMITTED is refused**
  (ADR 0166). The pool `core/db` builds reads every new session's default
  isolation level and refuses the connection if it is not READ COMMITTED — at
  startup and on every connection it opens while the process runs. **Breaking
  for operators:** a line is added to ADR 0015's cluster contract; an
  installation whose default is REPEATABLE READ or SERIALIZABLE no longer
  starts, and the error states the level it found and the statement that
  undoes it.

- **A customer can now pay with their POINTS** (ADR 0165). Points are spent
  through the slot store credit goes through — a provider in this module,
  `loyalty_points` — and ONE POINT is worth ONE minor unit of the currency it
  was earned in: the contract's amount and the ledger's points are a single
  number, nothing is converted at the boundary, and the earn rate reads as a
  rebate in basis points. A capture paid in points earns NO points — at the
  ceiling rate a point would earn itself back, and below it the shop would pay
  a rebate on a debt being extinguished; the earn target is now computed only
  from the collection's net money captured through OTHER providers, while
  spent credit keeps earning, because it is money owed at face value. The two
  tenders are ONE state machine: the store-credit and points providers are not
  two hand-written copies but two registrations of the single machine in the
  `balancetender` package, each running over its own ledger — authorization
  writes a MINUS hold, cancellation a release, a refund a refund row, and a
  capture writes no spend (it only releases the part it did not take), and
  every spend row references the provider's OWN session rather than the
  collection. Before the balance is summed, it is the BALANCE that gets
  locked, not the rows — an advisory lock keyed on the customer and the
  currency (D118) — and the transaction opens READ COMMITTED by name (D119);
  this is ADR 0152's lock too. The ledger gate derives the second door from
  the tender's IDENTITY rather than a list of names, and now looks at the
  credit ledger as well. The balance can go BELOW ZERO: a refund takes back
  points the customer has already spent and is not refused for it; the next
  earn closes the deficit first, and the tender refuses against it. Both
  tenders now answer the hourly reconciliation (credit used to count as
  "cannot be asked"); a guest who picks either one gets a 409 instead of a
  server error; both are registered under ONE option, on an installation where
  the customer claim is proven — the `StoreCredit` name is gone, replaced by
  `Options.PersonBoundTenders`, named for its reason. On the storefront,
  points cover either the whole order or none of it — splitting is the admin
  surface's job; the customer still cannot read their own balance.

- **A capture now earns the customer POINTS** (ADR 0164). The append-only
  `payment_loyalty_entries` ledger lives in the payment module, and the only
  thing that writes its rows is `writeCollectionTotals` — the one function
  that moves a collection's totals. Every row is a DIFFERENCE: the target is
  computed from the collection's own cumulative amount and the difference is
  appended on top of what has been written, so when the same event arrives a
  second time nothing is written, and a refund writes a NEGATIVE row. All
  three of the nouns the feature list proposed fell under measurement: the
  name `loyalty` is TAKEN by `examples/starter/loyalty` (the registry refuses
  a repeated name before mounting routes, and no lane boots the starter),
  `order.placed` is published in the saga's second step — before the money has
  even moved — and on a guest order its customer is EMPTY, and an earn half
  alone means a table no gate can see (the slice order ADR 0153 inverted). The
  rate is a basis-point setting with a zero default: the ledger exists, and no
  installation earns. The ceiling is one point per minor unit, and a value
  above it is not SILENTLY REDUCED but refused — an arch assertion ties the
  two separate homes together. The operator reads the balance and the history
  under `payment:read`; there is no write endpoint, because in this module a
  write means MONEY MOVING. Points cannot be spent yet.
- **A lane no longer HANDS a test the developer's database** (ADR 0163). The
  settings default to localhost:5432 and localhost:6379, and on a development
  machine both are listening, so a test that forgot to start its own
  installation passed green locally and went red on the runner (D107). The
  rule had in fact already been made, but for ONE lane and in prose:
  `internal/smoke` builds every server process's environment from scratch, and
  its godoc gives the reason by naming `DATABASE_URL`. Now `make test`,
  `make test-integration` and `make smoke` set both addresses to 127.0.0.1:1,
  where nothing listens. The address RESOLVES but does not connect, so tests
  that load the config for something else are unaffected. Measured BEFORE the
  change: the whole integration lane and the whole smoke lane were run against
  the dead addresses, both green, with not a single connection attempt in
  either run — so no scenario was leaning on a service in the environment. The
  gate's values are not written by hand but derived from `config.go`'s own
  `envDefault`: it also fails if the lane's value is set EQUAL to the default.

- **A message left in a dead consumer's hands now comes back** (ADR 0162). The
  Redis bus gives a message to a single consumer with XREADGROUP and remembers
  that it did; if that process died without ACKing, the message stayed in ITS
  pending list, and the restarted process comes back under a new name,
  `<hostname>-<pid>`, so it looks at its own empty list, and the ">" cursor
  never offered that message to anyone again. Measured against real Redis
  BEFORE the gate was written: one consumer took the message and stopped, a
  fresh consumer was opened, ten seconds later the entry was still there and
  nothing had been offered to anyone. The consumption loop now sweeps its own
  stream BETWEEN two reads: a message under ANOTHER consumer's name that has
  sat idle longer than `ClaimMinIdle` (one minute by default) is claimed with
  XCLAIM and dispatched like a new message. So the "at least once" promise
  covers the case durability is bought for — the process dying — while a
  handler's error and panic go on being ACKed UNCHANGED. A poison message is
  bounded: a message delivered three times without an ACK is not handed out a
  fourth time; it is ACKed and logged at error level — that line is the dead
  letter, and the only thing that reads it is a human. The threshold has to be
  longer than the slowest handler, because it is the ONLY thing that tells a
  slow consumer from a dead one; because XCLAIM asks about the idle time a
  SECOND time, a message its owner finished between the two commands is never
  claimed. The admin API answers one hundred twenty-one read operations, and
  until now its only callers were a browser and curl. A model client asking
  "which orders are stuck" had no way in without someone writing a wrapper —
  and a wrapper is a second endpoint list, free to go stale. `gobit mcp` now
  reads JSON-RPC on stdin and answers four methods: initialize, ping,
  tools/list and tools/call. The TOOL LIST is derived at startup from the
  document this process SERVES — one tool for every GET operation under the
  admin prefix — and a call is an in-process GET through the router this
  process mounts. Nothing is re-implemented: a tool exists only if an endpoint
  does, it says what the endpoint's own describe block says, and the call
  passes through every ring of the admin surface — identity, scope, quota and
  the audit log. Being READ-ONLY is not a promise but a property of two
  things: the list is built only from GETs, and if the credential carries the
  superior scope it is refused before the server starts (by asking the
  installation itself, through the endpoint any client would use). The names
  are derived FROM THE PATH, because no operation in this document carries an
  `operationId` — measured; so a tool moves when its endpoint moves, which is
  the honest failure: a tool that survived the move would answer about
  something else. The document cannot say which tool needs which PRIVILEGE (it
  carries no scope), so the key's scopes decide, and a refused call comes back
  in the API's own error envelope — that is what names the missing privilege.
  Two limits were recorded: this one, and that part of the document's prose is
  Turkish (forty-three of the one hundred twenty tool descriptions; the ledger
  governs FILES, not the text inside them).

- **A COMMAND no longer receives the server's events** (ADR 0160, D104, D105).
  Every verb in the dispatch opens the whole application — deliberately, and
  that is what lets `seed` take the schema from the modules themselves and
  `recover` reach the services it repairs. But opening the application
  registers the modules, and registering the modules SUBSCRIBES them to the
  event bus. On the in-memory bus that is harmless: the bus is the process
  itself. On Redis it is not — there, subscribing is not a declaration but
  creating the consumer group if it is missing and starting a goroutine that
  reads from it, and the consumers in one group receive each message ONLY
  ONCE. So `gobit seed`, on an installation with Redis, joined the server's
  group and, for as long as it ran, received `order.placed`,
  `payment.captured` and every topic the modules listen to. And it RAN what it
  received: the notification module's subscriber is registered in the same
  `Register` that subscribes, so a seed command sent order confirmations. And
  a message it could not ACK before exiting stayed in the pending list of a
  `<hostname>-<pid>` consumer that will never come back — the bus has neither
  XAUTOCLAIM nor a pending-list sweep (D105, OPEN). Assembly now takes an
  event ROLE: the two paths that answer requests (the server and the facade's
  in-process harness) consume, while five verbs PUBLISH to the real bus and
  subscribe to nothing. Publishing was deliberately left untouched — when a
  command writes through a service, it writes the outbox row in the same
  transaction and publishes directly after the commit; swapping the bus for
  the in-memory one would silently drop that direct half. The population gate
  checks the CALL SITES: every `openApplication` call has to name a role, and
  a consuming call that does not answer requests is refused — next year's verb
  will be written by copying a neighbor, and every neighbor is a command.

- **A BROWSER can now shop against gobit** (ADR 0159, D99, D100). Nothing in
  this repository put a browser in front of gobit: the storefront API is
  forty-eight routes, thirty-seven of them open to guests, and the only proof
  that any of them worked was a Go harness driving them over HTTP. Someone
  asking "can I build a shop on this?" had two options: read the tests or
  believe the README. The Next.js/SvelteKit shape the row asked for was
  MEASURED and rejected: the only gate in the whole repository that touches it
  is the path-language check, so a node toolchain would enter UNAUDITED, with
  its own lockfile and its own exposed surface. Instead, `examples/storefront`
  is a Go module like the other four: `main.go` is the published facade plus
  its own module, and the module serves three shells and a single
  framework-free script. The pages run in gobit's OWN process, so the browser
  is on the SAME ORIGIN as `/store/v1`, and no installation has to open CORS
  for the example to work. The shop holds no service and reads no database —
  the browser fetches every figure on every page, which is what makes the
  example a claim about the SURFACE rather than about the example. It refuses
  AT STARTUP when the two values it cannot discover (the publishable key and
  the sales channel) are missing: a shop that started without them would get a
  401 on every request the page makes and would look like an empty catalog. It
  carries its own content policy, and that policy CANNOT be the panel's — a
  catalog shows product images, while the panel's policy starts with
  `default-src 'none'` and has no `img-src`; there is no published policy for
  an embedder's own pages, so the embedder writes the three headers
  themselves. In the same change `.js` joined the language scan (D99): the
  repository was already shipping two hand-written scripts whose content had
  never been read, and this example would be the third — prose the operator
  reads INSIDE THE PAGE, the only shipped text nothing checked. And it emerged
  that the separate modules had never been linted in CI (D100, OPEN): the Lint
  job runs the root through the action and runs `make vuln`, not `make lint` —
  D88's shape one tree over. And adding the fifth module turned up a third
  thing (D101): the table saying which separate modules build against the
  PUBLISHED surface was hand-written and tied to nothing — deleting the fresh
  entry left the whole arch suite green. Its population now comes from the
  Makefile's `SEPARATE_MODULES`, which another gate in turn ties to the go.mod
  files on disk: the chain is disk → Makefile → table.

- **The first run is now a document the lanes EXECUTE** (ADR 0158, D98). The
  road from an empty database to a shopper's order is fifteen calls, and
  eleven of them were written down nowhere: they lived inside two harnesses —
  smoke's storefront scenario and `internal/e2e`'s rig, each setting up its
  own region, price link and stock. Every lane was green, and the gap was
  invisible EXACTLY because of that. `security.md` was the only document that
  walked from an empty database, and it ends at reading the catalog — on an
  empty database that is an empty list, so the call's success says nothing
  about whether there is anything to buy. `gobit seed` does not close the gap
  either: that verb builds the load rig (fifty-two thousand products, bulk
  SQL) and creates no region. `docs/first-run.md` now carries the whole road
  as a pasteable block, and a smoke scenario runs it against the real binary,
  verifying a status code for every LINK and the order at the end. Writing it
  found what the two harnesses could not see: the storefront helper binds TWO
  countries to its region and is therefore taxed through the "no single
  jurisdiction can be named" branch, at the REGION's rate; a single-country
  region — that is, the ordinary first installation — is taxed by the tax
  module, and if there is no tax region there the answer is zero. This is not
  a defect (the server warns with `tax_source=tax_unconfigured`), but nothing
  told an operator so. The population rule ALREADY EXISTED —
  `TestEveryChainedCurlFlowIsExecuted` keeps a document→witness map with keys
  derived from the document — and the new document arrived there as an ERROR;
  before that was found, a second gate for the same job was written and
  deleted.

- **The panel's ADDRESS now belongs to the panel** (ADR 0157, D97). ADR 0155
  had set the content policy on a SINGLE chi group that the panel's routes
  enter; the reasoning was right, its SUBJECT was wrong: a group covers every
  route the panel BINDS, while the sentence the policy states is about an
  ADDRESS. A plugin's `AddRoutes` runs on the same router, AFTER the panel,
  and its only check is a pattern collision — a pattern that does not collide
  is bound outside the group. Measured with a probe, not argued:
  `/admin/ui/rogue` answered 200 with an empty `Content-Security-Policy` and
  no `X-Frame-Options`, while the panel route beside it carried both. The hole
  was ONE ring deep, not THREE — the composition root already sets the other
  two panel rings (origin and identity) on the PREFIX, so the page was inside
  the operator's session, and there was not a single rule saying which scripts
  could run on it. The policy is now set beside those two rings, on the whole
  prefix and BEFORE THEM: it covers a refusal and a 404 as well. PRIVILEGE was
  the SECOND hole, which the policy cannot close — ADR 0156 assigns every
  panel path its privilege in the panel's own table, and a route the panel did
  not bind is in no table, so an operator with no privilege at all reached it.
  So the decision has two halves: a plugin can no longer BIND a route inside
  the panel's address, the registration is refused at startup, and the refusal
  message names the WAY IN — `RegisterAdminPage`, the path that declares a
  privilege and hands over a script the panel serves from its own origin. The
  prefix is written in two places (the panel and `core/plugin`, which cannot
  import `internal`) and BEHAVIOR binds the two: the panel's own constant is
  handed to the registry and the refusal has to fire — a drifted copy would
  guard an address nothing serves, which is the thing that reads like a rule
  and is not one. The refusal matches on a SEGMENT boundary, so
  `/admin/uipload` still binds. Nothing in the tree bound to the panel's
  address; the only plugin that reaches the panel already uses the sanctioned
  path.

- **A panel screen now costs a PRIVILEGE** (ADR 0156, D92, D93). The admin API
  names a scope on seventy-one routes, and `internal/e2e`'s privilege matrix
  proves end to end that a valid identity without the scope gets 403. The
  panel was a SECOND door to the same data and looked only at identity: its
  ring resolved the principal and put it in the context, and the framework
  read a single bit from it — `_, signedIn := PrincipalFromContext(...)`. The
  scope list was there on every request and was dropped at the assignment.
  This was not a theoretical account: `POST /admin/v1/users` takes a scope
  list, `PATCH` changes it, and such an account entered the panel and read the
  whole catalog, every customer's name and address, stock levels and the sales
  report — and could moreover CHANGE a product title, because the panel calls
  module write surfaces directly. Every panel path is now listed in a SINGLE
  table with a privilege written in the string the module's own API uses; the
  route refuses an operator who does not hold it with the panel's own 403
  page, NAMING the missing privilege, the menu drops an entry that cannot be
  opened, and the front door sends the operator to the FIRST screen they can
  open. A screen a plugin registers declares its privilege too, and a
  registration that does not is refused AT STARTUP. The privilege is per
  SCREEN: it does not narrow what an opened screen SHOWS, and the read layer
  stays unaware of the principal (known limits). The gate is held by a check
  that walks the router TWICE — one pass proves that every route refuses the
  unprivileged operator, the other that every route asks for the privilege its
  OWN path is listed with; the second exists because a binding line names its
  path twice, and a line that pairs one screen's path with another's handler
  refuses the unprivileged operator just as correctly. The same round found
  that ADR 0155's policy gate was built with NIL pages and never walked a
  plugin's screen (D93).

- **A plugin can now put a screen in the admin PANEL** (ADR 0155, D91). The
  panel came with six screens and there was no way to add a seventh:
  `sections()` is a six-element PACKAGE-PRIVATE slice and `internal/adminui`
  sits under `internal/`, so it cannot be named from outside. A plugin could
  open an admin ENDPOINT — `plugins/analytics` was exactly that shape — and
  the only way to read it was curl.
  `core/plugin.AdminPage{Label, Path, Script []byte}` is now published and
  `Host.RegisterAdminPage` collects them; the panel takes them as a
  constructor argument, refuses a malformed registration AT STARTUP, and binds
  the shell, the script and the menu entry from a SINGLE list. The script is
  BYTES, not a URL, and that is what makes the policy possible: the panel
  serves it from its OWN origin, so `script-src 'self'` is enough and no
  installation's policy opens up for a third origin — including those that
  installed no plugin. The plugin ships no template, and the alternative
  ADR 0030 rejected stays rejected: a SINGLE shell owned here draws every
  registered screen, and the script fills it from /admin/v1 with the
  operator's own session. And the second half of this slice: until now the
  panel carried NO content policy at all — `Content-Security-Policy` appeared
  zero times in the tree, and neither did `X-Frame-Options` or
  `Referrer-Policy`. For a surface that draws HTML an operator is signed in
  to, that was wrong already; since ADR 0030 it was worse, because the panel's
  new screens are CLIENTS of /admin/v1, so a script the panel serves carries
  the operator's session. The policy could be strict without nonces because
  the panel had earned it BY ITS STRUCTURE, and that was measured: across
  fourteen template and style files there is exactly one `<script>` (a
  deferred src), no inline style, no event attribute, no image, no `url()`, no
  `template.HTML`. The policy was set on a SINGLE chi group that holds every
  panel route, and a gate that WALKS the router proves all twenty carry it —
  one call per handler is a rule that holds until someone adds a handler. The
  first consumer is `plugins/analytics`: its funnel screen is now in the
  panel's menu.

- **A project can now be started FROM THE BINARY** (ADR 0154, D90). gobit is a
  library and its front door was shut: nothing generated a project, so an
  author's first step was to read `cmd/server/main.go` and guess a `go.mod`,
  guess which settings exist and guess which services to bring up.
  `gobit new <dir>` now writes a project from templates embedded INSIDE the
  binary. And the fact that set the shape of the slice is this — measured, not
  argued: `github.com/bdrtr/gobit@latest` resolves to v0.8.0 and that tag does
  NOT CONTAIN the root package (the facade came after it), so with
  `require ... v0.8.0` + `import
  "github.com/bdrtr/gobit"` `go mod tidy` fails, reporting "does not contain
  package"; `@latest` fails the same way. The only version importable today is
  a commit's pseudo-version, and the generated `go.mod` writes it: the version
  the GENERATING binary was built at — the tag if it was built from a tag,
  otherwise the commit's pseudo-version. A build that can know neither does
  NOT GUESS; it refuses and points to a checkout with `-replace`. The format
  matters too: if a tag is reachable, the proxy bumps the PATCH and prefixes
  the stamp with `-0.`, otherwise it gives `v0.0.0-<zaman>-<hash>` — a binary
  that writes the wrong one names a version the proxy does NOT serve, and in
  the generated project `go mod tidy` silently turns it into something the
  generator never chose. Three template traps were closed by file NAMES, and
  all three were measured: a directory containing a `go.mod` SILENTLY drops out
  of the embed set (`all:` does not lift this, and it is exactly why the row's
  own suggestion, "embed `examples/starter`", is impossible), a template ending
  in `.go` breaks at once `go build` and the two gates that parse every
  production Go file in the tree, and `.env.tmpl` goes untracked because of the
  repository's own `.gitignore`. The generated project is proven by BUILDING
  AND RUNNING it; what that lane cannot prove is written down too: because it
  repoints go.mod at this checkout, it cannot tell "the template works at the
  version it pins" apart from "it works at the tip of the tree". The language
  gate now scans `.tmpl` — a template's prose is rendered into someone else's
  project, so Turkish left there does not stay here, it SHIPS.

- **A shop can now see WHERE its carts go** (ADR 0153). A shop's first
  question about its storefront is a ratio: how many of the carts opened
  turned into orders. The numerator has been on the bus since the order module
  existed; the DENOMINATOR was nowhere, because the cart module published
  nothing and imported `core/eventbus` ZERO times — so the module every
  shopper touches first was the module that said nothing about itself. It now
  publishes two events, `cart.created` and `cart.completed`, with the house
  pattern — the outbox row INSIDE the transaction, the direct publish AFTER
  the commit — and builds the body in ONE place (the order module builds it
  twice by hand, and nothing compares the two copies; the payment module's
  note said so, and this is the third module to follow it). Its consumer is
  `plugins/analytics`: it subscribes to three topics, writes ONE ROW PER event
  and opens the `GET /admin/v1/analytics/funnel` endpoint. Completion and
  order are kept APART, and that is the most useful thing the endpoint shows:
  the saga places the order in its SECOND step and completes the cart in its
  LAST, so an order that fails in between stands beside an uncompleted cart.
  The count is a property of the TABLE: the bus delivers at least once and the
  publishers DERIVE the event id from the record, so the row's key is the
  event's id with `ON CONFLICT DO NOTHING`; an incremented counter would turn,
  on a single redelivery, into a ratio that never happened. An event WITHOUT
  an id is not written; it is REFUSED — an empty key would grab the primary
  key and every later event would look like its repeat (three of the product
  module's topics carry no id, so this is not a hypothetical shape). The ORDER
  the row proposed was measured and rejected: with an `Analytics` interface
  added to `core/provider` plus three lines in the published-names ledger and
  no implementation written, the whole `internal/arch` lane stays GREEN — so
  that slice is a promise made to 1.0.0 for a consumer that does not exist
  (ADR 0063 rejects exactly this). The cost is plain: two more topics are
  MANDATORILY forwarded, and `cart.created` is the highest-volume topic in the
  tree — every abandoned cart is now a webhook delivery. That is why there is
  no per-LINE topic.

- **The store can now HOLD MONEY for a customer** (ADR 0152). After a late
  delivery there are two ways to keep the customer — send the money back or
  credit it to the customer's account — and this repository could only do the
  first: no table of any module held a balance, so "we credited 200 lira to
  your account" was a promise kept in a spreadsheet, not in a table. The
  payment module now has an APPEND-ONLY ledger — a signed amount per customer
  and currency — and a `store_credit` provider that spends it from the slot a
  card is spent from. The balance is the SUM of the rows and is stored
  nowhere: authorization writes a NEGATIVE hold, capture writes nothing,
  cancellation releases it — so open holds are already subtracted from what
  the customer can spend, and a correction is a new ROW. What made this a
  decision rather than just a table was the SPENDING half: money that belongs
  to a person can be spent only by that person, yet the party that named the
  payment's owner was the CLIENT — a capture carried a reference and an
  amount and named nobody. Now the capture carries the customer, and that
  customer comes from the cart (the field that has been PROVEN since
  ADR 0125), so a guest cart cannot pay with credit — the provider rejects a
  session that names nobody. The dangerous combination CANNOT BE CONFIGURED:
  on an installation with `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` on, the
  provider is never registered, so the payment method DISAPPEARS — it does
  not turn into a way to spend someone else's balance. The locking decision
  rests on a correctness argument and was proven on a real server against a
  competing transaction; the first concurrency test written also passed with
  the lock REMOVED (the local server finished every transaction before the
  next goroutine started), so a test that PRODUCES the conflict was written
  instead of one that HOPES for it.

- **The catalog now says how long it can be REUSED** (ADR 0151).
  ADR 0044 moved the sales channel into the catalog PATH — "this is what a
  shared cache can store" — and deliberately turned on no cache and chose no
  freshness policy either. The fact that sets the policy was MEASURED there:
  the catalog body can change WITHOUT A WRITE, because the price-list window
  opens against the CLOCK (`listablePrices` takes the clock as an argument).
  So invalidation on write can never be complete — when 09:00 arrives,
  nothing writes — and the only tool that can be complete is a TTL. The three
  channel-scoped reads now write
  `Cache-Control: <kapsam>, max-age=<ttl>` on the SUCCESS path; the TTL comes
  from `STOREFRONT_CATALOG_CACHE_TTL` (zero — the default — writes no header)
  and the scope is `private` unless `STOREFRONT_CATALOG_CACHE_SHARED` is on.
  The two settings answer two SEPARATE questions: the TTL is freshness,
  `shared` is SECURITY — since ADR 0044 the publishable key has been a GATE
  that does not affect the body, so `public` lets a CDN serve the stored body
  to a caller WITHOUT A KEY. Most stores want exactly that (the channel's
  catalog is what the storefront shows the world, and the key sits in the
  browser anyway), but it is not a decision this repository makes on their
  behalf: the default is false and a shared installation gets a WARNING at
  startup. The header is written per handler and on the success path; a
  middleware would have to know the route table a second time and CANNOT SEE
  whether the handler will answer with a body or a rejection — a 404 that the
  CDN stores for the TTL means a product that stays missing even after it is
  fixed. A negative TTL stops startup, zero is accepted: zero is an answer,
  while `-1h` is a typo its author believes does what it says. One of five
  mutations survived, and the defect was in the TEST, not the CODE: the
  validator rejected `-1h` and no test held it to that.

- **A program that EMBEDS gobit can now stand it up in its own test**
  (ADR 0150). gobit is a LIBRARY (ADR 0025), yet a program embedding it had
  no way to write tests against it: the facade offers only `Main(args, out)` —
  it binds a port and blocks — and everything behind it (migrations, module
  registration, the router, the protection rings) is under `internal/`,
  unreachable from outside. The two remaining options were to run the binary
  and talk to the socket, or to REWRITE the assembly in one's own test; this
  repository has a record of what the second costs (ADR 0141 exists precisely
  because that copy drifted). Now `App.InProcess(ctx)` stands up the whole
  installation and returns the `http.Handler` that `Main` would serve — and
  it goes through the SAME assembly function, because a harness with its own
  assembly would be a second answer to "what is the installation", and the
  answer the tests trusted would be the one nobody deploys. Nothing with a
  PORT or a CLOCK starts: no HTTP server, no operator listeners, no SCHEDULED
  JOBS — a relay ticking underneath the test's own assertions makes a failure
  depend on when the test happens to look. The cost is written down: an
  event subscriber is reached only by a DIRECT publish; the relay that keeps
  the promise an outbox row makes does not run. Configuration is read from
  the ENVIRONMENT, just as `Main` reads it (a second configuration path would
  mean a test running with defaults no deployment uses), and the cost of that
  is that such a test cannot be `t.Parallel`. Widening the facade also found
  a hole in the gate that audits the published surface (D86): the name
  inventory walked only the `core/` tree, while the package list declares the
  facade published too — so `gobit.App` and its methods were promises to be
  kept until 1.0.0 that nothing audited, and the gate stayed green when
  `InProcess` was added.

- **A carrier can now be asked WHERE a parcel is** (ADR 0149). A tracking
  number could be attached at shipment time and the timeline carried five
  moments, so the MANUAL half was complete; the PROVIDER half was missing —
  the shipping contract was three methods (`Quote`, `Create`, `Cancel`) and
  asked the carrier nothing once the label was printed. The gap was named by
  the bundled provider's own godoc: `GetShipment` "is NOT part of the core
  contract… only this way can a fault in which the two ledgers diverge be
  seen". The two ledgers are deliberately separate tables, and they really do
  diverge: when the shipment is marked, the module says `shipped` while the
  provider's row stays `pending`, and the tracking number the operator typed
  sits next to the number the label was opened with — so a parcel recorded
  with the wrong number is now VISIBLE. `core/provider` now publishes an
  OPTIONAL `ShipmentTracker` (`Track`, a READ, by the provider's own ID), and
  `GET /admin/v1/fulfillments/{id}/tracking` returns the carrier's view NEXT
  TO the module's record, WRITING nothing: which side is authoritative
  depends on the provider — a real carrier knows where the parcel is, while
  the bundled provider is the store itself, where the operator's record is
  the right one — and writing would mean picking the wrong side in one of the
  two cases. The answer has FIVE shapes and the client looks at the NAME, not
  at emptiness: a carrier that says "pending" and a carrier that cannot be
  asked produce the same empty fields. Seven mutations bit, one survived, and
  the defect was in the TEST, not the CODE: in none of the fixtures was the
  module side empty, so the two numbers were already different — the test
  that closed it is a parcel with both sides empty.

- **A rule can now ask what a product BELONGS TO** (ADR 0148). ADR 0144
  brought the set-reading operator (`any_in`) and gave the set to the context
  side; it could not give it to the row side, because a product's categories
  and tags are not columns of the product row and nothing published them. So
  the operator existed, but half of its questions could not be asked: a
  merchant could write "20% off this product" and "20% off this collection",
  but not the campaign stores actually run — a discount on a CATEGORY. The
  rule row was saved, the admin endpoint returned 200, and the discount
  silently stayed at zero. The product record now publishes `category_ids`
  and `tag_ids`, and the cart sends each line's lists. The reads happen ONLY
  when the field is named: the panel's grid, link resolution and the tax-type
  read pay nothing — but an EMPTY field selection (meaning "the whole
  record") does pay, because the whole record has to be correct. Membership
  is DIRECT, which is the same answer the provider's `category_id` FILTER
  gives: a rule naming a parent category does not reach products filed only
  under its subcategories, and that limit sits in known-limits next to the
  filter's. A shipping method carries NO list and never will — it is in no
  category — so a category rule targeting shipping selects nothing, which is
  the right answer. And in this round a rule that had stayed in prose was
  turned into a gate: the promotion module answers the same computation on
  TWO surfaces (the cart's interop and `POST /admin/v1/promotions/compute`),
  and both godocs said the shapes had to stay EXACTLY the same — the field
  was added to one and not the other, and nothing failed (D85).

- **The second factor is now DEMANDED** (ADR 0147). ADR 0143 gave an
  administrator a place to HOLD a second factor — a sealed TOTP secret, two
  endpoints, RFC 6238 — but the only caller of the method that asks "has this
  person proven their phone" was its own test: a person enrolled and
  confirmed, then logged in with their password as if nothing had happened.
  This is the repository's own recurring defect, and here it is worse — what
  the feature claims to do is protect administration, and the installation
  that turned it on believed it was protected. `Login` now asks for the
  account's proven factor before signing the token; the code arrives in the
  login body and the rejections NAME THEMSELVES (`auth_mfa_required`,
  `auth_mfa_code_wrong`), both only after the password turns out CORRECT, so
  they tell a stranger nothing about the account. A wrong code counts as an
  attempt (that counter is the only limit on guessing six digits); a missing
  code does not (it is the first half of an ordinary two-step login). The
  three questions ADR 0143 left open were answered by SHAPE: machines are
  unaffected (the demand sits on password login, and a key has no
  authenticator), an unproven enrolment demands nothing (an abandoned scan
  locks nobody out), and re-enrolment no longer DELETES the confirmation —
  the new secret waits in `pending_secret` NEXT TO the proven one, because
  deleting would be a way out of the second factor that requires no secret at
  all. Someone who loses their phone can no longer fix it on their own, and
  no endpoint fixes it for them: an administrator who could remove a
  colleague's factor would make a SINGLE stolen session enough to enter every
  account with a password. The remaining path is on the machine:
  `gobit mfa-reset <email> -confirm <email>`. Nothing is mandatory store-wide
  (a mandate before anyone has enrolled would lock everyone out at once), and
  this was written into known-limits. And widening `Login` broke
  `adminui.Session` while staying GREEN in every lane that compiles: the
  panel resolves five surfaces by name and none of them was pinned — the
  failure was waiting at STARTUP; all five are now pinned.

- **An operator can now take an order OVER THE PHONE** (ADR 0146). The
  cart's admin surface was read-only by decision: a correction made from the
  panel meant changing, behind the customer's back, the amount the customer
  was looking at. That reasoning covers CHANGING a cart, not OPENING one —
  and the order module cannot fill the gap, because `CreateOrder`
  deliberately has no route: an order opened over HTTP would carry a total
  the caller decided. What makes the amount the server's is the cart. The
  surface gained exactly two writes — opening a cart and adding a PRICED line
  — and the line write carries a MANDATORY `sales_channel_id`: an admin key
  carries no channel, and an identity without a channel means not
  "channel-less" but "bound to no channel", so the request would be rejected,
  for every variant in the catalog, with a message about the variant ID the
  operator typed. The handler WRITES the channel onto the principal, so the
  cart's existing scope rule runs as it is instead of being bypassed. Opening
  a cart does NOT ASK for a channel: nothing reads the channel on that path,
  and asking for one would be a claim written into a context nobody looks at.
  The cost is written down: the server has not proven the scope, the claim is
  made separately on each request (two lines of one cart can be written under
  two channels), and the operator can add lines to a cart the customer is
  holding — but cannot do anything that CHANGES what the customer sees, and
  the customer still pays, from the store surface, against the total in front
  of them.

- **An exchange can now send A DIFFERENT PRODUCT** (ADR 0145). Every
  after-sales item in the module pointed at an existing order line through a
  NOT NULL foreign key; that is right for records about goods the customer
  already owns, but not for an EXCHANGE. "Send the same shirt one size up" is
  an ordinary exchange, yet `order_replacement_items` could express only units
  of a variant already on the order — and the money half of the exchange has
  been able to collect a difference since ADR 0120, with no goods for it to
  answer for. An item can now name a variant INSTEAD OF a line (a CHECK in
  the schema: exactly one of the two). Nothing changed downstream, and that
  is not luck but the measurement's finding: the shipment flow already worked
  from the variant alone; the line was there because the item itself could
  not say what it was sending. The cost is written down: a line item is
  bounded by "no more than was bought", while a variant item is bounded only
  by the difference amount the operator WRITES — this was added to
  known-limits.

- **A rule can now ask "is it in ANY of these groups"**
  (ADR 0144). A customer is in as many groups as the merchant puts them in,
  but the cart could send only ONE: the ranked head (ADR 0049). So a customer
  in the {retail, vip} groups whose head was retail DID NOT MATCH the rule
  `customer_group_id in [vip]` — a segment discount silently not applied to
  someone INSIDE the segment, which is exactly the defect ADR 0103 opens with.
  A ninth operator (`any_in`) reads the context's value SET, and the cart
  sends all the groups ALONGSIDE the ranked head. The old operators do NOT
  LOOK at the list: the answer of an already shipped `in` rule has to stay the
  same, or a live discount would widen without announcing anything. The
  measurement also found that all three targets the entry called "missing"
  were wired, and that a sentence in `cart/discount.go` ("the customer group
  is NOT PUT into the context") had long been wrong.

- **An administrator can now carry a SECOND FACTOR** (ADR 0143). The
  `auth_mfa_credential` migration, and two endpoints under `/admin/v1/auth/mfa`:
  enrolment and confirmation. The endpoints name no user — they act on the
  caller ITSELF, because an administrator who could open an enrolment on a
  colleague's behalf would hold the secret of that colleague's phone; a
  request made with an api_key is rejected too, since a machine has no
  authenticator. An enrolment does NOT COUNT until the first correct code.

  This is the FIRST secret the module can read back: the password is
  argon2id, the api key and the invitation token are SHA-256, none of them
  retrievable — but verifying six digits means recomputing them. The secret
  is sealed with AES-GCM and the key is supplied by the installation
  (`MFA_SECRET_KEY`); without a key, enrolment is REJECTED, because a default
  key is not a key, and plaintext is a security feature that silently does
  less than its name says. The key is SEPARATE from `JWT_SECRET`: the two are
  rotated at different times. TOTP arrived WRITTEN in-house, not as a
  dependency, and is verified against RFC 6238's own test vectors. The login
  flow is not touched yet — making it mandatory is a separate decision.

- **Both actions compute the SAME TARGET** (ADR 0142, D82). Two actions put a
  line's canceled units back on the shelf: the cancellation write itself and
  the cancellation of the parcel holding the rest. Both computed a
  DIFFERENCE, and the parcel action subtracted the term "what the write
  already put back" — a number it did not read but ASSUMED. The bus does not
  promise order: a write whose direct publish is lost arrives a minute later
  through the outbox relay, AFTER the operator has canceled the parcel.
  Measured: EIGHT units were written to the shelf for a five-unit
  cancellation, and because the two actions' references differed, the
  ledger's uniqueness could not see it. Both now compute the target
  `min(canceled, sold − in parcels)` and the module moves the difference under
  the lock; order no longer matters, and a redelivered event finds the target
  already met. `inventory_movements` gained a column carrying the line ID,
  and the reference's unique index was DROPPED — the same action
  legitimately writes a second time when the target grows.

- **The end-to-end harness wires every flow production wires** (ADR 0141).
  `internal/e2e` builds its module and flow set BY HAND — deliberately,
  because a harness that called the real installation would test the
  installation, not the modules. The cost of that copy was being a copy:
  production wired seven flows, the harness six. The missing one was the
  repository's only flow driven SOLELY by the bus — nothing resolves it,
  nothing calls it, so leaving it unwired breaks no request and turns no test
  red; only the stock figure comes up short. D75 and D76 sat exactly there for
  weeks. The gate now compares the flow packages the two roots IMPORT, and
  the harness runs the first scenario that sees ADR 0134/0135/0139/0140 at
  once.

- **A parcel now RECORDS which order it was opened for** (ADR 0140). There
  are two ways to open a parcel and neither did both: a parcel the flow
  opened was linked but carried no items (the surface it opens through takes
  no items), and a parcel the module's admin endpoint opened carried items
  but was linked to nothing. Measured against a real database: a three-unit
  parcel says `{oli: 3}` to `CommittedQuantities` and EMPTY to the link list.
  So the `committed` term was structurally zero for every parcel that
  contributed to it — and that term sits in the middle of THREE decisions
  (ADR 0134, 0135, 0139). The link is now written by the module that owns the
  definition; the write in the flow was removed, because having the same rule
  in two places was the reason it was forgotten in one.

- **A canceled parcel gives back the units it held** (ADR 0139). How many of
  a line's units belong to the shelf is `min(canceled, sold − in live parcels)`,
  and BOTH sides of that expression move; but only one of them had an event.
  A line canceled under an open parcel put back the units outside the box and
  correctly left the rest — ADR 0135 looked at exactly that case, said "the
  framework does not pull back anyone's shipment on its own", and named the
  shop's remedy in the same sentence: cancel the parcel. Canceling the parcel
  flipped a status, touched no stock and told NOBODY; the fulfillment module
  had never published an event. So those units were neither in a parcel, nor
  owed to the customer, nor on the shelf. The module now publishes
  `fulfillment.canceled` and the cancellation flow listens to it as its
  second event: the amount it releases is the DIFFERENCE between the two
  windows — because it is a difference of states, the flow does not need to
  remember what it returned before, and in whichever order two parcels are
  canceled, the total is the same.

- **The container reaper is off where the machine gets thrown away**
  (ADR 0138). On 11 September the verification lane turned red twice, in two
  unrelated packages, after waiting sixty seconds each time; what it was
  waiting for was not the Postgres the test asked for but the Ryuk container
  SHARED between processes — it terminates itself ten seconds after its last
  client leaves, is still caught by the label search until its record is
  deleted, and sixty seconds are spent waiting for a port a dead container
  will never publish. Since the GitHub runner is destroyed when the job ends,
  the reaper has nothing to protect: it was turned off in two jobs, and the
  claim that justifies the decision — that the runner is ephemeral — was
  nailed down in both directions by an arch gate.

- **A colleague now sets THEIR OWN first password** (ADR 0137). Until now
  there were two ways to add a user and both were wrong: either you wrote the
  password into `CreateUser` — so one person knew another person's secret,
  and the record of "who can act as this user" was wrong from the first
  minute — or you created the user without a password and never wrote an
  `auth_identity` row, which yields an account that cannot log in and whose
  reason nothing states. Now there are invitations:
  `POST /admin/v1/users/{id}/invitations` opens one,
  `POST /admin/v1/auth/accept-invitation` spends it. The token is NOT in the
  response — if the API handed the invitation back, the administrator would
  still be holding the colleague's first-password link. The accept endpoint
  is the second unprotected admin path, and it has to be: the person calling
  it does not yet have an account to authenticate with. **And what made this
  possible**: the notification module now has a cross-module surface — until
  then the only way to send mail from inside gobit was an EVENT, and an event
  sits in the durable stream, is delivered at least once and is FORWARDED to
  the operator's third-party endpoints; a single-use invitation token can be
  none of those.

- **The compiler now checks EVERY interop pair** (ADR 0136). A consumer
  defines the narrow interface it needs in its OWN package and resolves the
  concrete value from the container by name; since neither side imports the
  other, a signature drift compiles on both sides and blows up only at
  RESOLUTION time — on the request path, cached with `sync.Once`, while
  startup is green. And it had blown up: `*cart.Interop` carried neither
  `ApplyPromotionCode` nor `RemovePromotionCode`, so the two storefront
  coupon endpoints returned 500 to the first customer who typed a code (D73).
  What kept this from being written was a sentence in another module's
  godoc: "the compiler never sees the two together" — wrong; a THIRD package
  in the same Go module can import both. `internal/arch/interop_pins_test.go`
  now carries thirty-seven assignments, and a gate that derives the
  population from disk keeps the list complete (on its first run it found two
  missing from the list I had written by hand).

- **A parcel can no longer carry more than the order OWES**
  (ADR 0135). `POST /admin/v1/fulfillments` took line IDs and quantities and
  checked none of them against the order — verified by reading: an empty ID,
  a global quantity range and the same line appearing twice are rejected,
  nothing else. Not whether the line belongs to that order, not whether the
  quantity stays within what was sold, not whether the units are in another
  parcel, nor whether they have been canceled. So an operator could ship goods
  the customer had been told were canceled — and since ADR 0134 returned the
  stock of those units to the shelf, the same goods left twice and the count
  fell short by the difference. The fulfillment module now resolves the
  `fulfilling` flow at request time, asks for the bound
  `received − canceled − in live parcels` and rejects an item that exceeds it. The
  endpoint itself DOES NOT CHANGE, and if the bound cannot be read the parcel
  is not opened — a bound that cannot be read is no bound (D72).

- **The known-limits document now COVERS the contrib identity modules too**
  (D71). `docs/known-limits.md` is the document people open to read what
  gobit does not do, and its identity section ends the way to close the four
  rejecting routes with "one line of wiring: wire a verifier". Since ADR 0127
  there has been ONE to wire — in this repository — and the word `contrib`
  appeared nowhere in the file. So a reader following the document's own
  advice learned neither that it existed nor what it does not close: a
  signed cookie cannot be revoked before it expires, a stolen cookie can
  enroll its own passkey and remove the owner's key, a credential store the
  installation wires in may answer none of the data-subject capabilities, a
  change of `Options.RPID` abandons every enrolled key, and the default rate
  limit on sign-up is per PROCESS. All of it was written in an ADR; none of
  it was where someone looks for limits.

- **Canceled units now return to the SHELF** (ADR 0134). Checkout's last step
  confirms the reservations, so stock is DECREMENTED — the units of an
  existing order have therefore left the sellable count, and a line deleted
  afterwards is a unit that nobody will ship and that is not counted as stock
  either. Nothing put it back: not full order cancellation, not ADR 0113's
  partial cancellation, and the order module cannot (the units live in
  another module). Now order publishes `order.line_canceled` (outbox +
  direct) and a NEW flow subscribes: it asks fulfillment how many units the
  live parcel holds and puts back the increase in
  `min(total canceled, received − in parcels)` — so a second cancellation does not
  double-count and a shipped unit does not return to the shelf. It is the
  repository's FIRST listen-only flow. Restocking is IDEMPOTENT for the first
  time: the bus delivers at least once, so the cancellation ID is the
  movement's reference and the ledger keeps it unique (D70).

- **A customer can now open THEIR OWN account** (ADR 0133).
  `contrib/identity-session` only logged people in and let an operator write
  a credential; a customer could not open an account. Two endpoints were
  added: sign-up and verification. Sign-up creates NOTHING about the person —
  no customer, no credential, no session; only a row in this module's own
  table holding the address, the argon2id hash of the password and the hash
  of the token. The SAME 202 is returned for an address that already has an
  account, otherwise the question "does this person shop here" would be
  answered for anyone; what differs is the message sent. The token is
  consumed with `DELETE ... RETURNING`, so being single-use needs no lock, and
  it is spent BEFORE the account is opened. Who creates the customer record is
  a seam the installation wires: `customer.service`'s `RegisterGuestCustomer`
  says "the same e-mail is no obstacle", which is the wrong semantics for
  sign-up. The endpoints DO NOT EXIST unless the seam is wired — a typed nil
  counts as unwired too.

- **The two contrib identity modules now answer a DATA SUBJECT**
  (ADR 0132). `contrib/identity-session` and `contrib/identity-passkey`
  implemented none of ADR
  0029's three data-subject capabilities — yet between them they held an
  e-mail address, an argon2id password hash, a customer ID, a per-device
  credential and four timestamps. So a store could fulfil an erasure request
  and leave in place the credentials that let the erased person in. The audit
  written to catch this could not see them either: it walked only under
  `plugins/`, and a separate go.mod does not make a table any less personal —
  the roots are now verified against DISK. Passkey erasure is deliberately
  OUTSIDE the RP scope: the other operations answer "which keys let this
  person in", while this one answers "what is held about them". The password
  hash is declared but its value is not produced — dropping the column would
  make the answer wrong, and printing its value would put the person's own
  secret into the file (D69).

- **A passkey now belongs to a SINGLE relying party** (ADR 0131). The
  authenticator that generates a passkey binds it to an RP ID: an
  installation whose `Options.RPID` changes — a domain move, or a subdomain
  being dropped — leaves every enrolled key unusable. The rows stayed, and
  nothing recorded which party they belonged to, so the "protect the last
  login path" rule shipped one commit earlier COUNTED them: a person holding
  one abandoned key and one new key was told "you have two paths" and was
  allowed to remove the NEW one — the protection produced the very lockout it
  was written to prevent. The server had no view of this either, and that
  half was measured: a row enrolled under `example.test` let someone in under
  `moved.test` with a 204. Now `rp_id` is a column and every read and write of
  the store is scoped by it; NULL means a row from before the column existed
  and is read as the configured party — so the upgrade locks nobody out (D68).

- **A person can now SEE their passkeys and remove one**
  (ADR 0130). `contrib/identity-passkey` offered only enrolment and login:
  someone who lost their phone could not see what opened their account, nor
  revoke that device. Two endpoints were added — a list of the caller's own
  keys and removal of one. Removal is rejected if it would leave the account
  with no way to log in, and that rule is not a CONDITION but a LOCK: the same
  check written inside the DELETE under READ COMMITTED leaves zero keys when
  two removals run concurrently — measured, on every run. The question "is
  there another login path" is this module's OWN question, and it is asked
  before the transaction opens: querying another module while holding a row
  lock needs a second connection from the same pool. "We could not check" is
  never "you have no other path" — one is a 500, the other a 409.

- **The signing key can now be ROTATED without locking anyone out**
  (ADR 0129). `contrib/identity-session` signed and verified with a single
  key; changing it meant every cookie in every browser became unverifiable at
  once. So the price of a rotation was every shopper's session — which is the
  reason keys do not get rotated, not a reason they should not be. The
  module's own package doc said so in one sentence; a limit that is written
  down is a limit that can be closed.
  `identitysession.Options.RetiredSecrets` holds keys the cookie may still
  carry but that nothing SIGNS with any more. The order is exactly the
  feature itself: an implementation that accepts both but keeps signing with
  the OLD one passes every test that says "sessions work" and has rotated
  nothing. A LEAKED key is not retired, it is thrown out outright — that locks
  everyone out, and that is the right price.

- **Passkeys are in THEIR OWN module** (ADR 0128). `contrib/identity-passkey`
  performs both WebAuthn ceremonies, keeps its own credential table and puts
  the person into the SAME session cookie the password opens. A separate
  `go.mod`, because it was measured: importing go-webauthn adds nine modules
  that are NOT in gobit's graph — including `go-tpm` and `go-tpm-tools`, that
  is, attestation support for hardware most shops will never see. An
  installation that imports `contrib/identity-session` for passwords should
  not carry that. The ceremony state is a short-lived cookie sealed with the
  session module's key; this made that module's MAC also take in "what this
  signature is FOR" — one key signing two shapes makes them interchangeable.
  Login NAMES nobody: the authenticator asks the person which of their keys
  to use, which is both the better flow and the only flow WITHOUT account
  enumeration. The ceremonies RUN against a real software authenticator,
  because every mistake this module can make is about a challenge, an origin
  or a user handle, and no assertion about the handler sees any of those.

- **A working customer identity is now IN THE TREE — but OUTSIDE the module**
  (ADR 0127). `contrib/identity-session`: a signed cookie session, argon2id
  passwords, its own table and two storefront endpoints; the embedder imports
  it and `Add`s it. ADR 0125 closed every storefront endpoint that names a
  customer until a verifier is wired, and ADR 0126 published the rules — but
  there was nothing to wire: every implementation here was a test fake. Where
  it lives was the DECISION: `plugins/*` is in the main module, so a WebAuthn
  library entering there would land in the graph, the security scan and the
  legal review of a shop that wants a product catalog — the dependency gate's
  own sentence. A separate `go.mod` keeps it out. The first slice adds no
  dependency for anyone: argon2id needs `golang.org/x/crypto`, and gobit
  already requires it DIRECTLY. Passkeys were left for a record of their own.
  Four gates and two lanes learned the new tree and each of them found
  something — the sharpest find was twenty-eight tests that nothing ran.

- **A customer identity now passes a PUBLISHED suite** (ADR 0126).
  `corehttp.Identity` is the one interface this framework requires and does
  not implement, and nothing checked what the embedder wrote —
  `docs/known-limits.md` had said so in one sentence for two records now: an
  implementation that hands back the claimed identity satisfies the interface
  and the framework cannot tell the difference. ADR 0125 turned this into a
  live question: every storefront endpoint that names a customer now rejects
  until a verifier is wired. The obvious rule, however, DOES NOT WORK — the
  interface's own contract counts "a header written by an upstream proxy" as
  a legitimate source, and rightly so: behind a filtering gateway that header
  is proof. What separates the two cases is not the header but whether
  something filters it — and no test holding the request can see the
  gateway. The solution: the implementation DECLARES it
  (`identitytest.UpstreamTrust`) and the suite does not probe that header.
  The two implementations in the suite's own tests are the same code: the one
  that declares passes, while the one that does not fails with an error that
  names the interface to implement BY NAME.

- **Serving an unverified customer claim is now a CHOICE** (ADR 0125).
  ADR 0057 tied twelve storefront endpoints that name a customer to a single
  comparison and deliberately chose to let four of them serve the claim
  UNCHECKED when no verifier is wired — and its reasoning was not wrong
  either: rejecting pulls a working surface away from an embedder who has
  done nothing wrong. The residue was written down plainly and put in
  `docs/known-limits.md`: someone who knew a customer ID (that ID travels in
  every order response) could read that person's company and spending limit
  and open carts in their name — the cart half spends that person's B2B
  allowance. What that record could not do was turn this into a DECISION: an
  installation got the open answer without knowing the question existed, and
  a WARN at startup is not a choice. All four now reject by default — as the
  address book already did — and the old answer is one setting away
  (`STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM`). What is withdrawn is not
  the surface but getting it WITHOUT DECIDING. The default is also the ZERO
  VALUE: the field was named in the positive, because `internal/e2e` imitates
  the composition root with zero Options, and so does every embedder that
  builds the modules by hand. Guest traffic is the same under both values — a
  body that names nobody is never questioned, which is the sentence ADR 0057
  built the whole comparison on.

- **Shipment now ASKS whether the money is STILL THERE** (ADR 0124).
  ADR 0120 let the exchange collect its difference, and the row holds the
  MOMENT the money was there — the only thing an order row can hold about a
  figure that payment owns (ADR 0119). But a MOMENT is not a BALANCE: the
  collection stays reachable from payment's own refund route, and there is no
  flow on that path. Measured: fund, refund the collection, ship — it
  returned 200, a real parcel was opened, the units came off the shelf and
  the exchange was marked `completed`, with nothing in the collection (D61).
  The defect is not a missing rule but a rule asked ONCE about something that
  CHANGES. The flow now asks before stock moves — where a rejection still
  costs nothing — and once more before closing the record, because that step
  also runs on the retry path.

- **A schema name cited in live code now RESOLVES** (ADR 0123). Four of D59's
  ten stale sentences named a CHECK that ADR 0120 had dropped: they gave the
  reader a name to verify the claim with, and the name resolved to nothing.
  This is the class `doc_references_test.go` was written for — a citation
  sends the reader off to SEARCH and what they search for does not exist —
  but in a dimension that test cannot reach, because a constraint name is
  neither a Go symbol nor a path but a word in a comment. The audited
  vocabulary is derived from the migrations THEMSELVES: every name the schema
  ever defined. The migrations are walked IN ORDER, and the order is the
  whole point — a name is routinely dropped and re-added a line later; that
  is how a CHECK gets widened.

- **Generated code is now verified by REGENERATING it** (ADR 0122). The
  repository keeps 75 sqlc files and 14 gqlgen files in the tree, and nothing
  compared them with their sources. The defect surfaced through a comment
  (D60), but the measurement found something worse: the source `.sql` is NOT
  the query the database runs — what runs is the string in the generated file
  — so an edit to the source alone is executed by nothing. Multiplying the
  B2B spending window a thousandfold in the source leaves `go build`,
  `go vet`, the unit lane and the integration lane against real PostgreSQL
  clean; all of them run the OLD query, which is the correct thing for them
  to do. The edit is invisible precisely when it is wrong. CI now runs the
  generators and asks for the diff — the question it already asks for go.mod.

- **The payment module now SAYS when money moves** (ADR 0121).
  The order's summary is a REPORT of what payment holds, and only two flows
  wrote it. Payment, however, publishes its capture and refund routes itself,
  and there is no flow on those paths — so money moved and the order's record
  never found out. ADR 0022 had seen this three days earlier, named the
  subscriber "the better home" and rejected it for a single reason: payment
  published nothing. In the same sentence it left the question that had to
  be answered first — **what does a payment event carry?**
  The answer: the collection's ID and the moment, NOT the amount. There are
  three reasons and all three were measured. Refund is deliberately not
  idempotent, so an amount in the payload would be an INCREMENT, and the bus
  delivers at least once — a redelivered increment reports a total that never
  happened. The consumer's write, for its part, is a merge that works
  correctly only on a CUMULATIVE number. And every published topic is
  forwarded outside the installation: money in the payload would go to the
  third-party endpoints the operator has registered. The subscriber reaches
  the order in REVERSE through the `order_payment` link, because the
  collection's `reference` carries the CART ID — the published OpenAPI
  description wrongly described it as "an order id in practice", and that was
  fixed too. ADR 0119 does not bend: what is forbidden is an order row
  holding payment's figure as ITS OWN TRUTH, not holding a report — and the
  subscriber produces the report by asking at the MOMENT IT WRITES. Defects:
  D55 closed, and its sibling D57 was opened and closed in the same commit —
  the capture route carried the same silence, and D55 had missed it because
  it was written from the route that had been looked at only.

- **An exchange can now COLLECT its difference** (ADR 0120). When migration
  000008 removed exchange completion, it wrote down by name what would bring
  it back: goods going out and, if the difference is not zero, money coming
  in. ADR 0090 brought the goods; the money took three records — 0117 kept
  the sale's link as it was, 0118 turned the collection's gate into remaining
  capacity, and 0119 decided that the order row may NOT MIRROR payment's
  amount. One question was left: where to put the withdrawal guard. It was
  measured, and none of the FIVE shapes that guard withdrawal survived —
  reading the money there needs a module this module cannot ask, and reading
  the counter produces a record that cannot be undone and an order that can
  never be forgotten again.
  The answer moved the question: **the guard sits not at withdrawal but at
  FUNDING.** The moment the exchange takes the money it leaves `requested`,
  and the transition table itself says what that means — the same as the
  return's `received → conflict` row.
  Decision: a positive difference is funded by NAMING the collection in which
  the operator gathered the money; the row holds that collection's ID and
  the MOMENT, never its amount. The goods of a funded exchange can complete
  it, ordinary withdrawal rejects it, and its way out is a SINGLE action that
  sends the money back and withdraws the request as well.
  The costs are stated openly: the status vocabulary went from three to four
  and was published. The completion bound STAYED in the schema, and that is
  possible only because the row holds an ID — a CHECK sees a column, it does
  not see a link written in the link layer. This overrides the second
  sentence of ADR 0117: that record said "link" because that was the visible
  shape; the tree's own shape for a cross-module ID written by the flow that
  holds both sides is a COLUMN (migration 000012 wrote this down with its
  reasoning).
  And I closed the hole the new status would have opened: the erasure sweep
  looked for `requested`, and `funded` would not have triggered it — so an
  order whose money is being held would have become FORGETTABLE. The branch
  reads both.

- **An order does NOT KEEP a second copy of the money** (ADR 0119). The
  exchange's difference was designed three times, six candidates were
  produced, and all six were knocked down by independent readings. They
  differed on almost everything and died of one thing: each put a MONEY
  number on the exchange's row and tied the rule to a row-local CHECK — the
  shape migration 000017 defended when it bounded completion.
  That reasoning does not carry over to money another module owns. Measured:
  payment PUBLISHES a route that refunds a capture and publishes no event at
  all (zero `Publish`, zero topics, zero subscribers), and a constraint on
  `order_exchanges` CANNOT SEE a row written to `payment_collections`. So a
  copy on the order side is a claim that a published route can silently
  invalidate, and the strongest guard the schema offers is exactly the guard
  that cannot notice it.
  Decision: an amount that payment owns is NOT MIRRORED onto an order row; if
  that amount decides something, payment is ASKED at the moment of the
  decision. What an order row can record is the MOMENT A QUESTION WAS
  ANSWERED, not the arithmetic of the answer.
  The cost is paid openly: 000017's row-local bound is given up for this fact
  (that bound was justified by the fact being on the row; this fact is not on
  the row), and because there is no cross-module transaction between the
  measurement and the write, a window remains that does not close — a
  completion declares the MOMENT it was written, not every moment after.
  The tree already holds such a copy, and it now has a name:
  `order_summaries`' totals are a REPORT, not a source. That surface's godoc
  justified its own merge semantics with "a subscriber listening to payment
  events", and such a subscriber CANNOT EXIST; the sentence was fixed, the
  staleness was not — its trigger is ADR 0022's own trigger. Defect D55.
  What the three rounds settled is handed over to the next record (the link
  name and its one-to-one cardinality, the positive-only predicate, EQUALITY
  rather than the floor as the measure, the structural ceiling, a `down` that
  rewrites, adding a method instead of widening a signature), together with
  the one question deliberately left open: the withdrawal guard.

- **A collection's gate now reads what REMAINS** (ADR 0118). The gate asked
  "has this collection ever received anything", and the computation beneath
  it never read what had been captured — it only subtracted the live
  sessions' reserve. So the flag was not a shortcut but the ONLY wall between
  a second session and a second capture; that is why it was crude, and that
  same crudeness made the remainder of a partial capture uncollectable
  forever.
  A partial capture is not a corner case: the admin capture endpoint takes
  the amount as OPTIONAL and bounds it only from above, the same place is
  reached without the operator choosing anything when a provider authorizes
  partially, and the derived status vocabulary has recognized that state by
  name since the day it was written. The module was also breaking its own
  word: the session test says "a canceled session must not lock the
  collection forever, the customer must be able to try another payment
  method", and that held only as long as nothing had been captured.
  Decision: opening a session asks for the REMAINDER — the amount minus what
  was captured minus the live sessions' reserve — and rejects only when that
  is zero. The barrier against double capture moves from a flag to
  arithmetic, and moreover to where the second wall already stands: the
  `captured_amount <= amount` constraint could until now fire only AFTER the
  provider had TAKEN the money.
  A fully refunded collection is NOT REOPENED, and this is deliberate: a
  refund does not shrink what was captured, so the remaining capacity stays
  zero. That is ADR 0117's trigger, but it has no consumer — both refund
  callers in production only send money back (ADR 0063). A correction on the
  side: ADR 0117 said "the obstacle is the collection, not the link"; the
  measurement gave a narrower answer — even if the existing collection were
  reusable it CANNOT take the difference, because `amount` is never written
  again. The difference needs a SECOND collection, and its name is still
  deferred. Defects D53 and D54.

- **The sale's payment link carries only the sale** (ADR 0117). ADR 0116 made
  a cardinality widenable and left a single question open in writing: does
  the exchange's capture need a name of its own. Until then the answer was
  forced — widening `order_payment` stopped startup — and now it no longer
  is. Measured: what refuses is not the mechanism but the ROW. A link row
  carries two IDs and a moment, no read statement SELECTs that moment, and
  under a widened `order_payment` no data would remain to tell the capture
  checkout opened apart from a capture covering an exchange — all three
  readers take the first one they get. The most expensive is the refund
  flow: its comment is not a description but a RATIONALE, saying
  "one-to-one, so more than one is not a choice but a data error", and
  widening would leave the behavior the same while making the rationale a
  lie.
  Decision: `order_payment` stays one-to-one; money collected against an
  order for another reason will be tied to a link of its own, which carries
  BY NAME the distinction the row cannot carry. That link's name and ends are
  NOT DECIDED here — it has no consumer, and this repository does not publish
  a name nobody reads.
  The same round fixed the read surfaces ADR 0114 had left behind (D52): the
  admin exchange record gained the MOMENT its status already published, and
  the timeline reports both of an exchange's endings. Both were proven by
  mutation.
  An exchange with a difference still does not close, and the measured
  obstacle is not the link but the collection: a capture collection cannot be
  abandoned once it has received something. The trigger is written down.

- **A link's cardinality can now WIDEN** (`core/link`, ADR 0116).
  Three places in the tree named the same step — the limit ADR 0114 left
  open, the payment module's link definition ("that day this becomes
  OneToMany and **nothing else changes**") and the capability list. That
  sentence was wrong, and wrong in the harshest way: `core/link` writes every
  definition into a durable ledger and compares the incoming definition by
  EQUALITY, so a changed cardinality is a conflict at startup — the
  application would never have started. The schema half had the same shape:
  because the DDL is `IF NOT
  EXISTS` from top to bottom, a looser declaration created nothing and
  REMOVED nothing; the unique index built under the old cardinality would
  survive and keep enforcing the old one.
  Decision: a declaration whose two ends are unchanged and whose cardinality
  is WIDER than the stored one is applied — the ledger row is migrated, and
  the indexes the new cardinality does not want are dropped in the same
  transaction, under the lock the declaration already holds.
  The safety argument is one sentence: every pair a narrow cardinality
  accepts is accepted by the wide one too, so the rows on disk satisfy the
  new constraint by having satisfied the old one — widening reads no data to
  know it is safe. NARROWING has to read and continues to be rejected.
  The costs: a release that widens a link CANNOT BE ROLLED BACK this way (the
  old binary declares the narrow cardinality and is rejected at startup), and
  the rejection message now states the allowed direction, because the reader
  who runs into it is usually rolling back.
  `verifySchema` gained the other half of its question: it used to ask whether
  the needed indexes exist, and now it also asks whether the unneeded ones are
  GONE. And a widening past `OneToOne` SPENDS a CONCURRENCY guarantee:
  `from_uniq` is the only structural barrier against two targets being linked
  to the same left-side record — flows read the link and then write, and the
  advisory lock is only around `Define`. The cost is `OneToMany`'s own
  meaning, but this is the record that makes it reachable.
  `order_payment` is NOT widened here: its readers assume a single capture and
  say so in writing (the refund flow counts a second one as "a data error, not
  a choice" and takes the first), and a rule for choosing between the two
  cannot be written until the second one has a meaning. It is the same
  distinction ADR 0089/0090 made.

- **The store now SAYS who it is** (new `settings` module, ADR 0115).
  The invoicing flow took both parties FROM ITS CALLER, and its own godoc had
  written down why the seller was there: "the seller's legal details are the
  store's own configuration and live in no module here." That had two
  consequences: two documents issued by the same store could name two
  different sellers, and the identity printed on every invoice was the one
  thing the operator could not edit — the person who could change prices,
  products and orders had to ask for a redeployment to correct the store's
  tax office.
  Decision: the `settings` module holds a SINGLE `store_profile` — legal
  name, tax number, tax office, e-mail, address, country — and the invoicing
  flow reads the seller from it. The seller is no longer part of any request.
  The costs: issuing a document before the profile has been written is
  REJECTED, and the message names the endpoint (the operator issuing their
  first invoice is exactly the person who has not filled in the profile). The
  admin body LOSES the `seller` field — narrowing a published surface is not
  a side effect here but the decision itself: a field the caller can supply
  is a field two callers can supply differently. PUT, not PATCH, because the
  record is an IDENTITY, and a partial write would leave the legal name from
  one edit next to the tax number from another. ONE profile per installation
  (ADR 0009 puts multi-tenancy at the installation boundary).
  The module declares its columns as PERSONAL DATA but does NOT IMPLEMENT an
  eraser: a sole proprietorship is a person, but the subject who asks for
  erasure is the CUSTOMER, and erasing the controller's own identity, while
  the issued documents keep printing it, would mean erasing the store from
  its own installation. The e-mail is case-folded NOWHERE, and the exemption
  states its cost: this address is printed, not matched.
- **An exchange can now ship its goods** (`order_replacements` recognizes a
  second source, ADR 0114). An exchange was a request that could only be
  withdrawn. When migration 000008 removed its "completed" status, it wrote
  down by name the condition that would bring it back: goods MUST go out and,
  if the difference is not zero, money MUST MOVE, "and the framework has
  neither". One of the two has arrived — ADR 0090 built the goods-out flow,
  and `order_replacements` is the record it ships. But that record could be
  fed only from a CLAIM, because on the day it was written the only thing
  that could ask for goods was a claim; yet an exchange already means "goods
  going out in return for goods coming in".
  Decision: a shipment record names a SINGLE source — a claim or an exchange
  — and closes that source when it ships. An exchange closes only if
  `difference_due` is zero, and the database holds that bound
  (`order_exchanges_completed_owes_nothing`). An exchange with a difference
  stays open even AFTER its goods have gone out. This is not a gap but the
  honest state: the goods half sits in the shipment record, while the money
  half happened somewhere this framework cannot see. The trigger for the rest
  is already written in the payment module's own link definition — the day
  the order↔payment link becomes one-to-many.
  The cross-module wire carries `source_kind`/`source_id`/`source_status`
  instead of `claim_id`/`claim_status`: two pairs, one of them always empty,
  would make every reader ask "which one was filled in". The two ends cannot
  import each other (ADR 0006), so the compiler does not see this seam; the
  proof is in the integration lane.
  The exchange now has TWO transitions, so the common frame that
  `CancelExchange`'s godoc called "not worth writing for a single transition"
  has been written: with two transitions, the rule "the second call keeps the
  FIRST moment" would otherwise have been written in two places.
- **A line can now be PARTIALLY canceled** (`order_line_cancellations`,
  ADR 0113). Cancellation was all or nothing: `CancelOrder` takes the whole
  order and rejects an order that has a capture — which is right for what it
  is: it is the checkout saga's compensation and runs when nothing has been
  shipped and nothing has been charged. What it could not express was the
  ordinary case: one line of a live order goes out of stock, gets damaged in
  the warehouse, or the customer drops it while the rest of the order ships.
  The module had nothing to say this with, and both ways of recording it
  were wrong — either cancel the whole order, or edit the quantity on the
  line, which is a snapshot of the cart.
  Decision: the record holds HOW MANY of the line's units will not be
  delivered, together with the reason; the ceiling — bought minus return
  requested minus already canceled — is checked under the ORDER'S LOCK. The
  return path reads the same total, so whichever action spoke for a unit, it
  has been spoken for ONCE: a three-unit line cannot have a return requested
  twice and also be canceled once.
  The costs are written down by name. Neither the order's TOTAL nor the
  line's QUANTITY moves (both are snapshots, and the total is nailed to the
  lines by a CHECK). The STATUS does not move either: an order whose last
  line has been canceled too is still an order someone has to close. MONEY is
  a second action — a unit that was paid for but will not arrive is a refund
  or a credit (ADR 0105), and which one depends on a policy this module does
  not hold; a cancellation before payment owes nothing. STOCK is not put back,
  and this is the limit worth naming: the order module cannot reach into
  inventory (ADR 0006), so releasing the reservation is the job of a flow
  above it, and no flow asks for that yet — the trigger is the first flow
  that does.
  The concurrency claim is proven on real Postgres: with the lock removed,
  all sixteen of sixteen callers win and sixteen units are written to a
  three-unit line. The first test I wrote did NOT CATCH this — making the
  goroutines meet only at the start is not enough; once a structural delay
  was placed where the total is read, the mutation turned red reliably.
- **A purchase now earns a unit** (the "buy X, get Y" mechanic,
  ADR 0112). `buyget` was a word in an enum and nothing more: the type could
  be written, the promotion could not be published, and the computation
  skipped it — three rejections standing in for a mechanic that was never
  built. Three things were missing, each of a different kind: the rules were
  `context` and `target`, so nothing separated the line that was BOUGHT from
  the line that was REWARDED; the application method measured an amount, not
  a quantity, so no field said how many units the reward lands on; and the
  computation input carried the line amount, not the unit price — while the
  reward is priced per unit, and deriving it takes a single division, and
  division rounds.
  Decision: the units selected by `buy` rules are counted against
  `buy_quantity`, then the CHEAPEST `apply_to_quantity` of the units that the
  target rules select and that the purchase has NOT CONSUMED are discounted.
  A unit that is bought is not also the unit that is rewarded, so "buy 2, get
  one" needs THREE units in the cart; the other reading of the same words
  would give the customer two for the price of one. The purchase is met by
  the MOST EXPENSIVE units and the reward lands on the cheapest — the
  supermarket's own rule and the safe direction for the merchant. The reward
  is granted ONCE per computation; a repeating ladder ("every third one
  free") is a separate promise, and its trigger is a merchant writing one.
  The cost was paid in two places. The unit price is now a MANDATORY field of
  the discount request, and its identity (unit × quantity = amount) is
  enforced — both callers (the cart flow and the admin compute endpoint) send
  it, because an optional field would make the mechanic work for one caller
  and silently not work for the other. And the mechanic and the method MUST
  AGREE: a buyget without the number pair and a standard promotion carrying
  the pair are both eliminated with `reward_mismatch` and the operator is
  told (ADR 0110). Not applying is the safe direction; the match itself is a
  CHECK, so a hand-written row cannot be half-filled either. `allocation` and
  `max_quantity` are not read on this path, and the `not_standard`
  elimination word is gone — the engine now applies both mechanics.
  The storefront's coupon query was fixed too: a buyget coupon now comes back
  with its MECHANIC and its two numbers, and a coupon the computation would
  eliminate is not offered to the customer. Both use the same predicate; had
  they been written separately, the customer would type the code, nothing
  would happen, and no reason would be recorded anywhere. The mechanic has to
  be in the body: a "buy 2, get one" coupon carries ten thousand basis
  points, and had the mechanic not been stated, the storefront would show it
  as "100% off".
- **The cart's OWN data can now drive a promotion rule**
  (context with the `cart.` prefix, ADR 0111). The discount engine's rule
  context was built from the TWO names the cart flow decides -- region and
  customer group -- and nothing added a third: `internal/app.Options` takes
  only `Modules`/`Plugins`, so the embedder had no seam at all. That made
  ordinary promotions impossible to write: a shop selling two brands from one
  installation could not say "ten percent, brand A only" -- not without
  opening a column in the cart module for a concept that module had never
  heard of. For a framework it ought to be the other way round.
  The prefix is NOT DECORATION: a cart carrying `customer_group_id` in its
  metadata would otherwise let whoever WRITES that bag grant themselves a
  segment discount. The dot is deliberate -- neither of the two fixed names
  contains a dot, so the two namespaces cannot collide under any spelling.
  Only STRING values pass: the engine compares all values, so a number would
  need a formatting rule, and 1 and 1.0 are the same number but two different
  attribute values. A merchant who wants a number writes it as a string; the
  numeric operators already parse it. The count is CAPPED, because every
  attribute is copied into the discount request on every totals pass.

- **An eliminated promotion now says WHY it was eliminated** — and only to
  the operator (`skipped[]`, ADR 0110). `eligible()` returned a bool and
  dropped the reason: nine gates produced a single `false`, so the
  computation could say WHAT applied but not why the coupon the merchant had
  published did not -- the merchant saw zero discount and was left with nine
  hypotheses. ADR 0109 made the question an ORDINARY one: customers can now
  type codes.
  The obstacle was the candidate query. `ListApplicablePromotions` carries
  `status = 'active'`, so a promotion that was published but NOT ACTIVATED is
  never a candidate -- it comes back as neither applied nor eliminated. That
  is exactly the most common reason a code does nothing, and it was the one
  answer the endpoint could not give. `ExplainDiscounts` uses the read
  WITHOUT the status filter; the AMOUNTS on the two paths are exactly the same
  and have to be, because the merchant is shown one path's numbers and the
  customer is charged the other's.
  The reason is on the admin endpoint ONLY: telling a customer "this code
  exists but its campaign has not started yet" would let someone guessing
  codes extract the campaign calendar. The campaign's three states (deleted,
  window closed, budget exhausted) are ONE word -- the same answer for the
  merchant, and separating them would put the campaign's calendar into the
  response.
  The reason set is CLOSED and the vocabulary was written out a second time,
  because Go cannot enumerate the members of a named string type: a word
  nobody can produce is an answer the endpoint promises and never gives --
  and this is not hypothetical: the FIRST version of this change did exactly
  that with `not_active` before the query was widened.

- **The coupon the customer TYPES now lands on the cart, and the order SPENDS
  it** (`cart_promotion_code` + a saga step, ADR 0109). Ever since it was
  built, the promotion engine accepted coupon codes, and nobody SENT it one:
  the cart had nowhere to put the code and the discount request's "codes"
  array was always empty, so only AUTOMATIC promotions could reach a cart --
  the merchant published the coupon and watched every customer who typed it
  get nothing. The silent half was worse: NOTHING in this repository called
  `RedeemPromotion`, which moves the usage counter and the campaign budget,
  so a single-use coupon could never be limited at all.
  The code is checked BEFORE it is WRITTEN; the reverse would keep in the
  cart a code that is unusable for the whole round and then have to take it
  back -- and if that take-back failed, the customer would be left with a
  coupon nothing will honor. A coupon that discounts NOTHING is still
  applied: a valid code with no line matching its target is not invalid, it
  just has not been of use today.
  Coupons are spent BEFORE the order is OPENED, and the reference is the
  CART: a promotion whose last use is taken while the customer is on the
  payment page must reject the purchase, and rejecting once the order exists
  would mean canceling an order that should never have been opened.
  **A checkout left half-done at the moment of an upgrade cannot be
  recovered** -- the engine matches step NAMES against the record, and a
  five-step record does not match a six-step definition; the cost is written
  in the ADR.

- **An image can now be CORRECTED, but its address cannot be changed** (three
  admin endpoints, ADR 0108). ADR 0104 PUBLISHED `alt_text` on the storefront
  and in the GraphQL type and did not leave it correctable: `CreateProduct`
  took the images and nothing else wrote the table, so a mistyped alt text
  stayed for as long as the product lived -- and a published wrong text is
  WORSE than no text at all, because the screen reader now reads out the
  mistake. The patch reaches the alt text, the sort order and the metadata; it
  DOES NOT REACH THE ADDRESS: `url` and the upload link were written in the
  same call, and moving one without the other would leave the row's own column
  and the link record pointing at different files -- exactly why the module
  never opened a "link this image to that upload" endpoint. Changing the
  picture is a NEW image plus deleting the old one, that is, two calls that
  say what they do. Every query carries TWO IDs: an endpoint addressed only by
  the image's ID would let a caller name their own product and edit someone
  else's picture. `alt_text` gained a length limit, and on BOTH write paths: a
  limit that stands on only one of two paths is not a limit.

- **Two carts can now MERGE, and quantities ADD UP** (ADR 0107). Logging in
  could HAND OVER a cart but could not FOLD one in: if the member had a cart
  of their own, the handover was rejected and the customer was left with two
  carts, never to see one of them again. Adding up overlapping quantities is
  not a NEW decision -- it is `AddLineItem`'s decision applied to a batch:
  adding the same variant twice raises a single line's quantity (the price
  tier is chosen from the total quantity, one line means one reservation, the
  same product appearing twice reads like two products), and the same two
  additions should not get a different answer just because they were made in
  TWO SESSIONS. The lock is taken by IDENTITY, not by ROLE: otherwise two
  merges running in opposite directions would each hold the row the other is
  waiting for, and PostgreSQL resolves that by killing one of them -- which is
  exactly what happened when the integration test carried out this mutation.

- **A claim can now SHOW WHAT HAPPENED** (`order_claim_evidence`, ADR 0106). A
  claim carried a reason and a note, so "the box arrived crushed" was a
  SENTENCE and never a photo. The link is made by the upload's IDENTITY, not
  its address: `product_image` carries both because its address is written
  into a page on every product view; a claim's evidence is opened months
  later, by a single operator, for a single claim, and by then a signed
  address will have expired. The same file is evidence for a claim ONCE -- a
  double click is not a second photo -- but it can be evidence for two
  separate claims.

- **An order's AMOUNT OWED can be reduced without changing what was SOLD**
  (`order_credit_lines`, ADR 0105). An order's total is a snapshot of the cart
  and is nailed to its own lines by a CHECK; a concession granted after the
  sale changes not what the customer bought but what they will pay. The
  ceiling is the order's TOTAL, checked under the order's LOCK; a concession
  granted after payment takes the balance below zero, which means "the shop
  owes the customer", and a refund closes it.

- **An image now says WHAT IT SHOWS** (`product_image.alt_text`) — published
  on the storefront and in the GraphQL type. An empty value is not MISSING, it
  is an ANSWER: HTML gives `alt=""` the meaning "this image carries no
  information", i.e. the decorative picture itself. That is why the column is
  not nullable, and its value is trimmed -- an alt text made of a single space
  is a description someone believes they provided (ADR 0104).

- **A promotion rule can now name the PRODUCT and the COLLECTION.** A line
  carried only its VARIANT, so a merchant writing a discount for a product had
  to list each of its variants one by one. Both keys come from the product row
  the pass ALREADY reads, so there is no extra cost. Category and tag are
  LISTS and a line attribute is a single string; carrying them means changing
  the engine's contract, and that decision is deferred here IN WRITING
  (ADR 0103).

- **The order hands the document EVERY rate it charged** — which ADR 0097
  already said it did. It did not: the order's invoicing surface had no
  `tax_components` field at all, the reader was reading a key the producer
  never wrote, and all the totals still balanced. What kept this hidden was
  the flow's own FAKE: an order shape written BY HAND in the consumer's
  package said what the real producer could not. Two tests now pin the hop
  (ADR 0102, D50).

- **A product now wears a TYPE, and a tax rule can name it.** The tax module's
  consumer had been WIRED since the day it was written, and what reached it
  was always EMPTY: a merchant could not say "books 1%" and named every book
  one by one instead. The type comes from the catalog read the totals path
  ALREADY performs -- the same row carries both the discount flags and the
  type, and is read ONCE. When a type is deleted, its products are released in
  the same transaction, because a stale type pointer means money (ADR 0101).

- **A customer now sees their own order's TIMELINE**
  (`GET /store/v1/orders/{id}/timeline`) — the same composition, narrowed to
  the moments of the order and of the GOODS. Money moments and archiving do
  not come through, and the storefront response type carries NO amount field
  at all: an edit that copies one over does not compile. A newly added kind is
  INVISIBLE on the storefront, which is the safe direction (ADR 0100).

- **The price list now carries `metadata` too, and WHICH records carry it is
  now governed by a rule.** A record the merchant WRITES carries it (title,
  description, window); `price`, `price_set` and `price_rule`, which the
  ladder computes over, do not. An update does not MERGE the field, it
  REPLACES it — merging would leave no way to delete a key (ADR 0099).

- **The changelog must now mention every decision taken SINCE THE LAST
  RELEASE, and a gate enforces it.** The population is derived from the two
  documents' own dates: no git command, no hand-written baseline. The cost:
  announcing a decision is now part of making it (ADR 0098).

- **The tax breakdown reached the DOCUMENT: an invoice line now writes each
  rate separately.** The invoicing flow reads the order's `tax_components`
  field, and the invoice module writes into `invoice_line_taxes`; the cost is
  a fourth copy of the same five fields. The new table was brought under
  retention protection, and the limit ADR 0095 left open is closed (ADR 0097).

- **A line now remembers EVERY rate that taxes it.** The breakdown travels
  with the line from tax to cart and from checkout to the `order_line_taxes`
  table; the line's own `tax_rate_bps` remains the BASE of the stack, while
  the list is the whole. Because two boundaries along the path silently drop
  any field they do not know, every schema changed in a SINGLE commit
  (ADR 0096).

- **A rate can stand ON TOP of another rate.** Selection does not change: a
  single rate is still selected, then expanded into the stack it heads; each
  component is rounded on its own base, and the line's tax is their sum. The
  cost: the rate stored on the line is the BASE of the stack — for now the
  invoice writes only the base rate (ADR 0095).

- **A product now wears a tax CLASS** (ADR 0094) — `tax_class` names the
  class, `tax_class_member` links the product, and a rate rule can be written
  for a class rather than for individual products. The tax module resolves the
  class from its own tables: nobody sends it, and nothing changes on the wire.
  A product is in at most ONE class; in specificity it ranks below the product
  and above the type.

- **The storefront's stock badge now counts only the channel's warehouses.**
  Inventory also publishes the same total WITH A PER-WAREHOUSE BREAKDOWN, and
  the storefront sums it through a second expansion: the badge and checkout
  now read the same binding. A read that does not narrow pays nothing; if the
  breakdown is missing, the answer is ZERO, not the total (ADR 0093).

- **A sales channel now ships from its OWN warehouses** (ADR 0092) — a link
  was added between stock location and channel, and the checkout reservation
  is limited to the warehouses that serve the order's channel. A channel
  without a link narrows nothing. The storefront badge was not narrowed: a
  product that shows as in stock can be rejected AT CHECKOUT.

- **Moving a category now takes the LOCK on the whole tree.**
  `pg_advisory_xact_lock` makes a second mover wait, and the statement's cycle
  guard then rejects it once the wait is over. A write that cannot close a
  loop — name, position, flag, clearing the parent — takes no lock; the cost
  is that two moves no longer run at the same time (ADR 0091, amends
  ADR 0085).

- **A claim can now be closed not only with money but with GOODS too.**
  `internal/workflows/returns.DispatchReplacement` reserves the goods, opens a
  parcel on the order and deducts the stock. A reservation now carries a
  PURPOSE: reserved goods leave the ledger as `replacement` and do not get
  mixed up with sales. The cost: one flow resolves another flow by name
  (ADR 0090).

- **A claim to be resolved with goods now says WHAT is to be sent.**
  `order_replacements` and `order_replacement_items` were set up beside the
  order; four admin endpoints record, read, list and withdraw them. Nothing is
  shipped — `claim.go` still does not resolve a `replace` claim, but it can
  now be written against a RECORD rather than a guess (ADR 0089).

- **A key that names a cancelled parcel is REJECTED** (ADR 0088) — the
  "already open" answer came from a link that had outlived the parcel. The
  status is read from the narrow surface the flow already has, so the module
  boundary does not widen; the cost is one call per opening. The error names
  the shipment and says that a NEW key is needed.

- **The group NAMES of the known limits are now held too, not just their
  counts.** The README's list was matched against the `docs/known-limits.md`
  headings, in lower case and IN ORDER: a group inserted in the middle but
  written at the end describes a different document from the one it prices.
  Whether an item sits under the RIGHT heading is still something no gate can
  hold (ADR 0087).

- **A price can carry its tax INSIDE it** — and extracting it is not the
  inverse of the plain calculation but an arithmetic of its OWN. The flag
  lives on the tax REGION, and NULL means INHERIT; a chain that says nothing
  stays tax-exclusive, so nothing changes in existing installations. The
  amount written on the label is now the amount paid at checkout (ADR 0086).

- **A category can now be CHANGED: `PATCH
  /admin/v1/product-categories/{id}`** — name, parent category and flags can
  be written, and a category born closed can finally be opened. A move that
  would close a loop is rejected by the STATEMENT itself, not by a check
  standing beside it (ADR 0085).

- **The numbers in `docs/gaps.md` are now unique and DENSE.** The rule already
  stated in the ledger's first paragraph is finally enforced by
  `internal/arch/gap_ledger_test.go`. Three rows that sat on the same address
  were renumbered as D36-D38; the commit messages that introduced them go on
  naming the old numbers (ADR 0084).

- **The chained command block in the document is now RUN, not rewritten**
  (ADR 0083). The block in `docs/security.md` is handed to `sh` as it stands
  and its responses are read in order: the header name, the body field and the
  `jq` path, which the route gate cannot see, are held for the first time. The
  cost: the test needs `curl` and `jq`, and FAILS without them instead of
  skipping.

- **Every type that makes a concurrency promise is now run from TWO
  goroutines** (ADR 0082) — the population comes from the intersection of the
  package's promise and the primitive the type carries, and the witness is
  named by the written map in `internal/arch/concurrency_promise_test.go`. The
  godoc of `Bootstrap` now also says what its guarantee does not cover: a
  subscriber is not a route.

- **Every path the startup probe measures now carries a WITNESS test.**
  Because its subject is the CLUSTER rather than the code, a cluster that
  breaks the contract turns the suite red in three places; the population
  derives not from a list but from the probe's own SQL
  (`internal/arch/cluster_contract_test.go`). The suite's containers were not
  bound to production's initdb arguments (ADR 0081).

- **Three fuzz targets were published, and the important seeds were not found:
  they were COMPUTED.** `go test` runs only a target's SEEDS; every target
  carries at least three seeds (`internal/arch/fuzz_seed_test.go`), while
  `make fuzz` does not run in CI. The remaining rule: after writing a target,
  mutate the code it protects; if generated input cannot find the boundary,
  compute the boundary and seed it (ADR 0080).

- **Every benchmark carries a `benchbudget.Budget`, and the ceiling is
  ALLOCATIONS per operation.** The budgets run in the ordinary test lane;
  `internal/arch/benchmark_budget_test.go` derives the population from the
  DECLARATION, not the file name. An allocation added to a priced path now
  breaks a test; a change that slows things down without allocating is still
  invisible (ADR 0079).

- **Every DIRECT dependency carries a sentence of justification, every
  INDIRECT one a line** — the sentence comes from the author who chose the
  dependency, not the consumer who discovers it
  (`internal/arch/dependency_allowlist_test.go`). `govulncheck` runs at the
  root and in the two example modules, a known vulnerability BREAKS the build,
  and there is no exemption mechanism (ADR 0078).

- **`core/providertest` was PUBLISHED: a provider now passes a RUNNABLE
  conformance suite, not a written sentence.** The surface grew to eighteen
  packages, and the twelve providers in the tree run, from their own packages,
  the SAME suite an embedder would run. The suite checks only what holds
  without going to the service, and says so: green does not mean "it works"
  (ADR 0077).

- **The migration of ADR 0030 BEGAN: the panel's first `/admin/v1` screen is
  the moderation queue** (ADR 0076). The screen is a shell and a script; there
  is no new module contract. The session cookie now goes to the whole `/admin`
  tree: the admin API's CSRF immunity was an ABSENCE, and a defence was put in
  its place — a state-changing request that arrives with the cookie requires a
  same-origin `Origin`.

- **`docs/measurements/README.md` is now CHECKED: every row states its
  report's real length, and every report has a row.** The real gain is the
  second direction: unindexed evidence is evidence that anyone who does not
  know its name CANNOT FIND. The cost is one line -- the commit that grows a
  report also carries the index row (ADR 0075).

- **Agreement between the model and the operators is now COUNTED.**
  `GET /admin/v1/reviews/suggestion-agreement` gives two numbers per model:
  how many decided reviews have a suggestion, and how many of those
  suggestions named the status the review ended in. There is no RATIO; a
  client that sees the denominator computes it itself. The report is computed
  on read; nothing is stored (ADR 0074).

- **A suggestion SURVIVES the decision made on it: moderation does not delete
  it.** The admin list now narrows with `?suggested=`, an unrecognised value
  gets a REJECTION rather than an empty page, and the filter is backed by
  `reviews_suggestion_idx`, limited to the queue. The cost is a growing table;
  what it buys is the ONLY corpus that can measure whether model and human
  agree (ADR 0073).

- **The model answers a CLOSED question, and a scheduled job asks it.**
  `core/provider` publishes a classification contract, the `ai-anthropic`
  plugin fills the singular `ai.provider` slot, and the job is registered only
  if the slot is filled. An installation that names the plugin takes on a
  SUBPROCESSOR, and no accuracy is claimed: it was not measured (ADR 0072).

- **A model's suggestion is stored BESIDE the review, not in its decision.**
  Four columns stand on their own; `status` and `moderated_at` are not
  touched, so the mirror still says a human made the decision. A suggestion
  exists in full or not at all, is not written to a review that has already
  been decided, and is visible to the shopper nowhere. Nothing writes
  suggestions yet (ADR 0071).

- **A count claim is checked against a CLOSED vocabulary** (ADR 0070) — there
  are eight populations, and a sentence enters the gate only by writing the
  population's PATH on the same line. On the day it opened, the gate found
  wrong numbers in the READMEs. Every ADR record and this file are out of
  scope; the universal negation stays ungated, because its object is a
  predicate.

- **The job report's CHANNEL was published; the scheduler was not.**
  `core/jobreport` carries three functions; the runner stayed in
  `internal/core/job`, and the core's own jobs also report through the same
  published package — ONE mechanism. A plugin's successful run can now speak
  in the `gobit jobs` detail too; the cost is the seventeenth package under
  `core/` (ADR 0069).

- **Every change to physical stock leaves a ROW, but the number is still held
  by `stocked_quantity`** (`inventory_movements`). A movement is written in
  the same transaction as the column and carries `stocked_after`; drift shows
  up in a single row. A reservation is not a movement, behind each row there
  is a REASON rather than an actor, and nothing deletes the row; the ledger
  came with an admin endpoint (ADR 0068).

- **`province` is the unit below the country; it is NOT the district.** Every
  hand-written declaration now says so; `internal/arch/province_test.go`
  rejects a new SILENT one. The address was fixed end to end; the district
  still has no field (ADR 0067).

- **No suggestion store is BUILT; a suggestion lives in the module that owns
  the row it concerns, and is applied through that module's write path**
  (ADR 0066). The trigger is the first suggestion a query cannot reproduce;
  what applies it is not gobit but the human calling the existing endpoint.
  The cost: an operator who wants suggestions today finds nothing, and a
  suggestion that spans two modules has no home here.

- **`coreprovider.QuoteInput` was NOT WIDENED: district and desi (volumetric
  weight) arrive on the day the tree can address and measure a parcel.** The
  cost: a carrier integration that prices by district stays on a flat tariff.
  A gate holds the rule: `TestEveryQuoteInputFieldIsFilledByTheTree` rejects
  any field in the published input that no production file in the tree fills
  (ADR 0065).

- **The saved payment method is on hold, and what it waits for is not a
  feature but a PROVIDER.** Paying with a stored token works end to end today;
  what is missing is an upstream flow that PRODUCES such a token. The shopper
  types their card in again at every checkout, and the published surface is
  not spent on a shape with no implementer (B9 → ADR 0064).

- **The stock event and the file event were NOT PUBLISHED: a forwarding plugin
  is not a topic's first subscriber.** `plugins/webhookout` carries every
  topic by necessity, and `TestEveryTopicHasASubscriberThatChoseIt` now
  rejects that. The cost: two events that could pass every gate are not sent;
  B15 and the event half of B7 closed as a DECISION, not as a gap (ADR 0063).

- **A callback's ledger is the RECEIVING module's own table** — there is no
  dedicated `callback_log`. The questions of reader, scope and retention are
  already answered on the `paytr_payment` side; a call whose handler never ran
  stays only in the log, and gobit makes it no retention promise. The decision
  reopens on the day a SECOND provider enters the tree (ADR 0062).

- **gobit builds neither half of the language axis, and this is not a
  shortcoming but a DECISION: A11 left the ledger with an answer.** Two gates
  reject a locale that has no record — a Go name or struct tag outside
  `plugins/webpush`, an SQL column outside the device record. The cost: a
  locale arriving as a path or query key is not tracked (ADR 0061).

- **Separating the migration role from the runtime role is the OPERATOR'S job;
  gobit's binary does not change.** A single DSN remains, and neither role
  management nor a startup check is coming; what is provided is not a setting
  but the privilege list published in `security.md`. The out-of-the-box
  single-superuser setup, for its part, stays both unchanged and unprotected
  (ADR 0060).

- **The measurement harness can now also build a SKEWED taxonomy:
  `Spec.SkewedCategorySize` produces two small categories, zero produces
  none.** The small-category case now needs a COMMAND rather than a hand-built
  trial database; the cost is that a product belongs to two categories for the
  first time, and that positional memberships need resetting when the size
  changes (ADR 0058).

- **Every storefront endpoint that names a customer submits its claim to ONE
  comparison:** `corehttp.ProvenCustomer`. The b2b storefront and the cart
  were wired to it, and the address book was moved onto it. What is questioned
  is the CLAIM, not the ENDPOINT; in an installation with no verifier wired,
  nothing is withdrawn, only a WARN is logged (ADR 0057).

- **A callback does NOT BECOME an `audit_log` row; its record is the
  `CallbackRegistry`'s own log.** Every outcome leaves a line there, rejected
  ones included. The cost: the operator searches a log where they would expect
  a query: `GET /admin/v1/audit-log` shows no callbacks (ADR 0056).

- **A stock location closes EMPTY, and the closed row stays readable.** A
  location still holding units or a live reservation REFUSES to close, and a
  closed location accepts no stock writes; availability reads thus stay
  join-free. `deleted_at` gave way to `closed_at`, and closing is final: there
  is no reopening (ADR 0055).

- **Orders and payments are NOT DELETED: ten `deleted_at` columns were
  dropped, and the money-events surface did not gain a third moment.** An
  order retires through its STATUS, a money record is kept; no record can be
  hidden any more. The four uniqueness rules finally cover EVERY row as well:
  an idempotency key cannot be freed by stamping it by hand (ADR 0054).

- **gobit stores ONE language; the second language belongs to the embedding
  program.** There is no locale column, translation table or translation
  module; the only place that can carry a second language today is the tables
  with a `metadata` field, and categories, tags, options and the country and
  currency names gobit seeds are outside that path. Because no request
  reaching the storefront can state a LANGUAGE yet, A11 stays open (ADR 0050).

- **ONE group determines the price, and the SELLER ranks the groups.** The
  cart writes the customer's highest-ranked group as a single
  `customer_group_id` value; `customer_group` gained a `rank` column. Pricing,
  promotions and delivery did not change at all; a store that never sets a
  ranking gets ID order (ADR 0049).

- **The cluster contract does NOT BUDGE: pgvector comes as an optional,
  separate plugin module** (ADR 0045). The extensions line stays `none`,
  because a plugin nobody has to install does not change what any installation
  must provide. `CREATE EXTENSION` belongs only to that module's own
  migration; if it leaks outside, ADR 0015 is reopened in the same change.

- **What the customer pays and what the seller receives REMAIN the same
  number.** When a difference is needed, the equality is not relaxed; the
  difference arrives as a separate RECONCILIATION LINE carrying its own
  counterpart, and the first consumer decides the line's shape. The four
  layers that hold the equality were named one by one for the first time; the
  cost is that an instalment surcharge is still not possible today (ADR 0042).

- **How an email address is stored is ONE rule: it is trimmed, then lowercased
  on the GO side, never in the database.** `invoice` now folds in Go too:
  `buyer_email` keeps exactly what the document says, and equality is
  established through `buyer_email_folded`. The six copies stay where they
  are; `internal/arch/email_test.go` holds them together (ADR 0038).

- **A person can now SEE what can be erased about them** (ADR 0034) — a
  personal-data disclosure was published alongside erasure, and a person's
  file is gathered by the same sweep that erases it. A holder that cannot
  answer shows up in the file as `Unresolvable`: the gap is written not into a
  ledger but into the document the person receives.

- **An incoming provider call is not MOUNTED, it is REGISTERED** (ADR 0028) —
  the plugin declares the route with `Host.RegisterCallback` and the core does
  the mounting; quota, body limit, timeout, signature verification and the
  replay window apply to all of them. A route without a verifier is rejected
  at startup: an unprotected endpoint can no longer be EXPRESSED.

- **The composition root moved to `internal/app`, and the PUBLISHED facade at
  the module root calls it: `cmd/server` is now fifteen lines.** An
  application outside the tree thus becomes possible, and the operator
  subcommands ship with the library. The facade is four methods; the lifecycle
  was not published, and only the facade can import the `internal/` tree
  (ADR 0027).

- **Two clocks REMAIN, and every moment names its own clock** (ADR 0053).

- **`returned_at` finally reached the cross-module read layer.** It existed in
  the column, the model and the admin body; it was missing only from the map
  that decides what another module may read. Nothing COULD HAVE REPORTED the
  gap, because that map does not answer an unknown field with zero, it REJECTS
  it (ADR 0004) -- so nobody asked for the fourth moment, and nobody learned
  they could not. The order timeline now also shows the returning parcel
  (`shipment.returned`).

- **Migration cancellation dropped its graceful layer** (ADR 0052, amends
  ADR 0003) — D31 was diagnosed and CLOSED.

- **The remainder of D10 is closed: ownership belongs to the whole tree, not
  to the modules** (`internal/arch/module_sql_test.go`).

- **The shape of the records is now governed by a rule: an ADR gets 80 lines,
  measurements live in a separate tree.**

- **`docs/measurements/` was opened.** The 15 measurement reports inside
  gaps.md (2,757 lines) and `catalog-search-cost.md` were moved there,
  unchanged. An ADR links to a measurement in a single line. A measurement
  file may be as long as it needs to be -- nobody has to read it; everybody
  has to read the ADR.

- **The `docs/adr/README.md` index.** Number, title, the decision in one
  sentence, status (in force / which record changed it). A newcomer reads this
  first. `TestTheADRIndexNamesEveryRecord` holds both directions.

- **`docs/gaps.md` 4,615 -> 156 lines.** Every gap has one row: the question,
  and where the answer is. A closed row names its ADR and falls silent; the
  reasoning is in the ADR, the history in git. Struck-through text and the
  "original naming below" blocks were deleted. Nothing was DECIDED or reopened
  during the simplification -- but two rows turned out to be STALE: D22
  (webhookout can now be installed) and D25 (the parameter gate has been
  written) still showed as open in the ledger.

- **Store search also carries the sales channel IN ITS PATH** (the fourth
  route of ADR 0044) — and the rule is now not a claim but a MECHANISM.

- **The store catalog gained THREE filters** (ADR 0039, ADR 0040, ADR 0041) —
  option value, stock status and price range; all one surface.

- **The sales channel moved into the catalog PATH** (ADR 0044) — the store
  catalog now lives under
  `/store/v1/sales-channels/{sales_channel_id}/products`.

- **Metrics go out by SCRAPE** (ADR 0046) — if `METRICS_ADDR` is set, a
  `/metrics` endpoint opens; OTLP stays with traces.

- **A replaced price is DELETED** (ADR 0047) — and what survived a replace was
  never a history anyway.

- **The four carried flags are finally READ** (ADR 0048) — two at checkout,
  two in the promotion engine.

- **The audit log (`audit_log`) can finally be READ** (ADR 0037) — and reading
  it is recorded too.

- **The search plugin was translated into English and its two endpoints
  documented** — the schema ledger dropped to ZERO, the language ledger from
  214 to 202.

- **A component name now carries the module that owns it** (ADR 0036) — and
  twenty-five of the thirty-eight entries in the ledger opened yesterday were
  paid off the same day.

- **The webhook plugin's operator surface was both TYPED and documented**, and
  typing it exposed a defect.

- **The schema vocabulary was PUBLISHED, and silence was given a voice**
  (ADR 0035; addendum to ADR 0026 — the sixteenth package).

- **The saga store now deletes its own rows, and it turned out to be EDITING,
  not "pruning"** (addendum to ADR 0033).

- **The KVKK erasure contract was established, and it became the fifth of the
  seventeen decisions** (B17 → ADR 0033; an addendum each to ADR 0026 and
  ADR 0032).

- **The first four of the seventeen decisions were made and written down as
  ADRs: the root, the pair and the single live hazard** (A2 → ADR 0029, A7 →
  ADR 0030, A12 → ADR 0031, A4 → ADR 0032).

- **A row that said "behind a decision" had no written decision; it was
  written, and the decision turned out to be text matching** (A18, the OPTION
  VALUE half of B2).

- **If a decision is named after the FEATURE that found it, the next round
  pays for the same question a second time** (A15, B4).

- **A15 was applied, not quoted: what carries the answer is SQL — not a
  comment line** (A15, B4).

- **A plugin that had been written, documented and tested end to end COULD NOT
  BE INSTALLED — and the gate written for this class was GREEN** (C5, D22).

- **The migrate-down tests of `internal/app` were failing not in the proof
  itself but in its PRECONDITION.** Two tests had hard-coded that region has
  "exactly two migrations"; when the module gained a third, both broke. The
  number is now read: one derives the number of steps to roll back from the
  current version, the other reads the untouched owner's version BEFORE
  rolling back and compares against it.
- **Four foundational rows carried a blocker that their own rows did not name
  — and measuring showed that ALL eight open rows were blocked** (B9, B15,
  B16; B8 the same day).

- **The decision list said it was an ORDER but wrote the order down nowhere;
  it was measured and written down, and out came one root and one PAIR.**

- **Two modules had no tests at all in their `api` package, thirteen did, and
  nothing was asking about it** (D26).

- **The other direction of the same machine was derived too, and writing both
  directions required FOUR fixes in the scanner** (continuation of D25).

- **The gate for D25 was written and turned up two live findings on its first
  run** — and the naive form I rejected stays on record.

- **A handler can read a query parameter it never documented, and all of the
  repository's gates stay green** (D25).

- **gobit will be a LIBRARY, not a template that gets copied** (ADR 0025).

- **A write whose identity is unknown is now held by the SCHEMA too**
  (ADR 0051) — the third part of the decision, the part the record itself
  carried as "not done".

- **The address book now requires a PROVEN identity** (ADR 0043) — and gobit
  still does NOT PRODUCE that identity.

### Fixed

- **A published API description said the OPPOSITE of what the endpoint does**
  (D62). ADR 0125 made four storefront endpoints reject by default; the eight
  passages describing the old behaviour did not follow. Three of them were
  OpenAPI descriptions — that is, the promise given to the integrator
  (ADR 0026) — and someone reading the document would write code at odds with
  a status the endpoint now returns by DEFAULT. The other five were in Go; two
  of them, explaining why `IdentityLookup` exists, described the rejection as
  "the breaking change ADR 0057 was rewritten to avoid" — precisely what ADR
  0125 did, deliberately and with a way back. No gate caught it, and none can:
  the count gate prices populations, the schema-name gate resolves
  identifiers; neither reads a sentence for its MEANING.

- **A funded exchange stayed OPEN forever after its goods had shipped** (D59).
  ADR 0120 gave the exchange the `funded` state and widened both closing
  guards for it; the shipment flow, however, kept its own third condition when
  closing the source — a copy of a vocabulary it does not own. The new word
  did not make it into the copy, so the operator collected the difference and
  shipped the goods, and the record stayed `funded`. Nothing failed, nothing
  was logged: that guard's whole job is to be silent. The flow now looks at
  the order module's ANSWER (`source_open`), not at the state. In the same
  commit, ten sentences describing the world before ADR 0120 were corrected —
  one of them a published OpenAPI description.

- **Four decision rows named a TOPIC; they were measured and turned into
  QUESTIONS — and the first draft carried twenty-four false claims** (A4, A5,
  A11, A12).

- **All of the documentation was measured against the code: thirty-eight
  claims were wrong, and three contradicted gaps.md's OWN table.**

- **A cleanup done to add a root revealed that godocs referred to twenty-five
  dead file names — and no gate saw that class.**

- **The path check's root list had a five-file hole, and what found the hole
  was a file that fell into it.**

- **Nothing held the DEFAULT of a flag that nothing READ either — and
  `allow_backorder` is not alone, it is one of four** (A6, D2).

- **The gates themselves were audited: three of 89 gates were GREEN right on
  top of the defect they were written for** (D23).

- **Carrier events arrive OUT OF ORDER; the shipment state machine rejected
  all of them — while tolerating duplicates** (B10, D24).

- **Tax's shape was looked for in four more modules: two turned up a DEFECT,
  two did not — and the absence was measured too** (D6, D19, D20).

- **TWO of the column check's three blind spots were closed, a FOURTH was
  found while closing them — and the fix turned up nine live findings on its
  first run** (D16, D18).

- **Nine columns that appeared the moment the check was fixed: nine
  `deleted_at` columns that nothing writes** (D18).

- **The check built to catch a column that is never written had never caught
  the finding it gives as an EXAMPLE in its godoc — all three of its blind
  spots were measured by mutation** (gaps.md D16).

- **A criterion that is not given now writes NO CLAUSE AT ALL — and the real
  cost of the category filter was MEASURED for the first time.**

- **The product module's eight recorded performance figures were measured
  again; FIVE turned out wrong** (D15).

- **`make load-test` was measuring an EMPTY catalog** (D14) — and this is one
  floor below D11.

- **`make load-test` measured nothing — and looked green.**

- **`docs/gaps.md`: B2's remaining four filters turned out NOT to be a single
  job — measured and split.**

- **`docs/gaps.md`: the premise of the read-cache item was refuted BY
  MEASUREMENT.**

- **`docs/gaps.md`: the "an admin session cannot be revoked" item was WRONG.**

- **`docs/gaps.md`: the item saying a guest cart cannot be taken over was
  WRONG.**

### Added

- **The storefront's fourth lookup endpoint was built — and it is the only one
  that returns text** (prerequisite for the OPTION VALUE half of B2).

- **The storefront catalog can now be sorted — and what the row called its
  "only trap" had already been solved by the contract it rides on** (the SORT
  half of B2).

- **Review module: a customer can write a review, and it appears nowhere until
  it is approved** (B4). The seventeenth module.

- **A parcel can COME BACK: `returned` is the fifth shipment status, and the
  table now separates NOTIFICATION from COMMAND** (B10).

- **The SENDER for outgoing webhooks was written — and it CANNOT BE INSTALLED
  today; this was not assumed, it was measured** (C5, D22).

- **The nine columns that nothing ever wrote were answered — and it turned out
  the nine had no SINGLE answer** (D18).

- **A successful job run can now SPEAK: the detail column of the `gobit jobs`
  list came out from behind an ERROR** (D21).

- **A plugin can now register a scheduled job — and the extension point
  arrived together with its FIRST CONSUMER** (B13).

- **Dead letters now have an OPERATOR FACE: `gobit deadletters`** (B12).

- **A return request and a claim can now be WITHDRAWN — two `UPDATE`
  statements had no caller in production** (D17).

- **The outgoing delivery machine: retry with increasing delay and a DEAD
  LETTER — and measurement showed that what it fixes is not a slowdown but
  delivery STOPPING** (B12).

- **An order's archiving is now DATED, and the exchange table received its
  first `UPDATE` statement** (D5, D4).

- **The tax module carries the transaction in the CONTEXT, and the module has
  its first shared lock** (the tax half of D6).

- **The panel's catalog now has a SEARCH BOX: the read layer learned `q` — and
  its cost was MEASURED on 52,004 products** (the last filter of B2, the
  remaining half of D12).

- **The measurement harness is now built FROM THE REPOSITORY: `gobit seed`**
  (D13).

- **The panel's catalog can now be narrowed by category: the read layer
  learned the taxonomy filters and a `category` entity** (B2, D12).

- **A module's SQL can name only its OWN tables**
  (`internal/arch/module_sql_test.go`).

- **Every `-run` pattern in a build file must name a real test**
  (`internal/arch/build_files_test.go`).

- **The sold LINE can now be read: the `order_line_item` entity and a Sales
  section in the panel** (B14).

- **The panel has a frame and a second section** (styling, menu, orders).

- **Invoice module** (ADR 0024) — the document, its lines, its parties, its
  status and its **gapless** numbering.

- **An order is now invoiced with a single call**
  (`POST /admin/v1/orders/{id}/invoice`).

- **An order line now says at what RATE it was taxed** (`tax_rate_bps`).

- **Deep pages are now cheap** (cursor pagination).

- **The Go side is now measured** (pprof + benchmark).

- **Admin writes now leave a trace** (audit log).

- **A promised event is now part of the transaction that promises it**
  (outbox, ADR 0023).

- **A storefront in the browser can now call the API**
  (`CORS_ALLOWED_ORIGINS`).

- **Refunds arrived and closed the half that ADR 0022 left open** — including
  the B2B budget bug.

- **Claims are now resolved — but only with money, and by REJECTING the other
  kind.**

- **A customer can now open a return request**
  (`POST /store/v1/orders/{id}/returns`).

- **A return now ACTS: the stock of received goods is put back**
  (`internal/workflows/returns`, after-sales 2/3).

- **There is now a path between an order and its payment** (the
  `order_payment` link).

- **A return record can now move, and it says which lines came back**
  (after-sales, 1/3).

- **An order that has been paid for can no longer be cancelled** — it could
  be, and the cancellation reversed nothing.

- **Tax is now computed from the right inputs** — a mixed cart was taxed
  throughout at the highest rate.

- **An order now knows what has been paid against it** (ADR 0022) —
  `paid_total` was zero on every real order.

- **A shopper can no longer set their own shipping price** (ADR 0021) — this
  is not a feature but the closing of an exploitable hole.

- **Payment reconciliation** (`internal/jobs/paymentrecon`, ADR 0020) — the
  repository's only named periodic promise that was not being kept, and it is
  about money.

- **Scheduled jobs arrived** (`internal/core/job`, ADR 0019) — but at a tenth
  of what I planned, and their real value lies not in the code but in the
  MEASUREMENT.

- **Payment with PayTR arrived** (the `payment-paytr` plugin) — and it reached
  **the same finding** as web push, from the opposite direction.

- **Browser push notifications arrived** (the `web-push` plugin, ADR 0018) —
  and did NOT GO INTO the provider slot. This round's real finding changed the
  decision, not the code.

- **Uploads can now go to an object store** (the `file-s3` plugin). The
  out-of-the-box `local` provider is correct for ONE process and wrong for
  TWO: the file lands on the disk of the instance that served the upload, and
  every request routed to another instance gets a 404 — with no visible error,
  because from that instance's point of view the key really does not exist.
  Works with AWS S3, MinIO and R2.

- **Notifications are now REALLY sent** (the `notification-smtp` plugin). The
  only notification provider out of the box was `logonly`, and its name
  honestly said what it did: it writes a log line and sends nothing anywhere.
  So until now the notification slot was a promise the framework did not keep
  — the provider abstraction existed, a working implementation did not.

- **A SECOND error reporter was written, and ADR 0014's test was thereby run**
  (`error-otlp`). The ADR said "only a second implementation shows whether the
  contract has the right SHAPE or merely the shape Sentry wants"; the second
  implementation chose the one whose model is furthest from Sentry. The
  OpenTelemetry log model has no "issue", no grouping key, no deduplication: a
  record is a time, a severity, a body and attributes.

- **`gobit recover <execution-id> -confirm <execution-id>`: a half-finished
  saga can now be compensated BY HAND.** v0.8.0 had made an interrupted
  payment VISIBLE (`gobit stuck`) and REVERSIBLE (compensation from the
  records), but the reversal could only be triggered by a caller returning
  with the same key. That covers the customer who retries and nobody else: an
  abandoned cart has no returning caller, the record stays `running` forever,
  and there is NO ONE TO RELEASE the stock it reserved. The operator held a
  list they could not act on.

### Changed

- **No Turkish is left in the `cart`, `order` and `auth` modules** (ADR 0012's
  ratchet). Ninety-five files were translated in two stages by eighteen
  agents, one agent per package; the content ledger dropped from 397 files to
  **302**, the path ledger from 16 rows to **9**.

- **A class of debt the ratchet COULD NOT SEE was found and closed: Turkish
  without diacritics.** The three-lane detector scans for Turkish letters, the
  word list and AST IDENTIFIERS; all-ASCII Turkish such as `"limit negatif
  olamaz: %d"` inside a comment or a string constant falls outside all three
  lanes. The result: files that were CLEAN according to the detector, and
  therefore never entered the ledger, and therefore were never assigned to any
  agent, went on carrying Turkish.

- **A stem deleted from the detector's stem list was put back.** The
  `"ayristir"` entry in `turkishStems` had become `"parseDir"` in `5b0778c`
  through an identifier rename: the list is DATA, not SOURCE, and the bulk
  rename silently ate it. Because the suite stayed green, nobody noticed. The
  cost of the blindness is measurable — `internal/arch/configuration_test.go`
  had carried the identifiers `ayrisik`/`ayristirmaHatasi` ever since, and the
  file had been counted as "translated". The third recurrence of the same
  class (the earlier ones: `denetim` → `auditCtx`, `gunlukBekle` →
  `waitForLog`).

- **The 19 YAML files outside the ratchet were translated.** The detector
  scans `.go`, `.sql`, `.gohtml`, `.md` and `.graphqls`; YAML is not scanned
  at all, so this debt NEVER showed up in the ledger. Fifteen `sqlc.yaml`
  files, `gqlgen.yml`, `.golangci.yml`, `.github/workflows/ci.yml` and
  `deploy/docker-compose.yml`.

- **No Turkish is left in the `internal/core/workflow` tree** (ADR 0012's
  ratchet). Over five rounds the engine itself, `pgstore` and ALL the test
  files of both were translated; the ledger dropped from 715 files to **708**.

- **No Turkish is left in the `internal/core` tree** (ADR 0012's ratchet). In
  the four rounds that followed the workflow round, the remaining files of
  `core/http`, `redisguard`, `core/query`, `core/openapi` and
  `internal/core/config` were translated; the ledger dropped from 708 files to
  **680**, and there is no longer A SINGLE row in the ledger starting with
  `internal/core/`. The remaining debt is in `internal/modules/*`,
  `internal/e2e`, `internal/arch`'s own tests and ADR 0001-0011.

- **The BREAKING SURFACE of fifteen modules moved to English**, and the
  ordering itself is a decision. The debt was ~45 thousand lines; so that the
  remaining half would be HARMLESS if the budget ran out midway, the surface
  that a forking/vendoring installation COMPILES against was translated first:

- **No Turkish is left in the `internal/arch` and `internal/core` trees**, and
  `internal/e2e` is half done. The ledger dropped from 715 files to **559**,
  the path ledger from 37 to 29.

- **The translation was parallelised** (one file or one module per agent), and
  measurement showed that two steps must stay CENTRAL. If shared identifiers
  are not translated by a single hand BEFORE the wave, two agents translate
  the same name into two different English names; shared error TEXTS, on the
  other hand, have to be translated AFTER the wave, because the `provider.go`
  of twelve modules carries byte-for-byte identical boilerplate, and the rule
  "do not touch a string in someone else's file" means no agent can clean it
  up on its own.

- **A bulk rename can corrupt the detector's own DATA.** This round it did:
  the `denetim` → `auditCtx` rename also changed the `"denetim"` entry in the
  `turkishStems` list in `language_test.go`, which means a stem was deleted
  from the detector. The suite stayed green, because `TestDetectorIsNotBlind`
  pins a floor under the list's LENGTH, not the individual entries. The stem
  was put back; the lesson repeats ADR 0012's own sentence — string constants
  can be DATA, not source.

- Error CODES, entity/link/record NAMES, filter keys, JSON tags and ID
  prefixes changed in NONE of these rounds. It is the same decision as the one
  made for the engine in v0.8.0: the message moves to English, the contract
  stays in place.

## [0.8.0] — 2026-09-04

### Breaking changes

These are legitimate in a minor release throughout `0.x` (see the top of the
file). None of the three changes the HTTP surface; two concern installations
that EMBED the framework, one concerns those who WRITE their own saga step.

- **The signature of `SetLineItemTotals` on the `cart/service.Store` port
  changed.** The old signature was called once per line and returned the
  updated line; the new one takes ALL the line totals of a calculation pass in
  a single call and returns only an error:

  ```go
  // eski
  SetLineItemTotals(ctx, cartID, lineID string, totals models.LineTotals) (models.LineItem, error)
  // yeni
  SetLineItemTotals(ctx, cartID string, lines []models.LineItemTotals) error
  ```

  An installation that implements this port itself will not compile. The
  reason is a measurement, written up below under "Changed": an UPDATE per
  line held the cart's lock for a time proportional to the number of lines.

- **The error MESSAGES of the engine, `pgstore`, `core/link` and
  `core/eventbus` are in English.** The error CODES and the VALUES of the
  status constants did not change — they are the machine contract and were not
  touched. The only class affected is assertions bound to the message TEXT: in
  this round exactly three such bindings broke in the repository's own tests
  and were seen only when the tests ran, because there is no compiler link
  between a code and its message. A storefront that shows the message to the
  customer AS IS is affected too (ADR 0012's ratchet; the same class began in
  v0.6.0).

- **The `workflow.Step` contract grew: `Compensate` may be called
  CONCURRENTLY.** Until now it said "may be called twice", and that meant ONE
  AFTER THE OTHER. The recovery path is the first path that can call a
  Compensate at the same MOMENT. Because the two stores shipped in this
  repository (`NewMemoryStore`, `pgstore`) make recovery exclusive, the door is
  closed in practice; it is open in an installation that implements ANOTHER
  `workflow.Store`, and a step that writes its compensation as
  read-modify-write releases stock more than once. Undoing must be done BY ID.

### Added

- **`gobit stuck`: half-done sagas can now be LISTED.** v0.7.0 had stopped an
  interrupted payment from being silent (an execution whose lease expires is
  closed, `compensation_failed` is written if work was done, and an ERROR is
  logged), but there was no surface to SEE that record; the operator opened
  psql. The command ONLY READS: no reservation is released, no execution is
  closed, no key is freed — releasing the stock of a saga that is still running
  would get that stock reserved a second time.

  The command lists TWO classes, and the second was found by measurement. The
  status query (`compensation_failed`) sees only the records the engine has
  CLOSED; but if the process dies in the middle of a saga and the customer
  never comes back, the record stays `running` forever, holds the stock and
  appears in no log line. Measured: of two executions awaiting manual
  intervention, only one was found by the status query. The second class is
  therefore defined as "lease expired AND has a step still holding something" —
  if a record that is merely old holds nothing, the engine repairs it itself,
  and listing it would fill the operator's page with needless rows.

  The decision itself is in [ADR 0016](docs/adr/0016-operator-read-surface-for-half-done-sagas.md).

  The staleness cutoff MOMENT is now computed INSIDE the query and returned
  along with the rows: the moment that SELECTS the rows and the moment WRITTEN
  in the header come from the same expression. Leaving two separate values was
  a class of drift invisible in tests — because the caller and the database
  are on the same machine in tests, a version that filtered by the caller's
  clock and printed the database's clock passed the whole suite (measured by
  mutation).

- **`gobit migrate status` and `gobit migrate down <owner>`: migrations now
  have a surface open to the operator.** The `.down.sql` files existed and
  their reversibility was tested, but nothing called them; rolling back was
  done by hand. `cmd/server` did not even read arguments — even `--help`
  started the server.

  The server STILL starts when run without arguments, and in no other way;
  forward migration stays automatic at startup, and there is deliberately NO
  `migrate up`, because a separate command would bring back the "I forgot to
  update the schema" class.

  Rolling back CANNOT BE UNDONE, so it has a gate: nothing runs until the
  owner name is typed a second time with `-confirm <owner>`, the default step
  count is 1, and a DIRTY ledger (a previous run left halfway) is refused even
  with confirmation — rolling back a dirty state damages, by one more step, a
  schema of which nobody knows which half was applied.

  The source list is not a SECOND list: the command collects the migrations
  from the same place the modules register them, so the set the server applies
  and the set the command sees cannot diverge.

  **A known and measured hazard is written in the godoc:** golang-migrate
  takes the advisory lock with `context.Background()`, so neither a deadline
  nor Ctrl-C interrupts the wait. Measured: a `Version()` call whose context
  expired after 5 seconds had still not returned after 15 seconds while
  someone else held the lock. This holds on the STATUS path too (reading the
  version creates the missing version table, which also takes the lock), so a
  `migrate status` run while a deployment's forward migration is in progress
  can wait silently.

- **The total COUNT of the storefront listing is now optional**
  (`GET /store/v1/products?with_count=false`; in GraphQL, not selecting the
  `count` field is enough). The default DID NOT CHANGE: a request without the
  parameter receives the same bytes as today.

  The count was made optional because it could not be made cheaper, and that
  is the result of a measurement: the channel filter runs one subquery per
  product (`SubPlan`, `loops=52004`) and the query itself is already on the
  index — the `EXPLAIN` output shows `Heap
  Fetches: 0`. So the set to be walked cannot be shrunk; it can only be left
  unwalked. Measured (52,004 products, LIMIT 20, median): the list service
  with counting **67.00 ms**, without **0.65 ms**; the count itself 64.07 ms.

  When the count is skipped, the `count` field is **ABSENT** from the
  envelope — it does not return `0`, it does not return `null`. `0` would lie
  ("no results"), and `null` would have meant loosening `Int!` in the GraphQL
  schema; the field's absence says the same thing on both surfaces: not
  counted.

  Plus a defect fixed for free: in GraphQL, a query that did not select the
  `count` field at all still ran the count SQL. The selection set is now
  consulted (`@skip`/`@include` included).

  The 79 ms in the README's "Known limits" was stale; it was re-measured in
  this round and the line updated. The plan's "consistent envelope" sentence
  was also corrected to say that the count can be dropped — field names and
  types do not change; only a count that was not computed is left out of the
  envelope.

### Changed

- **An abandoned saga's compensation now runs FROM THE RECORDS**
  ([ADR 0017](docs/adr/0017-recovering-abandoned-sagas-from-the-record.md)).
  When the process died in the middle of a saga, compensation never ran: the
  reserved stock, the opened order and the payment session were left hanging,
  and as the README put it, "there WAS NO automatic recovery". The obstacle
  was that `StepContext.Shared` is not persisted — compensation reads the
  answer to "which reservation do I cancel" from there.

  Measured: that answer is NOT lost. The steps' Invoke outputs are persisted
  and the compensation record does not delete them (the `StepRecord.Output`
  godoc already states this as a decision). The only thing missing was turning
  the JSON back into a typed value, and only the step itself knows how to do
  that: the new `workflow.Recoverable` interface does it. A chain with a step
  that does not implement it gets today's behavior, so the interface adds a
  capability and breaks no contract. When recovery completes, the record
  becomes `failed` and RELEASES its key — the customer can pay for the same
  cart again.

  **Recovery deliberately STOPS at one point, and that point is payment.** The
  engine writes the step record AFTER Invoke returns, so a process that dies
  inside the capture leaves no trace; if recovery counted that step as "never
  ran", the stock of a customer whose card was charged would be released,
  their key freed, and the customer charged a SECOND TIME. Such a step is
  marked with `workflow.RecoveryBlocker`, and while it has no record it also
  blocks recovery of the steps before it. The result for `complete_cart`:
  three of the four crash points are recovered; the capture point remains a
  matter for manual intervention.

  Recovery is not triggered, it is encountered: a caller returning with the
  same key finds it. A scheduled sweeper was deliberately not added — recovery
  runs work that has side effects.

- **The workflow engine ITSELF was translated into English**: `workflow.go` —
  the package comment (saga contract, compensation rule, idempotency key,
  persistence policy), the `Step`/`Recoverable`/`RecoveryBlocker` interfaces,
  `Executor` and all of the engine's internal routines. The ledger went down
  from 716 files to 715; no Turkish is left in the package's PRODUCTION code,
  and the remaining five files are all tests.

  Two TEST BINDINGS broke and were fixed where they broke — both were bound to
  the message TEXT, so the compiler did not see them and they surfaced only
  when run: `workflow_test.go` checked that the engine's own sentence was not
  lost by searching for `"b" adımı`, and `pgstore_integration_test.go`
  searched for the recovery refusal as `ELLE MÜDAHALE` in FOUR places. This is
  the recurring finding of the translation rounds: assertions bound to message
  text are the one binding through which a translation is noticed, and only by
  running the tests.

  The translation also had its own hazard class, met in three places: the
  order of elements in a Turkish sentence CHANGES in English, so the
  `%q ... %d ... %q` operands have to be reordered too (the Turkish word order,
  "%q workflow's %q step (%d) failed", becomes "the %q step (%d) of the %q
  workflow failed"). Swapping two operands of the same type is invisible to
  the compiler; `go vet` stays silent too, since they share a type. All three
  were reordered by hand along with the translation, and the suite was run.

  Behavior did not change: the error CODES (`workflow_step_failed`,
  `workflow_recovery_failed`, …) and the values of the status constants are
  the same — they are the machine contract. Only the human-readable text
  changed.

- **The workflow engine's contract files were translated into English**:
  `store.go` (the Store interface, status constants, record types),
  `options.go` (the RunOptions and the retry policy), `memory.go` and
  `parallel.go`. The ledger went down from 720 files to 716.

  The translation uncovered A STALE SENTENCE, which was fixed: the type godoc
  of `ParallelStep` said "Compensate calls all branches IN REVERSE ORDER and
  ONE AT A TIME", whereas the implementation runs the branch compensations
  CONCURRENTLY, and the reason for that is written in another godoc of the
  same file (sequential execution called later branches with a dead context,
  because a slow branch used up the shared budget). The two sentences
  contradicted each other; the English text states what the implementation
  does. The same godoc held a SECOND copy ("the inner rollback runs
  sequentially and in reverse branch order") — the inner rollback takes the
  same concurrent path too; that was fixed as well.

- **The PRODUCTION files of `internal/core/workflow/pgstore` were translated
  into English** (ADR 0012's ratchet): `pgstore.go`, `convert.go`, `sql.go`,
  `ids.go`, `migrations.go` and two migration SQL files. The ledger went down
  from 727 files to 720; the three files in the package that still carry
  Turkish are all test files.

  Behavior did not change, but one TEST BINDING broke and was fixed where it
  broke: the table testing the error mapping searched the primary-key
  violation's message for the word "kimlikli" ("with ID"). This was the only
  assertion bound to the repository's own message TEXT; since there is no
  compiler link between code and message, it is seen only when run.

  Only the COMMENTS of the migration files changed; the DDL was not touched,
  and since golang-migrate applies files by version number, an
  already-migrated database is not affected.

- **`core/link` and `core/eventbus` were translated into English**
  (ADR 0012's ratchet). 15 lines DROPPED from the Turkish ledger: from 742
  files to 727; the path ledger stayed at 38 (the two packages had no path
  entries at all). The count of Turkish letters in the two packages is zero.

  The translation did not change behavior, and that claim was tested
  structurally: an AST comparison that drops comments and normalizes strings
  and identifiers shows five of the six production files as IDENTICAL. In the
  sixth, the operand order of two format strings changed — a Turkish sentence
  orders its elements differently from English — and that order sat where no
  gate could see it: `go vet` sees nothing among three operands of the same
  type. The order is now pinned by an integration test, and the test's
  fixture was measured too: with a VIEW whose name collides, the DDL fails one
  step earlier, whereas with a MATERIALIZED view it completes "successfully"
  and reaches the check — so that is the silent shape.

  The error CODES did not change (identical in five production files), and
  the KEYS of the error details are now tested too: both the presence and the
  VALUE of the `stored` key are pinned — an assertion testing only its
  presence let through an error that reported the incoming definition instead
  of the stored one, and the operator would have seen the two definitions as
  identical.

- **Cart line totals are written in a SINGLE statement; the cart's lock is no
  longer held for a time proportional to the number of lines.** The
  calculation pass ran one UPDATE per line, and did so under the cart's
  `FOR UPDATE` lock; since the lock serializes every flow writing to that
  cart, the duration was directly the cart's write capacity. Measured
  (100-line cart, from taking the lock until the last write returns, p50):
  UPDATE per line **8.0 ms**, single statement **0.55 ms**; at 10 lines
  0.28 ms, so it barely grows with the number of lines.

  The measurement must be read honestly, and the godocs now say so: the test
  harness's container runs with `fsync=off`, so these numbers are the WRITE
  PHASE, not the WAL flush of the commit that follows. The flush is under the
  same lock too, and this change does not touch it — measured on a durable
  cluster, 6.2 ms regardless of line count. So the lock time an operator will
  see drops from ~14.2 ms to ~6.8 ms: **~2x**, not the 14x within the write
  phase itself.

  Pipelining (pgx batch / sqlc `:batchexec`) was deliberately REJECTED, and
  the reason is a number: the same 100 UPDATEs take 3.0 ms in a single
  pipeline, so only two thirds of the gain. The remaining difference is the
  per-statement parse/plan cost, and only cutting the statement count to 1
  removes it.

  The total–line pairing is protected by the shape of the API: the ID travels
  in the SAME value as its totals (`LineItemTotals`), so a caller cannot pass
  two separate slices in different orders. A pass that writes incompletely
  does not go by silently — an ID that does not match (a deleted line, another
  cart's line) fails the pass, and the error names the FIRST line that could
  not be written, in the caller's order.

### Fixed

- **The engine could return SUCCESS without running a single step.** The
  engine tried to open an execution for at most two rounds; if the second
  round also got the answer "abandoned, try again", the loop ended, and at
  that point the return value of `replay` was `(nil, nil)` — and that value
  was handed to the caller as is. Measured: `out=<nil> err=<nil>
  invokes=0`. The caller reads a nil error as "order placed"; in the cart flow
  that means a success response for which no order was opened — the worst lie
  a saga engine can tell.

  The error after the loop is now actually returned; its class is
  `KindUnavailable` (503) and its new code `workflow_execution_contended`: the
  system is not broken, the key is contended, and since no step ran, the
  caller can retry with the SAME key. In production this state is reached
  through two abandoned executions in a row, or through a `WithLease`
  declared shorter than the real saga duration.

- **A race that resolves itself returned 500.** If, after `Create` said "key
  taken", the read says "no such execution", the key was released BETWEEN the
  two calls — a compensated execution releases its key, and so does every
  caller that closes an abandoned record. The engine wrapped that read as
  `workflow_store_failed`, so the customer got a 500 because of a race that
  resolves itself (measured: when four concurrent callers reached the same
  abandoned record, one got exactly this error). It now tries to OPEN again;
  the key is already free.

  Both faults were found in the same hunt, by measuring the concurrency of the
  recovery path added after v0.7.0; both were proven by mutation (when the old
  behavior is put back, the tests fail one by one).

- **Recovery became EXCLUSIVE: an abandoned record is now compensated by a
  single process.** Since an abandoned record is owned by no one, every caller
  returning with the same key found it and ALL of them ran the compensation
  chain — measured with four concurrent callers, the chain ran four times. The
  engine now claims the record BEFORE recovering: a single conditional UPDATE
  that succeeds only while the record is still `running` and `updated_at` is
  the value the "this is abandoned" decision was based on. The winner stamps
  `updated_at`; that both eliminates the others and refreshes the lease while
  recovery lasts. Measured: the same four callers, ONE compensation (it goes
  back to four when the claim is removed).

  The claim is taken AFTER the step records are read and before the first
  write. Reading has no side effects; a won claim, however, stamps
  `updated_at`, which extends the lease. Had the claim come first, a caller
  that cannot read the steps — and so does nothing — would have silently
  pushed the record's lease forward, and a genuinely half-done saga would have
  been hidden from both the next caller and `gobit stuck` for a full lease
  period.

  A caller that LOSES the claim is not told "still in progress"; it is sent
  around the loop once more: the winner may release the key at any moment, and
  the second round answers both outcomes correctly — if the key is free, a new
  execution is opened; if the winner is still working, the record found is
  FRESH, so "still in progress" is then true.

  The capability is an OPTIONAL interface (`workflow.ClaimingStore`), not a
  method added to `Store`: a port method would break every Store
  implementation written outside this repository. The cost is that a wrapper
  that EMBEDS `Store` silently hides the capability (an embedded interface
  carries only its own methods) — the reasoning and the limit are in
  [ADR 0017](docs/adr/0017-recovering-abandoned-sagas-from-the-record.md).

- **That compensation may be called CONCURRENTLY is now written down**
  (behavior did not change). Since an abandoned record is owned by no one,
  every caller arriving with the same key recovers it; measured with four
  concurrent callers, the chain ran FOUR times. Until now the `Step` contract
  only said "may be called twice", and that meant ONE AFTER THE OTHER. For the
  repository's own steps the cost is duplicated work and duplicated provider
  calls (every compensation undoes BY ID), but a plugin step that writes its
  compensation as read-modify-write releases stock more than once. The
  contract now forbids that explicitly — and in the same unreleased round the
  door was closed too: recovery became exclusive (see the entry above). The
  prohibition stays anyway, because the capability that establishes
  exclusivity is optional, and a step CANNOT SEE whether the Store beneath it
  offers it.

## [0.7.0] — 2026-09-03

### Breaking changes

All three are legitimate in a minor release throughout `0.x` (see the top of
the file), and all three are things **to check when upgrading**.

- **`/ready` NO LONGER returns 503 for every dependency.** While Redis is
  unreachable the endpoint returns `200` with `"status": "degraded"` in the
  body; only a dependency that CUTS service, like Postgres, produces `503`. An
  installation that alerts on 503 will no longer SEE a Redis outage that way —
  the signal is the `degraded` field in the body and the WARN line written for
  every failing check. The reason for the change and its measurement are
  below; the decision itself is in
  [ADR 0007](docs/adr/0007-sertlestirme-arizada-davranis.md).
- **A cart carries at most 100 distinct lines.** A request to open a NEW line
  on a cart that has reached the ceiling gets `400` and
  `cart_workflow_line_limit_reached`. Increasing the quantity of an existing
  line is exempt; larger carts opened BEFORE the ceiling remain calculable and
  payable, they just cannot take new lines.
- **The ORDER of search results changed.** The result SET is the same; in a
  multi-word query, field weight (title > keywords > description) now beats
  word proximity. Clients with screenshot tests that depend on the order are
  affected.

For installations that embed the framework (Go), three signatures changed:

- `NewMemoryIdempotencyStore` now also takes the byte budget (`ttl, butce`).
- The type of the `RouterOptions.ReadinessChecks` field became `GatingChecks`,
  and `DegradedChecks` arrived beside it. An unnamed map literal can still be
  assigned; a caller passing a VARIABLE of a named `map[string]HealthCheck`
  type will not compile — and that is deliberate: keeping the two classes from
  mixing depends on it.
- `AddLineItem` was removed from the `cart/api.Carts` interface. The service
  method itself remains; adding a line goes through the flow.

### Added

- **The in-memory idempotency store grew WITHOUT BOUND; it now has a byte
  budget** (`IDEMPOTENCY_MAX_MEMORY_BYTES`, default 64 MiB). The store keeps an
  entry, response body included, for every mutating request, the CLIENT
  chooses the key that opens the entry, and the only limit was a 24-hour TTL.
  Measured (runtime.MemStats, after GC): 10,000 entries with 1 KiB bodies held
  15.51 MiB, 10,000 entries with 64 KiB bodies 630.69 MiB, 1,000 entries with
  1 MiB bodies 999.58 MiB; with 50,000 entries written and the clock advanced
  23 hours, the number of entries dropped was ZERO — the TTL stopped the
  growth nowhere. The `GUARD_BACKEND` default is `memory` and `Validate` does
  not require `redis` in production, so an ordinary production deployment runs
  this store.

  When the budget is full, the OLDEST entry is dropped. Refusing was worse:
  since the client chooses the key, a single client arriving with made-up keys
  could shut off all of the shop's mutating traffic — a memory fault would
  turn into an availability fault that costs nothing to trigger. The cost of
  dropping is that a retry arriving with that key is processed again, and that
  is the same cost the TTL already pays; eviction only brings that deletion
  FORWARD, and the oldest entry is the one with the least protection left. It
  is not silent: the first eviction is always logged at WARN, later ones once
  a minute; the budget is written at every startup; the README and
  `docs/mimari.md` name the limit.

  Entries now also sit, beside the map, in a list ordered by expiry. The old
  expiry pass scanned the WHOLE map, and the scan ran while holding the
  process's SINGLE idempotency lock: 50.3 ms at 1,000,000 entries, 2.13 ms at
  100,000. Now only the expired PREFIX is walked: 188 ns and 164 ns at the
  same two map sizes. This also made unnecessary the workaround that throttled
  the scan to once a minute — that throttling meant an expired entry kept
  being REPLAYED for up to a minute, i.e. protection longer than the TTL says.
  The response copy and the accounting were moved OUTSIDE the lock:
  concurrent replay with 1 MiB bodies went down from 50.1-52.7 µs to
  34.5-40.8 µs.

  **The smallest accepted budget was raised from 1 MiB to 2 MiB.** The floor's
  rationale was "a single maximum-size response must fit", but at 1 MiB it did
  not fit, and this was measured: a 1 MiB response written into a 1 MiB budget
  is dropped immediately, because an entry's cost carries the key, the
  fingerprint and structural overhead besides the body. So the floor ACCEPTED
  exactly the silently non-functional configuration it was meant to forbid.
  The test that checked equality with a constant was replaced by one that
  tests the behavior.

- **The PostgreSQL pool's limits became configurable** (`DB_MAX_CONNS`,
  default 10; `DB_MIN_CONNS`, default 2). The number was hard-coded and no
  environment variable could change it; yet the pool is the database
  concurrency ceiling not of A SINGLE request but of the WHOLE PROCESS — HTTP
  requests, the workflow engine and the event consumer all draw from the same
  pool.

  The overlooked side of the ceiling is in GraphQL: gqlgen resolves root
  fields concurrently and does not limit how many, so with
  `GRAPHQL_MAX_FIELD_REPETITION=20` a single legitimate storefront document
  can open 40 concurrent reads. Measured (52,000 products, real storefront
  queries, 40 concurrent root fields): with 10 connections, 771 of 813
  acquisitions wait, average wait 65.3 ms.

  The default nevertheless STAYED at 10, and the reason is a measurement: when
  the database is on the same box as the application, the bottleneck is the
  server's CPU, not the pool, and raising it does not win latency back (p50
  306 ms → 368 ms). When the database is across a network it pays off, and the
  gain depends on the root field's number of round trips: on the list path
  1.3x at a 5 ms hop (459 → 348 ms), 1.8x at 20 ms (638 → 351 ms); 3.8x on the
  three-round-trip single-product field (69.2 → 18.0 ms). So what was missing
  was not the number but the KNOB — raising the default would multiply every
  installation's cluster connection budget, while the gain falls only to
  latency-bound topologies.

  That the limits actually REACH the pool is tested and pinned from both ends:
  the pool opens with 1 connection and answers (this was the remedy the godoc
  recommended for an installation connecting many instances to a shared
  cluster, and until then it was only a structural claim), and a ceiling of
  250 also passes through unchanged. Both guard against silent mutations: a
  floor of the form `max(cfg.MaxConns, 4)` or a ceiling of 64 would agree with
  a test written with 4 and run a different pool from the one the startup log
  reports.

- **`pool_*` parameters in `DATABASE_URL` now produce a WARNING at startup.**
  pgxpool reads them, but because the application overwrites the pool fields
  from its configuration, `?pool_max_conns=40` did nothing — silently. That
  was harmless while the pool was hard-coded; from the moment `DB_MAX_CONNS`
  exists, there are two reasonable places where an operator might write the
  same number, and one of them does nothing. Warning rather than refusing is
  right: stopping a process that has been starting up for as long as the
  parameter has been ignored is a bigger cost than the surprise it prevents.

- **A line-count CEILING on carts: 100** (`cart.MaxLineItems`). A request to
  open a NEW line on a cart at the ceiling is refused with `400`, not `409`,
  and the code `cart_workflow_line_limit_reached`; the message states both the
  ceiling and the number of lines in the cart. There is NO truncation.

  The reason was measured: every request that adds a line REWRITES the totals
  of all of the cart's lines (the cart module's `SetTotals` runs one UPDATE
  per line, under the cart's lock), so building a 100-line cart costs 5,050
  line writes, and a 1,000-line cart 500,500 writes. A cart without a ceiling
  left unbounded the time a single client could keep the database busy.

  The ceiling is enforced only on the path that OPENS a line: adding again a
  variant already in the cart increases the quantity and does not hit the
  ceiling — if it did, the owner of a full cart could not even increase the
  quantity of their own line. The calculation pass, the quantity update and
  the order path never consult the ceiling, because a cart opened before the
  ceiling was introduced and carrying more than 100 lines today must remain
  calculable and completable. The ceiling is a GATE, not a hard upper bound:
  the comparison looks at a snapshot taken outside the cart lock, so two
  concurrent additions can overshoot by a few lines.

  For the sake of the "single gate" claim the ceiling relies on, `AddLineItem`
  was REMOVED from the `cart/api.Carts` interface (breaking; the service
  method itself remains and the flow calls it). The method had no callers, but
  its presence on the interface left the door open for a handler wired to it
  to silently bypass both server-side pricing and the ceiling — `CreateCart` is
  absent from that interface for the same reason.

- **`pricing.interop` publishes a bulk price surface**
  (`CalculateAmountsJSON`, item ceiling `MaxCalculateItems` = 1000). It
  preserves the request order, returns a per-item "priced" FLAG (not an
  error), and does not fail the whole request because of an item without a
  price. If the ceiling is exceeded, the request is refused as a whole;
  truncating would mean leaving part of the caller's cart unpriced while
  reporting the result as "successful". It was measured that the plan
  switches from the index to a full scan between 280 and 300 items, and this
  is written in the constant's godoc — up to 1000, the cost is not linear.

- **A WARNING at startup if `SHUTDOWN_TIMEOUT` is shorter than the saga
  budget.** The defaults are 15 seconds and 2 minutes, so an ordinary deploy
  can cut an in-flight payment off in the middle. Neither is wrong — 15
  seconds is a reasonable deploy budget (Kubernetes' default grace period is
  30 seconds), and 2 minutes is a reasonable ceiling for a chain that passes
  through three modules and a payment provider. What is wrong is an
  installation NOT KNOWING which one it has chosen.


- **That PostgreSQL is a FOUNDATION, not an OPTION, is now written down**
  ([ADR 0015](docs/adr/0015-postgresql-cluster-contract.md)). gobit does not
  support PostgreSQL, it is written ON TOP of it — and until now that
  dependency stood nowhere as a contract. Exactly because of that, a blocker
  surfaced in this round: the cluster was being created with `--locale=C`, and
  none of the fourteen ADRs mentioned locale.

  The ADR LISTS where the dependency lives — the N+1-free read layer built on
  array parameters (`= ANY`), the partial unique indexes that are the business
  rule itself (`UNIQUE (handle) WHERE deleted_at IS NULL`), `jsonb`,
  `timestamptz` (235 columns), advisory locks, and the DDL that `core/link`
  runs at EVERY STARTUP — then sets out in a table what the cluster must
  provide: version, encoding, CTYPE, extensions (ZERO today), privileges,
  `search_path`.

  The contract is enforced by a PROBE, because in a real deployment fixing the
  compose file is not enough: on RDS/Cloud SQL/Neon you do not choose the
  `initdb` arguments. The probe tests BEHAVIOR, not NAMES, and **today it has a
  single check** — that is a decision, not a gap: every other row in the table
  fails LOUDLY (the insert is refused, `link.Define` blows up, the query says
  "relation does not exist"); only case folding fails by returning a valid,
  empty and silent answer.

  A second database will not be supported, and the reason is not ideological:
  the first three items on the list are not portable, and a second dialect
  would also break the repository's "every rule defined in ONE place"
  discipline for every invariant.

### Changed

- **A Redis outage took ALL replicas out of traffic at once.** Until now
  `/ready` knew a single class of check: if one failed, 503. Since
  `GUARD_BACKEND` is `redis` in every multi-instance installation, Redis was in
  that set, and during a failover every pod went NotReady in the same second —
  Kubernetes emptied the Service, no healthy replica was left to shift traffic
  to, and a partial degradation became a full outage. This is one layer above
  the "fail-closed for everything" option that ADR 0007 REJECTED for the
  protection layers; the ADR was extended with that section.

  Checks now come in TWO CLASSES: if a `ReadinessChecks` check fails, 503 and
  the instance leaves traffic (Postgres); if a `DegradedChecks` check fails, it
  is reported in the body but the code stays 200 (Redis). The `status` in the
  body takes three distinct values — `ok`, `degraded`, `unavailable` — because
  the 503 used to say "degraded" as well, and the two states could not be told
  apart from a log.

  Putting Redis on the degrading side was MEASURED (`GUARD_BACKEND=redis`,
  Redis down): storefront catalog read 200, a write without `Idempotency-Key`
  200, a write carrying it a per-request retryable 503
  (`idempotency_store_unavailable`). No request is processed wrongly — the
  only class that cannot be protected is the only class refused. Making it a
  gate would have taken the requests that return 200 down with it.

  The two classes also have SEPARATE Go types (`GatingChecks`,
  `DegradingChecks`): moving a dependency to the other side is a one-word edit
  that looks innocent in review and passes every test. Since an unnamed
  `map[string]HealthCheck` can be assigned to both, it is also tested
  separately that the composition root does not use that type
  (`TestReadinessMapsUseTheNamedTypes`) — verified by mutation: a version
  using an unnamed map put Redis back on the gate side and no test in the
  repository failed.

  Degrading checks have a separate and SHORT budget (default 250 ms,
  `READINESS_DEGRADED_TIMEOUT`): a single Ping to an unreachable Redis takes
  1.7 seconds (the client tries five times), and the kubelet's default probe
  timeout is 1 second — a "degradation" check with no budget would fail the
  probe and bring the same outage back through the back door. A budget overrun
  names the budget in the body; but the budget has a cost, and it is written
  in the godoc: it destroys the root cause — "connection refused" and a DNS
  error collapse into the same sentence.

  Every failing degrading check logs a WARN, and the line says the instance
  CONTINUES TO SERVE: since the code stays 200, it produces no event in the
  orchestrator, so that line is the degradation's only alerting channel. If
  the same name is registered in both classes, the gate side wins, and that
  too is reported once at startup as a warning.

- **The price reads of building a cart grew QUADRATICALLY; they are now
  linear.** Every request that adds a line reprices ALL of the cart's lines and
  issued two queries per line to pricing, so building an N-line cart took
  ~1.5N² round trips. Measured (with the package's own fakes, counting calls):

  | cart | price calls (old) | (new) | SQL queries (old) | (new) |
  |---|---|---|---|---|
  | 10 lines | 65 | 20 | 130 | 40 |
  | 50 lines | 1 325 | 100 | 2 650 | 200 |
  | 100 lines | 5 150 | 200 | 10 300 | 400 |

  The calculation pass now uses pricing's BULK surface
  (`service.CalculateAmountsJSON`): two queries regardless of the number of
  price sets. The bulk read itself already existed
  (`ListPriceCandidatesBySets`) and had never been wired into the calculation
  path. The query itself was also measured with real data (54,000 price
  sets): for 50 sets, the per-set path 4.93 ms, the bulk path 0.25 ms; for 100
  sets, 9.88 ms and 0.33 ms.

  The AMOUNT selected does not change, and that claim is pinned by a test
  (`TestCalculateAmountsJSONMatchesCalculateAmount`): both paths run pricing's
  same pure selection function with the same candidate rows. The only
  difference is that the bulk path reads the clock ONCE, and the difference
  favors the bulk path — a campaign ending at that very moment cannot price
  two lines of the same cart from different instants.

  The single price asked for while a line is being OPENED is still asked with
  the single-item method: measured, on a single set the bulk path has NO
  advantage (candidate query 66 µs against 77 µs), and the single-item method
  gives a more precise "no price set" error.

- **ALL lines without a price are reported in a single error.** Since the
  bulk response carries every line at once, returning at the first unpriced
  line would throw away information already at hand: the owner of a cart with
  two dead variants learns about both in this request, instead of repairing
  the cart one request at a time. The error class and code did not change
  (`Invalid`, `cart_workflow_price_unavailable`); if only one line is
  unpriced, the message is exactly as before.

- **Search ranking uses `ts_rank` instead of `ts_rank_cd`, and the ranking
  query is now computed once per query.** The storefront's search endpoint
  must score EVERY matching document (a GIN index cannot satisfy `ORDER BY`),
  so the per-row cost of the scoring function is directly the endpoint's cost.
  Measured (an index of 52,000 documents, documents of ~92 lexemes, LIMIT 20):

  | matches | ts_rank_cd | ts_rank | match only |
  |---|---|---|---|
  | 1 002 | 13.7 ms | 1.4 ms | 1.1 ms |
  | 10 400 | 148.0 ms | 23.0 ms | 21.7 ms |
  | 52 000 | 663.0 ms | 24.7 ms | 23.8 ms |

  The difference is `ts_rank_cd`'s cost of ~12 µs per document, and the
  planner CANNOT SEE it: `pg_proc.procost` is 1 for both functions. A single
  word occurring across the whole catalog, at the default quota of 600
  requests/minute, burned 6.6 core-seconds per second.

  The ranking changed OBSERVABLY: `ts_rank_cd` could place word proximity
  ABOVE field weight; `ts_rank` cannot. For the query "mavi gomlek" ("blue
  shirt"), a product carrying the two words side by side in its keywords field
  (B) came before a product carrying both in its title (A); now the title
  wins. That is why the index is split into weights, so this is the fix.
  Proximity did not disappear entirely — measured, as the gap between the two
  words grows from 0 to 6 the score falls from 0.9910 to 0.7615 — it just can
  no longer beat weight.

  Ranking uses **the positive part of the query** (`querytree`): `ts_rank`
  gives EVERY document 0 for a query carrying a negation, so a shopper typing
  `gomlek -mavi` would have received results in indexing order rather than by
  relevance — even though support for `-` was counted as the reason for
  choosing `websearch_to_tsquery`. A query made only of exclusions (`-mavi`)
  leaves no positive signal to rank by; in that case the order is
  `product_id`, and this is written in the README's "Known limits" section.

  The ranking expression is a scalar subquery. After the sixth execution pgx
  may switch to a generic plan, and in a generic plan the expression would not
  be folded into a constant but re-parsed per row: 46.7 ms against 25.4 ms at
  52,000 matches.

- **The storefront's sales channel visibility rule was reduced to a single
  correlated subquery.** The rule DID NOT CHANGE; how it is written did. The
  old form was two independent subqueries ("has no assignment at all OR has
  an assignment in the requested channel"), and the comment in
  `saleschannel.go` claimed that one index probe was made per candidate row.
  The claim was wrong: when the planner sees two independent EXISTS it turns
  both into hashes, so it scans the whole link table twice BEFORE returning
  the first row.

  Measured — 52,000 products, 52,000 channel assignments, real Postgres, the
  storefront's `GET /store/v1/products?limit=20` endpoint:

  | | old | new |
  |---|---|---|
  | list query | 26.80 ms | **0.14 ms** |
  | count query | 73.87 ms | 78.97 ms |
  | total SQL in the request | 100.7 ms | 79.9 ms |

  The cost grew with CATALOG size, not page size — and on the storefront's
  hottest endpoint at that: the same endpoint took 7.5 ms with 2,000 products
  and 113 ms with 52,000, both returning the same 20 rows.

  The `IS TRUE` in the new formulation is NOT decoration: without it, when the
  channel array carries a NULL element, `bool_or` swallows the NULL,
  `COALESCE` takes it for "has no assignment at all", and an assigned product
  becomes VISIBLE in the wrong channel — so the form without it fails open.
  Measured across eight scenarios. And no test can catch this, because the
  channel array comes from Go as `[]string` and cannot produce a NULL element;
  the reasoning is written in the code.

- **The count's cost is now written down as a LIMIT** (README, "Known
  limits"). With the channel filter, the storefront listing's total count has
  to look at the whole catalog, and that is not something that can be fixed:
  on the same catalog a plain count without the filter takes 2 ms, a count
  with the channel filter 79 ms.

### Fixed

- **A payment interrupted midway locked the cart FOREVER.** An execution
  record is opened as "running" and closed by moving to a terminal state; if
  the process dies before it can write that transition (deploy, OOM, pod
  eviction), the record stays running forever. Measured: an execution that
  crashed three days earlier still said *"still in progress"*, and that cart
  could never be paid for again.

  The engine now accepts a LEASE duration (`workflow.WithLease`): the caller
  declares how long its flow can legitimately take, and a record that stays
  running longer than that is a record no process can be holding. Age alone
  is not proof, the lease is — that is why the duration is not guessed by the
  engine but declared by the caller.

  What to do with an abandoned record is decided by looking at the STEP
  RECORDS, and both branches are tested:

  - **If no step did any work**, there is nothing to compensate: the record
    becomes `failed`, releases its key, and the customer can pay for their
    cart.
  - **If work was done**, compensation never ran and half-done work is left
    hanging: the record becomes `compensation_failed`, KEEPS its key, an ERROR
    is logged and the caller says "manual intervention required". Silently
    retrying would reserve already-reserved stock a second time.
  - **If the steps cannot be read**, NO decision is made; the record is left
    as it is. The two mistakes do not cost the same: a late decision keeps the
    customer waiting, an early one releases the key of a running saga and
    double-books the stock.

  The `complete_cart` lease is 10 minutes: the theoretical upper bound is
  2 min + 5×30 s = 4.5 minutes, and the margin is deliberately more than
  double that.


- **A failed payment broke the cart PERMANENTLY.** A customer whose card was
  declined — something that happens in one of every ten payments on a real
  storefront — could never pay for that cart again. Measured:

  ```
  1) manual_outcome=decline  -> payment_authorization_declined   (the saga compensated)
  2) retry with a valid payment -> 409 workflow_execution_failed
     "...it failed before and was compensated; to try again, use a NEW
      key"
  ```

  The advice had no counterpart on the HTTP surface either: the key is
  DERIVED from the cart ID (`complete_cart:<sepet>`), so there is no field
  where the customer could supply a new key. The cart stays, contents and all,
  but cannot be bought; the customer has to build the cart from scratch.

  The defect was in the meaning: in this engine `StatusFailed` does not mean
  "failed" but **"failed and compensation completed IN FULL"** — that is, the
  attempt left no trace in the world. The key is a trace too. Moving to that
  state now RELEASES the key (without deleting the record; the failed attempt
  remains as an audit record), and the same cart can be paid for again.

  The boundary is drawn on both sides and tested: `completed` does not release
  the key (otherwise the same cart would be charged twice), and neither does
  `compensation_failed` (otherwise a new attempt would pile on top of
  half-done work awaiting manual intervention). The release happens in the
  SAME statement as the status write: a process that died between two
  separate writes would leave the key held forever, bringing back the fixed
  fault as a rare race.


- **SEARCH SILENTLY DID NOT WORK IN TURKISH.** `deploy/docker-compose.yml`
  created Postgres with `--locale=C`, and the C locale folds only ASCII
  letters. The result: a customer searching for `"çanta"` could NOT FIND the
  product titled `"Çanta"`. No error, no log, no metric — the search box
  returned an empty list.

  This was NOT a plugin problem: the storefront's own filter
  (`title ILIKE '%' || $q || '%'`) depends on the same setting, so it was
  broken in an installation with no plugin installed as well. Measured on a
  real server:

  ```
  GET /store/v1/products?q=çanta   -> 0 results
  GET /store/v1/products?q=Çanta   -> 1 result
  ```

  The fix is `--locale=C.UTF-8`. Three setups were measured on the same image:

  | initdb | `ILIKE` | `to_tsvector` |
  |---|---|---|
  | `--locale=C` (old) | ✗ | ✗ |
  | `--locale=C.UTF-8` (new) | ✓ | ✓ |
  | `--locale-provider=icu` | ✓ | **✗** |

  That ICU only gets halfway matters: it fixes `ILIKE` and leaves the search
  index broken, so it produces an installation that looks fixed. C.UTF-8 does
  not cost the sort order either — comparison is still byte order; only case
  folding changes.

- **This is now tested at startup** (`core/db/casefold.go`). After the pool
  opens, the database is asked two questions — `'Ç' ILIKE 'ç'` and a
  `to_tsvector`/`websearch_to_tsquery` match — and if even one fails, a
  WARNING is logged saying which search path is affected and what the remedy
  is. Startup is NOT STOPPED: an all-ASCII catalog works fine under the C
  locale, and refusing those installations would be wrong.

  The locale NAME is not read; the BEHAVIOR is tested: the name is a proxy,
  and an unexpected but correct locale would be misreported. Both halves are
  tested, because they diverge on an ICU setup — a check looking only at
  `ILIKE` would give that setup a clean report. Since the locale is fixed AT
  initdb TIME, an existing data directory keeps its old setting; the warning
  says so, and that a dump/restore is required.

### Security

- **On the storefront, a shopper could get someone else's CART.** The
  idempotency record is namespaced by the caller's identity; but the identity
  resolved on `/store/v1` is the SHOP's, not the shopper's — the publishable
  key is the same in every browser and is not secret anyway. So all customers
  share ONE bucket, and what selects the record is a header the client
  chooses.

  Measured, not inferred: two independent callers, `Idempotency-Key: cart-9`,
  the same body → **both received the same cart ID**, and the second one's
  response carried `Idempotency-Replayed: true`. Since the cart has no
  ownership check (README, "Known limits"), this means handing a stranger
  someone's cart: its contents, email, address, and the right to complete it.

  The storefront escaped this on most endpoints, because the fingerprint also
  includes the PATH, and cart-scoped endpoints have the cart ID in their path
  — a second customer using the same key on their own cart gets 409. The leak
  was exactly on the one endpoint that CARRIES no capability in its path and
  PRODUCES a capability in its response: `POST /store/v1/carts`.

  That endpoint is now EXEMPT from the idempotency ring. The cost is plain: a
  client retrying a create request that timed out opens two carts, and one is
  abandoned. Money, stock and anything visible to the customer are
  unaffected. Since the exemption works by EXACT PATH match,
  `/carts/{id}/complete` stays protected — that is the endpoint that produces
  a double ORDER.

  An e2e test pinned this behavior the OTHER WAY ROUND ("the same key produces
  one cart"), and its assertion was reasonable on its own; what was wrong was
  the assumption that the record could tell callers apart on the storefront.
  The test was rewritten to state the new contract and the leak it closes. The
  e2e setup also now USES production's exemption list: since it used to build
  its own list, deleting the line in production failed no test.

## [0.6.0] — 2026-09-03

### Added

- **Error reporting: the contract in the core, Sentry in a plugin**
  ([ADR 0014](docs/adr/0014-error-reporting.md)). `provider.ErrorReporter`
  lives in the core, its implementation in `plugins/errorsentry`. The feed is
  the **log**: every failure already writes at ERROR, so wrapping the log
  handler through `logger.Options.Middleware` closes three doors at once
  (WriteError, Recoverer, a direct ErrorContext) and adds no obligation to the
  code that produces failures.

  The hard part was not wiring up Sentry but deciding **what is never sent**,
  and that decision lives in the core:

  - The reporter **never sees the error itself**; the event carries only
    strings. It cannot send what it never receives.
  - Attributes pass through an **allowlist**, and the names of the dropped
    keys are still carried. By default no business identifier is included.
  - The only free text that leaves is the log message and
    `errors.Error.Message`; both carry a written guarantee. The wrapped chain
    stays in the process.
  - The grouping key is the error CODE, not the stack trace.
  - Three reports per code per minute; the suppressed count rides along with
    the next one.
  - The log is written first; a reporter that panics is switched off for the
    life of the process; a send failure is logged BELOW the reporting
    threshold — logging it above would turn a collector outage into a
    self-amplifying loop.

- **The access log's 5xx line is now marked "already reported".**
  We found this defect while running against a real collector, and no unit
  test could have shown it: a 5xx is logged TWICE — once as the diagnostic
  line that carries the code, once as the access summary that carries none —
  and both are ERROR. Reporting both doubled the volume and, worse, filled the
  `unclassified` bucket with every server error in the application; that
  bucket has to stay empty so that a genuinely unclassified failure can be
  seen in it. On top of that, it was spending that bucket's rate budget.

- **Price and stock editing in the panel** ([ADR 0013](docs/adr/0013-panel-write-surface.md)
  addendum). The variant page lets the operator edit a variant's base price
  per currency and its physical stock at each location; the `pricing.admin`
  and `inventory.admin` surfaces were added for this.

  The price surface is a **lossless read-modify-write**. The module's only
  price writer is DESTRUCTIVE: `SetPrices` does not change the set's prices,
  it REPLACES them — it deletes every price that is not in the input. The
  panel, however, reads prices from the query provider, and that provider
  FILTERS OUT prices that carry rules and prices on a price list. Put
  together, a naive form that edits the base price would silently delete
  every campaign price in the set — and since the operator never saw them,
  nobody would notice. So the surface reads ALL prices, changes only one and
  writes the rest back as they were. The cost is written down: the write
  regenerates the price IDs, which are referenced only by pricing's own
  `price_rule` rows.

  The stock surface also carries a READ, which the other two do not. The
  reason is a gap, not a preference: the query provider gives ONE total per
  item, and stock cannot be edited from a total — the operator needs to know
  which warehouse holds what. The breakdown was not added to the query layer,
  because there its audience includes the storefront; reserved quantities and
  internal warehouse names do not belong there.

  Empty locations are listed too: a form that showed only the locations that
  have a level could NEVER stock a new warehouse, because the warehouse would
  appear only once it had stock — that is, in exactly the state the operator
  is trying to reach. The reserved quantity is shown as well, because
  otherwise the service's "you cannot go below promised stock" rejection
  would look arbitrary.

  Money arithmetic is INTEGER from end to end. The text the operator types is
  converted to minor units by SHIFTING the decimal part (not by scaling it) —
  in a two-decimal currency "1.5" is 150, not 15 — and decimals beyond the
  currency's number of digits are not ROUNDED but rejected: rounding would
  silently change the price the operator typed. If the scale is unknown, the
  box takes raw minor units and the form SAYS so; an operator who typed
  "199.90" into a box that did not say so would record one hundredth of what
  they meant.

- **The panel now WRITES: a product's title, handle and status can be edited**
  ([ADR 0013](docs/adr/0013-panel-write-surface.md)). ADR 0011's "the panel
  uses the read paths" decision was deliberately reopened, and what replaced
  it is written down.

  The read layer was GENERIC; writing has no counterpart. The product
  service's method carries the module's own types (`UpdateProductInput`,
  `models.Status`) and the panel cannot name them — the moment it does, they
  become a DIFFERENT type defined in its OWN package. So the module publishes
  a narrow, primitive-typed **admin write surface**, registered in the
  container under the name `product.admin`, SEPARATELY from interop.

  The separation is not a filing preference: interop's godoc promises to stay
  narrow and lists its audience (other modules, workflows, plugins). Adding a
  write method there would, as a side effect of an edit form, give EVERY
  PLUGIN the power to rewrite the catalog. `TestAdminSurfaceHasOneAudience`
  makes the name true: no production file other than the owning module and
  the panel may reference a name that ends in `.admin`.

  The write goes through the SERVICE, not the repository: handle uniqueness
  and the `product.updated` event live there. The quiet half (the event) is
  asserted separately — otherwise the noisy half (the handle conflict) would
  have counted as proof of both.

  The surface is resolved CONDITIONALLY: in an installation without the
  product module, the panel still opens and the edit form returns a 503 that
  states the reason.

- **The panel no longer writes a JSON envelope to the browser on an
  unexpected failure.** The defect had been on the login path since ADR 0011:
  `corehttp.WriteError` writes the framework's JSON envelope, which is right
  for an API client, but what arrives on this path is a BROWSER. Worse, the
  envelope passes the message of non-Internal classes through unchanged — that
  promise was made to API clients; an operator reading the panel page cannot
  tell a leaked connection string from a diagnosis. The panel's own error page
  is now returned, and the real cause goes to the log.

- **The panel's catalog screens have arrived: the product list and the product
  page.** The product page shows variants, their prices and their stock —
  three things from three separate modules, none of them imported by the
  panel. The read layer is reached BY NAME like everyone else (ADR 0004), and
  price and stock arrive as expansions in ONE call; there is no query per row.

  The panel has to write these names BY HAND (it cannot import the modules),
  and their drift is SILENT: on the day a link name changes, the panel
  compiles, returns 200, and only the price column goes empty.
  `TestThePanelCatalogNamesAgree` moves this tie to compile time — the same
  rationale and the same place as `TestTheProviderRegistryNamesAgree`. Most
  filter and field names cannot be pinned, because the owning module does not
  export them; their protection is the read layer's "unknown field" rejection
  and the panel turning it into a 500.

  **The price is NEVER guessed.** The amount is a minor-unit integer, and
  making it readable requires the currency's number of decimal places; in
  ISO 4217 that number can be 0 (JPY), 2 (the majority) or 3 (KWD). The scale
  is read from the region record; if it cannot be read, the raw integer is
  shown and LABELED "minor units". Assuming a fixed 100 would show a wrong
  amount CONFIDENTLY in two classes. The arithmetic stays in integers from end
  to end (plan Section 8: float NEVER).

  A variant without stock shows `—`, NOT `0`: zero means "sold out", and never
  having been tracked is a different fact.

- **Admin panel skeleton: a fourth tree, `internal/adminui`**
  ([ADR 0011](docs/adr/0011-yonetim-paneli-dorduncu-agac.md)). The panel lives
  under `/admin/ui`, renders HTML on the server side (`html/template`,
  embedded in the binary) and does NOT IMPORT modules — it resolves the
  framework's read paths from the container by name. This round brings login,
  logout and a protected entry point; the catalog screens come in the next
  round.

- **The panel's identity is carried in a cookie, and the cookie is valid ONLY
  in the panel tree.** `Path` is pinned to the panel prefix; `HttpOnly`,
  `SameSite=Strict`, and `Secure` in shared environments. The reason is not a
  defense but PRESERVATION: the admin API's current CSRF immunity comes from
  the token living in a header the browser does NOT add ON ITS OWN. If the
  cookie also went to `/admin/v1`, that immunity would be lost and every admin
  endpoint would join a new attack surface. CSRF's second layer is the
  `Origin` check (`adminui.UI.CheckOrigin`).

- **`corehttp.WriteHTML`, `corehttp.WriteRedirect` and `corehttp.WriteAsset`.**
  The panel does not write its body itself: HTML also goes through the core's
  writer, so the error-path invariant (the body is written only by the core's
  writers) holds in the panel too. The page is rendered into a BUFFER first;
  an error midway returns 500 instead of a half body + 200.

- **The panel's protection ring is mounted at the composition root**
  (`adminui.Ring`). Middleware has to be mounted while the router is being
  built, whereas the panel is born from the container DURING module bootstrap;
  the ring bridges this gap and REJECTS any request that arrives before it is
  connected — an unprotected admin surface stays loudly closed rather than
  silently open (the identity line in ADR 0007).

- **The repository's working language became English, and the transition was
  tied to a LEDGER**
  ([ADR 0012](docs/adr/0012-repository-language-and-solid.md)).
  `internal/arch/testdata/turkish_ledger.txt` names every file that still
  contains Turkish, and `internal/arch/testdata/turkish_paths.txt` every path
  that has a Turkish NAME; a file that is not in the ledger may not contain
  Turkish. The ledgers only SHRINK: deleting a line requires the file to have
  actually been translated. The starting debt is 784 files + 41 paths.

  The detector has THREE LANES, and the reason was measured: transliterating
  the whole tree drops a rule that looks only at diacritics from 724 files to
  0 — that is, a single command would let you declare "translation done". The
  second lane searches comments and string literals for Turkish function words
  that SURVIVE transliteration (the list was measured against the 7711 files
  of the Go standard library, and only the words with zero hits were taken);
  the third searches for Turkish roots in WHOLE parts of identifiers.

- The fixture in the language detector was mutating the package-level
  exemption map while another test running in parallel read the same map; a
  data race under `-race`. The map is now passed to `scanSource` as a
  PARAMETER: the shared state was not locked, it was removed.

- **The log messages the smoke tests expect are now tied to production**
  (`TestSmokeLogAssertionsMatchProduction`). The smoke test expects a
  production log line as TEXT, and there is NO compiler link between the two:
  renaming the message leaves the smoke test compiling, vetted and linted, and
  `go test ./...` does not even run it — smoke sits behind a build tag. The
  break shows up AFTER the push, in CI's slowest job.

  This is not hypothetical: translating the `"izleme kuruldu"` message in the
  observability package broke exactly this pair, and every local gate stayed
  green. The check confirms, by looking at the source, that the message is
  still WRITTEN in production; moving the message into an exported constant
  would have put operator-facing text on the package's API surface.

- **The two gaps in SOLID that can be measured mechanically are closed**
  (`internal/arch/solid_test.go`). `TestResolvedTypeIsAnInterface` enforces the
  CONSUMPTION half of DIP: every `container.Resolve[T]` call site in production
  must resolve an INTERFACE. depguard only forbids imports between modules; it
  said nothing about a concrete type coming from the caller's OWN module or
  from the core. Measurement: 18 interfaces, 5 generic helpers and exactly one
  concrete family — `*db.Pool`, resolved 16 times under the name `core.db`,
  with its justification written down. `TestLayerPurity` enforces the layer
  boundary INSIDE a module: `api` may not import pgx, its own `repository` or
  generated sqlc code; `service` may not import `net/http`, chi or pgx.
  Measurement: 15 modules, 30 directories, 0 violations.

  Both tests had a defect, found by mutation, that is now closed: the counter
  of scanned directories was fed from the VERY rule list it was checking —
  renaming a layer in the rule found zero directories, and every module passed
  without a single import being read. The counter is now verified against the
  DISK.

- **Checks against the detector's own blindness.** `TestDetectorIsNotBlind`
  keeps each lane's separate counter and every scanned root positive; the list
  of roots to scan is verified against the DISK, because a counter that reads
  the list from itself falls silent along with it when a tree drops out of the
  list (seen by mutation). `TestDetectorFindsPlantedTurkish` plants a known
  sample in every lane, and `TestDetectorPassesEnglishSource` proves that it
  does not falsely accuse correct English — including `module`, `rollback`,
  `reason` and the variable name Go's `y, ok` idiom shortens to.

- **The SOLID rule was tied to measurement.** ADR 0012 tabulates the current
  state of the five principles: DIP and OCP are enforced, ISP holds
  STRUCTURALLY at module boundaries, SRP only at the macro level, and there is
  no check at all for LSP. For the last two, "there is no check" is written
  down EXPLICITLY; the size linters stay off, because splitting a 53-method
  interface into six to meet a threshold pleases the counter, not the design.

### Changed

- The wiring invariant (`TestTheAdminPanelIsSetUpInTheCompositionRoot`) and
  the module-isolation check (`TestTheAdminPanelDoesNotImportModules`) cover
  the fourth tree too. The prefix match was fixed to accept the ROOT of the
  tree as well: it used to see only subpackages, so a package set up at the
  root would have stayed outside the check.
- The body-write scan now also catches template streams of the form
  `tmpl.Execute(w, …)`. Because the scan looked at the receiver's import name,
  it was BLIND to the template writer, and the panel could have slipped
  through this blind spot.
- **That the panel cookie is NOT ACCEPTED at `/admin/v1` is now an invariant**
  (on 2026-09-09 this test was renamed
  `TestThePanelSessionReachesTheAdminAPIOnlyUnderTheOriginCheck`: once
  ADR 0030 was implemented, the cookie IS accepted and a defense took the
  place of the immunity — the test is stricter, because an absence takes a
  single assertion and a defense takes a matrix). This was ADR 0011's
  load-bearing claim, and until now no test held it: the admin API's CSRF
  immunity comes not from a defense but from the token living in a header the
  browser does NOT add ON ITS OWN. The claim is tested on the REAL protection
  stack, not on a hand-built chain — because what is being proven is a
  property of the SCOPE.

  Four mutations survived while the test was being written, and all four were
  real gaps in the test: opening the panel with the cookie passed even when
  the ring was not mounted at all (the panel prefix is already open for the
  quotas); nothing proved that the origin ring was MOUNTED; removing the login
  path's identity exemption failed no test — yet its cost is "nobody can log
  in", and the failure does not even look like an error: the login page comes
  back with a 401.

- **`corehttp.SchemeBearer`.** The core LOWERCASES the scheme it reads from
  the `Authorization` header and hands it to the authenticator that way; the
  panel, because it carries the token in a cookie, never goes through the
  header and was writing the scheme by hand. The two spellings worked today
  only thanks to the auth module's case-insensitive comparison. The contract
  is now written on the `Authenticator` interface, and both sides use the
  same constant.

- `core/http/auth.go` was translated into English. The messages of
  authentication responses changed (`"authentication is required"`); the codes
  (`unauthenticated`, `forbidden`) did not.

- **Eight core packages were translated into English** (ADR 0012):
  `core/errors`, `core/container`, `core/module`, `core/provider`,
  `internal/core/logger`, `core/db` (including the migration testdata),
  `internal/core/observability`, `core/plugin`, and in `core/http`
  `response.go`, `auth.go`, `router.go`, `server.go`, `middleware.go`, and the
  production files of `core/query`.

  The read layer's error DETAIL keys were translated too
  (`"aranan_ad"` → `"looked_up_name"`, `"alan"` → `"field"`). These are not
  error CODES; the code is the contract and did not change. Details are for
  diagnosis and are written in the repository's language. Behavior did not
  change. What changed is text that reaches USERS/OPERATORS: the container's
  diagnostic messages (`"missing: Reserve(...)"`,
  `"...have pointer receivers"`), module registration errors and log keys
  (`"servis"` → `"service"`, `"tembel"` → `"lazy"`). The `Kind.String()`
  outputs (`not_found`, `invalid`, …) are a CONTRACT and did not change; the
  godoc now says so explicitly.

- **The composition root and the core's response writer were translated into
  English** ([ADR 0012](docs/adr/0012-repository-language-and-solid.md)). In
  `cmd/server`, `kurulum.go` → `setup.go`, `kurulum_test.go` →
  `setup_test.go`, `belge_test.go` → `docs_test.go`; in `core/http`,
  `response.go` and its test. Behavior did not change, but the startup LOG
  MESSAGES and the generic internal error message returned to the user are now
  in English (`"an unexpected server error occurred"`). Error CODES did not
  change and will not: the code is the machine contract, the message is for
  humans.

  During the renames the registration check caught a real trap: naming the
  plugin registry's local variable `registry` made that line look like a
  module registration, because the check recognizes the receiver BY NAME.

  Content ledger 784 → 777, path ledger 41 → 38.

- The list that recognizes the headings of ADR option sections became
  BILINGUAL (`internal/arch/doc_references_test.go`). A rule that recognized
  only Turkish headings would take the REJECTED options of an ADR written in
  English for claims about today's repository, and would report nonexistent
  symbols as broken.

## [0.5.0] — 2026-09-02

### Breaking changes

Breaking changes may occur in minor releases throughout `0.x`. The following
directly affect clients that use the **store API**.

- **`region_id` was REMOVED from the `POST /store/v1/carts` body;
  `country_code` became REQUIRED in its place.** A request that sends the
  field now gets `422` (the body rejects unrecognized fields). The server
  derives the cart's region and currency from the customer's COUNTRY.

  There are two reasons for the removal, and both come from the same yardstick
  ("what goes into the body is what the customer can decide"):

  1. `region_id` is **not** what the customer wants to express. The customer
     picks a country (or their browser says it); the region is that country's
     counterpart on the server, and the operator sets up the mapping. Making
     the client write an internal entity ID is a softer form of the "taking
     the server's data from the client" class that was closed with
     `unit_price`/`currency_code`; and since the region selects the cart's
     **tax rate**, the consequence is not cosmetic either.
  2. A workflow that already did the derivation existed —
     `internal/workflows/cart`'s `create_cart` resolves both the region and
     the currency from the country code — and the storefront endpoint
     **bypassed** it. Two contracts for the same operation, and the path the
     operator saw was the raw one.

  Silently ignoring the field was not chosen either: the client would think it
  had sent it, and the server would open a cart in a different region — and
  that cart would be priced at a different tax rate, from a different price
  list.

  The new error surface carries THREE separate `404`s, and they are three
  distinct situations: a valid country that is bound to no region is
  `country_has_no_region`, a country code that does not exist in the reference
  table at all is `country_not_found`, and a country whose bound region has
  been deleted is `country_region_missing`. A malformed or empty code is
  `422`. In addition, the `cart_region_unavailable` (500) code DROPPED out of
  the cart-opening path — the region surface is no longer wired into the
  handler at all — and in its place came `cart_missing_after_create`, for when
  the cart is opened but cannot be read; both are operator codes, and the
  client does not branch on them. The distinction is kept because the two
  need different fixes: in one the customer picks a different country, in the
  other the client fixes its body.

- **The wiring is the SAME as the pattern on the line-item endpoints and
  brings no new mechanism.** `cart` defines a third narrow interface in its own
  package (`api.CartOpening`), resolves the concrete workflow from the
  container **lazily** under the name `workflows.cart.interop`, and fails
  **closed** if it cannot be resolved: `500`, no cart is written. As a
  consequence, the only place where the `cart` module resolved another module
  by name is closed too — the `api.RegionCurrencyReader` and `region.service`
  binding was **removed**, because the workflow now derives the currency. The
  module's `LinePricingName` constant became `CartFlowsName`: the same
  registration feeds two narrow interfaces today, and the constant's name had
  to be the workflow's name.

- The `Carts` narrow interface of `workflows/cart` grew again: `OpenCart` now
  also carries the cart metadata. This affects embedded code that writes its
  own implementation. `OpenCartForCountry` was added to the same surface —
  for a while `Interop` deliberately did not publish cart opening, because it
  had no consumer; now it has one.

- **The `SelectLocation` method of `fulfillment.interop` was REMOVED;
  `RankLocations` replaces it.** This affects embedded code and consumers that
  write their own fulfillment surface. The paths and request/response schemas
  of EXISTING endpoints did not change; for the error code see the item below,
  and for the new admin endpoints see the "Added" section.

  ```go
  // before
  SelectLocation(ctx context.Context, candidateLocationIDs []string) (string, error)
  // after
  RankLocations(ctx context.Context, destinationRegionID string, candidateLocationIDs []string) ([]string, error)
  ```

  There are two changes, and each has its own rationale.

  **The region parameter** breaks a written commitment: the old godoc said
  "the policy grows richer INSIDE this method; the signature the caller sees
  does not change". The commitment was wrong, and where it was wrong is
  concrete: what was missing was not only the warehouse itself but WHERE the
  shipment is going, and the latter cannot be obtained by enrichment inside
  the module. The region is in the caller's hands — the cart workflow's plan
  already carries it.

  **Returning a ranking** is a cost decision, and the comparison is
  COUNTERFACTUAL: v0.4.0's selection was a pure function that never touched
  the database. Had the policy been added to the old surface
  (`SelectLocation`, which returns a single location), the caller would have
  had to ask again after every warehouse that ran out — N queries instead of
  one for a line with N candidates; and since the order is deterministic,
  those N-1 calls would recompute the same ranking. The side gain is
  measurable: the termination of the cart workflow's candidate loop is now
  independent of what the module returns — it used to depend on the selected
  candidate being droppable from the list; now it is bounded by the length of
  a finite slice.

  The only seam the compiler does not check is where the interface is resolved
  from the container **by name**; its proof is the end-to-end scenario under
  `internal/e2e`.

- **The stock reservation step now PRESERVES THE UNDERLYING ERROR'S CODE.**
  The `error.code` that the storefront client sees in the body changed for the
  case where stock cannot be reserved during completion:

  | Situation | Before | After |
  |---|---|---|
  | No candidate in any warehouse | `checkout_workflow_reservation_failed` | unchanged |
  | The selected warehouses ran out | `checkout_workflow_reservation_failed` | `inventory_insufficient_stock` |
  | No candidate serves the cart's region | — | `fulfillment_no_serviceable_location` |

  The status code stays `409` in all three. The reason for the change is this
  round's own need, and it is a PRECONDITION of this feature: the transport
  layer writes a single machine-readable field into the body, and as long as
  the code was overwritten, a misconfigured region binding would have been
  reported as "stock could not be reserved" with full shelves — the operator
  could not find where to look. The pattern is not new: the engine fixed the
  same fault in its own wrapping one round earlier, and the rationale is
  written there, measured with the B2B spending limit.

  This affects clients that branch on the code.
  `checkout_workflow_reservation_failed` is now a fallback in the WRAPPING of
  the step error: if the underlying error carries its own code, that code is
  kept. The code is NOT gone — it keeps appearing in the errors the step
  produces ITSELF: when no candidate is found in any warehouse (the first row
  of the table above) and when the fulfillment module breaks the contract (an
  empty ranking, an ID that is not a candidate, a duplicated candidate — all
  three `500`).

- **Sales channel scope is now enforced on the WRITE path too.** In
  installations that USE channel assignment,
  `POST /store/v1/carts/{id}/line-items` returns `404` instead of `201` for a
  variant of a foreign channel. The details and rationale are below, under
  Security; the item is placed here too because an integrator who scans only
  this section before upgrading would otherwise not see it.

### Added

- **The admin panel has begun: the write gate, the skeleton and the scope of
  the checks.** The panel lives under `internal/adminui`, in a fourth tree as
  a sibling of `internal/workflows`, and renders server-side HTML from
  templates embedded in the binary. The decision and the rejected options are
  in [ADR 0011](docs/adr/0011-yonetim-paneli-dorduncu-agac.md). What arrives
  this round is only the skeleton: the session, the protection ring and the
  catalog screens come in later rounds.

  Three writers were added to the core — HTML, redirect and static asset. The
  HTML writer requires the body to be rendered **into memory first**: in a
  template streamed straight to the writer, an error that arises midway leaves
  a HALF page with a `200` status code, and once the headers are sent neither
  the panic recoverer nor the error writer can do anything. Unlike the JSON
  writer there is NO 2xx requirement, and that is deliberate: returning the
  login page to an unauthenticated browser with `401` is more honest than
  sending it somewhere else.

  **Two blind spots, closed in the round they opened in** — both measured:

  - The registration checks narrowed their scope to the module tree; in the
    panel tree, a capability that was "written but wired up nowhere" would
    have left the arch run GREEN. Nothing had to be invented: the same gap had
    already been closed for `internal/workflows`, and its pattern was ready.
    The check was also fixed to see packages that live at the root — the
    prefix match covered only subpackages.
  - The out-of-module arm of the "the body is written from one place"
    invariant did NOT SEE template writes: since the call's receiver is not a
    package name, the target could not be resolved and the call slipped
    through silently. This was not a permission but a false negative of the
    way the scan measured — the rule was not being lifted, it was going
    blind. The scan now catches a template being streamed to the writer.

  Templates are parsed AT STARTUP and their names are pinned in both
  directions: startup halts if an expected name was not parsed, and also if a
  parsed template is called nowhere. A template name is a string; a typo
  compiles, lint does not see it, and it blows up only when that page is
  opened.

  Verified with five mutations: the panel's wiring, the module import ban, the
  template being streamed to the writer, and both directions of the template
  name check.

- **Warehouse selection now carries a POLICY.** The limit was WRITTEN DOWN
  this round and closed in the same unreleased window; it never stood among
  the known limits of any released version. The value of the record is that
  the rule had silently been "the candidate with the smallest ID" since
  v0.2.0. When it was written down, it read: *"Warehouse selection carries no
  POLICY … proximity, cost and stock distribution CANNOT BE EXPRESSED, because
  the module has no location model."*

  The location model arrived in the fulfillment module's **own** schema (two
  tables), and the warehouse ID stays an opaque foreign ID without an FK —
  the way `region_id` has stood until now. The module does NOT COPY a name or
  an address: where the warehouse is located is the inventory module's data,
  and it stays there.

  The rule has three steps — **eliminate** (if there are regions bound to a
  warehouse and the destination is not among them, the candidate drops out),
  **sort** (`priority`, smaller first), **break ties** (smaller ID first). The
  admin surface is
  `PUT/GET/DELETE /admin/v1/shipping-locations/{location_id}` and
  `GET /admin/v1/shipping-locations`.

  **Backward compatibility is complete for the SELECTED WAREHOUSE and is
  tested:** with no policy record, elimination and sorting are no-ops, the
  tie-breaking rule is what remains, and the selected warehouse is the same as
  before this round.

  Two things change even in an installation without records, and both must be
  written down here: the error CODE changed (see the breaking change above; it
  also affects calls that NAME a warehouse, because that path goes through the
  same wrapping even though it never enters the policy), and selection now
  makes ONE SQL QUERY per line — the old selection was a pure function that
  never touched the database, so a new possibility of failure arose on this
  path.

  Measured on the real stack: `internal/e2e/multi_warehouse_test.go` sets up
  two sufficient warehouses with real Postgres and real modules, writes the
  policy, and reads which warehouse the reservation was opened in. Verified by
  mutation.

  What the policy does NOT EXPRESS was written down too — stock distribution,
  cost, an order-level decision and a preference per (warehouse, region) pair
  — and why each of them cannot be expressed is in
  [ADR 0010](docs/adr/0010-depo-secim-politikasi.md).

  The three accepted costs ENTERED the README's known limits, and the heaviest
  is this: binding a region ID that does not exist (or deleting a region and
  reopening it under the same name — the new record gets a new ID) eliminates
  that warehouse for every cart and, in a single-warehouse installation,
  closes the store; and a cart that fails can never be completed again,
  because the completion workflow's idempotency key is derived from the cart
  ID.

  The cost was not removed, it was made VISIBLE — but the limit of that
  visibility must be written down too: only the CODE reaches the storefront
  body (`fulfillment_no_serviceable_location`); the message in the body is the
  same for all three reservation failures, because the transport layer writes
  the outermost message. The dump of which regions the candidates are
  actually bound to is in the SERVER LOG and in the `workflow_executions`
  record. So the code goes to the client, the dump to the operator.

  The distinction that the region binding is a **constraint** while `priority`
  is used for preference is also deliberate: "the regions it serves" is the
  carrier's coverage area, and shipping outside the coverage is not a graceful
  fallback but an impossible shipment. Turning the binding into a sort key and
  putting the hard cut behind a flag was considered and rejected; the
  rationale is in the ADR.

- **The setup trap is now pinned in the real process:**
  `TestPublishableKeyWithoutChannelIsRejectedByStorefront` in
  `internal/smoke/keys_test.go` walks the README's publishable key paragraph
  end to end — a key without a channel is created (`201`), it gets `401` on
  the store surface, the diagnostic code (`auth_no_sales_channel`) is looked
  for not in the response but in the server's LOG, and once a channel is bound
  afterwards the SAME key gets in. This path ran at no level in the
  repository: no test expected that code, and `internal/smoke`'s own helpers
  always created the key bound to a channel. Verified by mutation — on a
  server that accepts a key without a channel, the scenario expects `401`,
  sees `200` and fails.

- The variant Query provider of the `product` module recognizes a new filter:
  `sales_channel_ids`. It can be used only TOGETHER with `id` or `ids`; given
  on its own, the request gets `422` — the channel filter is an authorization
  narrowing, not a listing criterion in its own right. The cart workflow's
  enforcement of channel scope on the write path relies on it. It is NOT
  exposed on the HTTP surface.

### Changed

- `metadata` in the `POST /store/v1/carts` body **stays** and is passed
  through to the workflow as is. The decision is the same as the one made for
  line metadata: the field really is the client's information (campaign
  source, storefront session), enters no calculation and has no counterpart to
  derive. Had it been dropped, a field the client sent would have silently
  disappeared, since the workflow is now the only path that opens a cart.

### Fixed

- **`.env` was SILENTLY overriding environment variables given on the command
  line.** The `Makefile`'s `.env` loader applied the file ON TOP OF the
  caller's environment; the empty `PLUGINS=`,
  `OTEL_EXPORTER_OTLP_ENDPOINT=` and `ADMIN_BOOTSTRAP_EMAIL=` lines in
  `.env.example` left every one of the README's examples of the form
  `VARIABLE=… make run` without effect — without an error. Measured (the same
  Makefile, the same `.env`, the only difference being the loader): before the
  fix, `PLUGINS=search-pg … make` → `PLUGINS=[]`,
  `OTEL_EXPORTER_OTLP_ENDPOINT=[]`; after it, both carry the command-line value
  and `.env` is still read (`LOG_FORMAT=text` comes through). Precedence was
  turned to the same direction as docker compose's: **environment > `.env`**.
  The method does no parsing — the caller's environment is saved with
  `export -p`, `.env` is loaded by the shell, and the saved environment is
  applied back on top.

- **`make openapi-client` wrote root-owned files into the working tree**, and
  `make clean` then failed with "Permission denied"; developers needed `sudo`
  to clean their own repository. The generator container was given `--user`.
  The mechanism was measured: without `--user` the container writes as
  `uid 0` and `rm -rf` exits with code 1; with `--user` the files are owned by
  the caller and the same `rm -rf` returns 0.

- The README's module isolation guarantee was STALE: it said "12 modules × 11
  bans", whereas `.golangci.yml` today carries 14 bans for each of 15 modules
  (counted: 15 rules, 210 `deny` entries, none missing). The number was
  corrected, and it is now written down that the list is maintained by hand,
  but that if it is forgotten the rule does NOT GO unchecked —
  `TestModulesDoNotImportEachOther` walks the module tree and knows nothing of
  `.golangci.yml`.

- The README referred to the customer session as "Phase 8"; in the same
  document's "Phase status" table, Phase 8 (Auth · admin user · API key ·
  RBAC) appears **complete**. A contradictory signal for the reader: what is
  shown as the scope of a finished phase is in fact within the scope of no
  phase at all. The phase number was removed and the scope written out
  explicitly.

### Removed

- **The rate limiter's exported key helper was REMOVED** — `PrincipalKey` in
  the `core/http` package. (The name is written here without its package
  qualifier: a qualified reference sends the reader off to SEARCH, and
  `internal/arch` checks for that; yet what this item says is precisely that
  there is NOTHING left to search for.)

  In v0.4.0 it was an exported helper that derived the rate limit key from the
  caller's identity. It had NO consumer in production, and could not have had
  one: the rate limit ring runs BEFORE authentication in the protection stack,
  so at the moment it is called there is no identity yet, and the function
  would return the same fallback key on every request. What it offered was a
  promise that could not be kept.

  This affects embedded code: a party that writes its own `KeyFunc` and calls
  this helper no longer compiles. The replacement is to write the same
  behavior in two lines in its own package; if the key should be split by
  identity, the ring has to be mounted AFTER authentication, and the rationale
  is in the `KeyFunc` godoc.

### Security

- **Sales channel scope is now enforced on the WRITE path too: a variant of
  another channel CANNOT be ADDED to the cart.** The rule (`a product without
  an assignment is visible in every channel, one with an assignment only in
  the channels it is assigned to`) was enforced until v0.4.0 only on the READ
  surface — the list, the counter, the single-item endpoint and the bulk read
  all went through a single SQL template.
  `POST /store/v1/carts/{id}/line-items`, however, read the variant by ID
  ONLY.

  The consequence made the rule itself meaningless: a client arriving with
  channel B's publishable key could add the line and complete the purchase
  just by writing into the body the ID of a variant sold only in channel A. A
  product hidden in the storefront could be sold through the cart, so the
  filter was a display preference, not an authorization. Measured on the real
  stack: before the fix the foreign channel's variant gets `201`, after it
  `404` (`internal/e2e/channel_cart_test.go`).

  The rule was NOT WRITTEN A SECOND TIME. The workflow still reads the variant
  from the Query layer; the only addition is that the channels coming from the
  request's AUTHENTICATED identity are placed on the read as a filter. The
  side that applies the filter is the product module, and it applies it with
  the very same SQL template the storefront uses
  (`repository/saleschannel.go`); the new query only instantiates the template
  with the variant's `product_id`.

  Channels are NOT TAKEN FROM THE CLIENT; they come from `corehttp.Principal`
  — the same decision as on the read surface. The three cases are also
  separated EXACTLY as on the read surface: no identity → no filter is
  applied; an identity without channels → the EMPTY SET (only unassigned
  products); an identity with channels → those channels. An arch test pins
  that the two derivations carry the same meaning
  (`TestChannelDerivationMeansTheSameOnBothSurfaces`).

  An out-of-scope variant returns the **same** error as a variant that does
  not exist at all (`404 cart_workflow_variant_unknown`): a different class
  would give away the existence of a product sold in another channel and
  would pierce the hiding itself.

  **Scope is enforced AT ENTRY.** The line quantity update and cart completion
  paths do not ask about scope again: adding a line is the only path that can
  bring a variant into a cart, and it is a deliberate decision that a line
  that is ALREADY IN a cart does not become unpayable because of an admin edit
  that later moves the product to another channel. The boundary is written
  down in `workflows/cart/saleschannel.go` and in the README; an arch test
  (`TestVariantReadsGoThroughTheChannelDecision`) forces every new variant read
  either to make the decision or to write down its rationale.

  **What is asked of whom:** installations that never use channel assignment
  are not affected (an unassigned product stays sellable in every channel). In
  installations that DO USE channel assignment, a client that has so far
  relied on the gap to add a foreign channel's product to the cart now gets
  `404`; the right fix is to route the catalog shown in the storefront and the
  product added to the cart through the same key.

- **The CONDITION under which the B2B spending limit is applied is now
  documented: the limit applies to a purchase that DECLARES its customer.**
  The behavior **did not change**; what changed is that until v0.4.0 this
  repository described the limit as a rule applied unconditionally.

  The rule works through `CustomerID` inside `order.CreateOrder`, and that ID
  enters the chain from the body of the storefront cart. The store surface's
  only identity is the publishable key, and it represents a sales channel, not
  a customer (`corehttp.Principal` carries no customer ID) — so `customer_id`
  is a claim that requires no proof. Measured on the real binary with a single
  publishable key (limit `50_000`, cart total `76_800`): with `customer_id` in
  the body, completion gets `409 order_spending_limit_exceeded`; the same cart
  without the field gets `200`. A purchase completed under someone else's ID
  is deducted from that customer's window, so the spending allowance of an
  employee whose name is known **can be burned**. Making the declaration
  mandatory does not close it either: `POST /store/v1/customers` opens a
  fresh guest record with no rules using the publishable key.

  The fourth door is that attribution can be made **after the fact**: a cart
  opened as a guest is handed over to someone else's `customer_id` with
  `POST /store/v1/carts/{id}`, and the order is written under that identity —
  so attribution rests on the declaration not only when the cart is opened but
  for the cart's WHOLE LIFETIME (measured: the handover gets `200`, and the
  order is in the victim's name). The number of doors is not three but FOUR;
  the same number is four in the README's B2B section and in ADR 0008 as well.

  Authentication was **not built**, and this is deliberate: verification was
  decided to be the job of the embedding application, not of the framework
  ([ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md) — the rejected
  options and the list of work that falls to the embedding application are
  there). The boundary was written into the README's B2B section, the `order`
  module's godoc, and the `service.SpendingPolicy` and
  `CreateOrderInput.CustomerID` fields, and pinned in `order` with two tests
  (`TestTrustBoundaryGuestOrderIsNeverAskedForTheSpendingRule`,
  `TestTheSpendingRuleIsAppliedToTheDeclaredCustomer`). The two tests protect a
  decision, not a capability: when authentication arrives, they are
  **expected** to fail.

  What embedding applications with a B2B setup must do: protect the
  storefront surface with a customer session and read `customer_id` from the
  session, not from the body. Without that layer, the limit only catches an
  honest client's mistake.

### Known limits

This section is part of a HISTORICAL record and says only **what changed in
this release**. The "Known limits" sections of v0.1.0 and v0.4.0 describe
what was known IN THOSE RELEASES and are not corrected retroactively; a limit
that closes is written into the record of the release in which it closed —
here. The FULL list of the limits that hold today is in the "Known limits"
section of [`README.md`](./README.md): extracting today from a release record
would require stacking three lists on top of each other, and the person who
makes the adoption decision reads exactly that list.

**Closed.**

- v0.4.0's item "`POST /store/v1/carts` still takes `region_id`" is CLOSED:
  the field is gone from the body, and the server derives the region and the
  currency from `country_code` (above, Breaking changes). The closing was done
  where the item itself pointed — not in the handler, but in the workflow that
  already did the derivation.
- The sales channel rule not being enforced on the WRITE path is CLOSED
  (above, Security). This was NOT WRITTEN in the "Known limits" section of any
  release, and that is the real point of the record: since v0.1.0 the rule had
  been described as an authorization while being enforced only on the read
  surface. A limit that is not written down is a limit nobody closes; this
  time, too, what made it visible was a document
  ([ADR 0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md) found the gap
  while building its own rationale).
- Warehouse selection having NO POLICY is CLOSED (above, Added): the rule is
  now the eliminate → sort → break ties triple. This limit, too, never stood in
  the "Known limits" section of any RELEASED version — it was written down and
  closed in the same unreleased window. The value of the record is that the
  rule had silently been "the candidate with the smallest ID" since v0.2.0
  (the release that brought multi-warehouse support). The accepted costs of
  the closing entered the open limits below.

**Ongoing.** v0.4.0's item "no OWNERSHIP check on storefront carts" holds as
is; the model did not change. The only thing that changed is that the place
the model does not cover (the `customer_id` claim) was MEASURED on the real
binary in this release.

**Investigated, decided and DELIBERATELY left open this round.**

- **The customer identity is not verified; the spending limit is applied
  CONDITIONALLY.** The measurements and the rationale are above, under
  Security; the decision is in
  [ADR 0008](docs/adr/0008-musteri-kimligi-guven-siniri.md). The correct
  statement of this limit is NOT "the spending limit is not applied" but "the
  limit applies only to a purchase that DECLARES its customer": in a
  storefront where identity is verified, the rule really does enforce
  accounting discipline.

- **Sales channel scope is enforced AT ENTRY; the QUANTITY of a line already
  in a cart can be increased later.** Scope is asked only when a line is
  added. Even if the product is later moved to another channel, the path that
  updates the line quantity does not ask about scope again
  (`Workflows.UpdateLineItem`,
  `internal/workflows/cart/update_line_item.go`); nor does the completion
  workflow. The consequence in one sentence: a client that already has a line
  in its cart can buy MORE of a product that is no longer visible in its
  storefront. This is not an oversight but the cost of a decision that was
  made — the alternative was an admin making a customer's full cart unpayable
  with a catalog edit. The decision is written down with its rationale in
  `internal/workflows/cart/saleschannel.go`, and an arch test forces every new
  variant read to make the same decision
  (`TestVariantReadsGoThroughTheChannelDecision`).

- **There is NO multi-tenancy, and that is a decision: the boundary is the
  INSTALLATION, not the row.** None of the 74 tables holds an answer to the
  question "whom does this row belong to", no query carries such a filter, and
  the framework neither recognizes a boundary between tenants nor CLAIMS one.
  Serving two customers from one installation is not supported: one tenant =
  one installation = one database = one process. The plan document left the
  concept out of scope in two places but did not write down its RATIONALE; an
  exclusion from scope without a rationale is not a decision, and it is
  re-argued every round. The two rejected designs and what would reopen the
  decision are in [ADR 0009](docs/adr/0009-cok-kiracililik-kurulum-siniri.md).

- **A wrong region binding CLOSES THE STORE and PERMANENTLY consumes the cart
  that fails.** Binding a region ID that does not exist — or deleting a region
  and reopening it under the same name, because the new record gets a new ID
  — eliminates that warehouse for every cart; in a single-warehouse
  installation, the result is that every completion is rejected even though
  the catalog is full. The failed cart can never be completed again, because
  the completion workflow's idempotency key is derived from the cart ID and a
  failed execution cannot run again with the same key. This burn existed
  BEFORE this release too; what changed is that its trigger can now be a
  single admin write rather than a stock fact. The cost was not removed, it
  was made VISIBLE: the failure carries its own error code, and that code
  reaches the storefront.

- **A region binding is not a PREFERENCE but a CONSTRAINT, and it NARROWS the
  fallback set.** An operator who binds two warehouses to separate regions
  accepts that the order fails when the first warehouse's stock runs out in a
  race — whereas before the policy was written, that order would have shipped
  from the other one. "A first, B if it runs out" is written with PRIORITY,
  not with a region binding. Turning the binding into a sort key and putting
  the hard cut behind a flag was considered and rejected; the rationale is in
  [ADR 0010](docs/adr/0010-depo-secim-politikasi.md).

- **Deleting a warehouse's LAST region binding does not hide it; it opens it
  to ALL regions.** The rule is the same as for sales channel scope and comes
  from the same rationale: the strict alternative would have stopped the
  orders of every installation without a policy on the day it shipped. The
  asymmetry must be written down — for the sales channel the cost is
  VISIBILITY, here it is a FAILED ORDER.

- **The architecture invariant that checks workflow setup is a syntactic
  PROXY, and its false negative was MEASURED.**
  `TestEveryWorkflowIsSetUpInTheCompositionRoot` asks the question "can a
  misconfiguration halt startup" as "does the path to setup go through a `go`
  statement". When the `go` is hidden behind a one-line indirection the check
  PASSES, yet the property does not hold: measured in the real process — the
  synchronous binary exits with code 1 on a setup error, while a binary of
  that shape starts up healthy and reduces the failure to a single ERROR line.
  The proxy is kept anyway, because the shapes it CATCHES (a bare `go`, a
  closure, a multi-link chain) are the ones written by accident; the shape it
  misses has to be written on purpose. The scope is written down in
  `internal/arch/registration_test.go`, and the sentence "this invariant
  guarantees that startup fails closed" is deliberately not made there.

## [0.4.0] — 2026-09-01

### Breaking changes

Breaking changes may land in minor releases throughout `0.x`. The following
directly affect clients that use the **store API**.

- **`unit_price` and `title` are REMOVED from the body of
  `POST /store/v1/carts/{id}/line-items`.** A request that sends either now
  gets `422` (the body rejects unrecognised fields). Silently ignoring them was
  not chosen: the client would believe it had sent them, while the server
  wrote a different price. The price comes from `pricing` and the title from
  the catalog; the rationale is below ("Pricing authority taken away from the
  client").
- **`currency_code` is REMOVED from the `POST /store/v1/carts` body.** A
  request that sends the field now gets `422` (the body rejects unrecognised
  fields). The server derives the cart's currency from the cart's REGION.
  Silently ignoring it was again not chosen: the client would believe it had
  sent it, the server would write a different currency, and the line would be
  priced from a different price list than expected. Rationale below
  ("Currency authority taken away from the client").

- **`POST /store/v1/carts` now returns `404` for an unknown `region_id`.**
  Previously the region's existence was never checked, and a cart could be
  opened with a made-up ID; since the currency is read from the region, that
  door is closed too. An empty or malformed `region_id` is still `422` — but
  the error now carries the `region` module's code (`region_invalid_input`),
  not `cart`'s.

- **`PATCH /store/v1/carts/{id}/line-items/{line_item_id}` with a quantity of
  zero now returns `204` instead of `422`** and removes the line. A zero
  quantity used to be invalid, so no client can depend on it; in every cart
  UI, bringing the quantity selector down to zero means "remove this", and the
  workflow translates the intent.
- The `Carts` narrow interface of `workflows/cart` grew: `AddCartLineItem` now
  also carries the line's metadata. This affects embedding code that writes
  its own implementation.

### Added

- **The cart workflows are WIRED into the production binary; `POST
  /store/v1/carts/{id}/complete` added.** `internal/workflows/cart` and
  `internal/workflows/checkout` were not called in any setup: `cmd/server`
  registered only the saga ENGINE (`core.workflow`), and not a single line of
  production code built the workflows themselves. The only callers were the
  `internal/e2e` tests — so in the running binary there was NO path that
  turned a cart into an order: payment, shipping, checkout promotions, the
  `order.placed` notification and the b2b spending limit were unreachable. The
  README, meanwhile, described `complete_cart` as a capability on offer.

  The wiring follows the same pattern as the `order` → `b2b` spending rule:
  the HTTP owner of the workflows is the MODULE. `cart` defines two narrow
  interfaces in its own package (`api.LinePricing`, `api.CartCompletion`),
  resolves the concrete workflow from the container under the names
  `workflows.cart.interop` / `workflows.checkout.interop`, and `cmd/server`
  only builds and registers the workflows — no handler code enters the
  composition root, and the URLs stay under the cart.

  The registration order is **circular**, and the circle is broken in two
  places: because the workflow resolves the surfaces of every module, it can
  only be built after `Bootstrap`, while the module's handler is built during
  `Register`. The module-side resolution is therefore LAZY (on the first
  request, with the result kept) — exactly the same mechanism as `order`'s
  `spendingPolicy` wrapper; no new one was invented.

  Endpoints wired: adding a line and updating a quantity now go through the
  `add_line_item` / `update_line_item` workflows (so the line is RE-PRICED on
  every change and the cart total does not go stale), while
  `POST .../complete` runs the `complete_cart` saga.

  Each field in the completion body was decided separately, as a question of
  authority: `payment_provider_id` and `payment_data` come from the client
  (the customer's choice); `expected_total` is **required** (had it been
  optional, every client that forgot the field would silently switch off the
  "is the amount charged the amount you saw" protection; a mismatch produces
  `409`, and because the calculation is refreshed before the saga's first
  step, NO side effect is applied); `email` is NOT in the body (the cart's
  address is already on the cart; a second channel would tie the order to an
  address other than the one shown on the cart); and `location_id` is NOT
  there either (which warehouse to ship from is a shipping decision; letting
  the customer pick a warehouse would leak the stock topology). The response
  carries the order's ID and the amount collected; payment session /
  collection / reservation IDs and operator-facing warnings are not
  published.

- **Pricing authority taken away from the client.** The `POST
  /store/v1/carts/{id}/line-items` body took `unit_price` and the cart service
  wrote it AS IS; only its range was checked (`checkAmount`), not its
  correctness. The field's godoc said "the `calculate_totals` workflow writes
  the final price" — but that workflow was never built, so the price the
  client sent WAS the final price. The storefront's identity is the
  publishable key, and it lives in the browser: this was a "write your own
  price" endpoint open to anyone. It had no consequence because the checkout
  endpoint did not exist either — but both had the same root, and the moment
  checkout was wired, "buy everything for a single cent" would have opened up.
  `title` was in the same class: the line's name is catalog data, and it is
  what appears on the cart, the order, the invoice and the packing list.

  The price now comes from the `pricing` module and the title from the Query
  layer. No admin-side counterpart was opened, and none is needed: `cart`'s
  `/admin/v1` surface is READ-ONLY by definition (the only party that changes
  a cart is the customer), so there is no endpoint to change for "let an
  administrator enter a price".

  **The price path fails CLOSED**: if the pricing workflow cannot be resolved,
  the line is NOT ADDED AT ALL — neither at the client's price nor at zero.
  This is the deliberate OPPOSITE of the b2b spending rule: if b2b is not
  registered, "no limit" is the right answer, but if there is no pricer,
  writing a "no price" line is silently selling goods for free. The rationale
  is written in the `linePricing` godoc.

  New tests: that the storefront accepts no price/title and that a rejected
  request WRITES no line to the cart (unit + e2e), that the line is not added
  when there is no pricer, that the completion endpoint really produces an
  order and that an unconfirmed total leaves no side effect behind (e2e, on
  real modules and real Postgres, entirely over HTTP).

- **Currency authority taken away from the client.** The
  `POST /store/v1/carts` body took `currency_code` and the cart service wrote
  it AS IS; only the code's FORMAT was validated, and it was not compared with
  the region's. So a mismatch was not rejected: a client that wrote `EUR` on a
  cart opened in a TRY region really got that cart in EUR.

  The class is the SAME as `unit_price`, only with a smaller blast radius: the
  client could not invent an amount, but it could choose WHICH PRICE LIST was
  applied. The currency is not a label on the cart but the price's SELECTOR —
  the line workflow reads the unit price from the variant's price set "in the
  cart's currency". A smaller radius makes the defect smaller; it does not
  make it legitimate.

  The currency is the region's data: in the `region` schema it is a SINGLE
  column per region (`region.currency_code`, an FK to the `currency` table).
  Since a region cannot have two currencies, the cart's currency is not a
  choice but a DERIVATION, and the handler now reads it from the region. The
  pattern is the same as for the price: `cart` does not import `region`; it
  defines a narrow interface in its own package (`api.RegionCurrencyReader`)
  and resolves the concrete service from the container under the name
  `region.service` (ADR 0001/0006).

  **This path fails CLOSED too**: if the region surface cannot be resolved,
  the cart is NOT OPENED AT ALL. Falling back to a default — the store's first
  currency, or whatever the client said — would reopen exactly the door that
  was closed. The rationale is written in the godoc of the cart-creation
  endpoint (`internal/modules/cart/api/store.go`).

  **On the admin surface the same field is LEGITIMATE and was not
  removed.** In the `POST
  /admin/v1/regions` body, `currency_code` DEFINES the region: there the
  operator writes the ORIGINAL, not a copy, and there is no source to copy
  from. The criterion is not "is the field in the body" but the question "is
  this value the caller's own data". On `cart`'s own `/admin/v1` surface the
  question never arises: it only reads, and there is no admin endpoint that
  opens a cart.

  New tests: that the field is rejected and the rejected request WRITES no
  cart (unit + e2e), that the currency really comes from the region, that no
  cart is opened when the region surface is missing, that an unknown region
  does not get a cart opened, and that the container name is a contract
  (unit). The real proof is in the e2e: the same variant in TWO regions with
  different currencies gets a DIFFERENT unit price per cart — that the
  currency selects the price becomes visible only in this assertion.


- **B2B module: company, employee and spending limit.** A setup in which the
  buyer is not an individual but an EMPLOYEE whose spending authority per
  period is limited. The module imports no other module; the employee →
  customer tie lives only in `core/link`, and the `b2b_company_employee` table
  has **no** `customer_id` column (keeping the same relationship in two places
  would open a place where they could diverge).

  The rule is split across two modules: the **limit** is `b2b`'s data, the
  **spending** (the total of placed orders) is `order`'s. Since neither may
  import the other, the contract is JSON: `order` defines its own narrow
  interface (`service.SpendingPolicy`) in its own package and resolves the
  concrete type from the container under the name `b2b.interop`. The accepted
  cost of this is that **the compiler does not check this contract**: had a
  field name diverged, the unit tests of both packages would stay green and in
  production the limit would silently disappear — which is why the two ends
  of the contract are joined in e2e over the real container.

  The check runs inside `order.CreateOrder`, **within the transaction** that
  writes the order and under the customer lock. Because `create_order` runs
  **before** `authorize_payment` in the `complete_cart` saga, money is never
  authorized for a rejected purchase; because the check and the write share a
  transaction, two concurrent orders cannot exceed the limit together. The
  rule was put in the service rather than the saga because that is the only
  path in this module that creates an order — had it been put in the saga, a
  second caller added later would silently bypass it.

  A `nil` limit means unlimited; `0` is a real zero limit. The window is
  calendar-based (monthly/yearly, UTC). If the company's currency differs
  from the cart's, the order is rejected; converting would need an
  exchange-rate source, and that decision does not belong to this module.
  When the module is not registered, the behaviour is as if b2b did not exist
  at all.

- **GraphQL's five new hardening limits are now configurable through
  environment variables.** `GRAPHQL_MAX_FIELD_REPETITION`,
  `GRAPHQL_MAX_RESPONSE_BYTES`, `GRAPHQL_MAX_INTROSPECTION_ROOTS`,
  `GRAPHQL_MAX_INTROSPECTION_DEPTH` and `GRAPHQL_MAX_SELECTIONS`. When the
  gates were added they could only be set through `graph.Options`; so the
  gates the operator could not set were precisely **the two of highest
  severity** (byte amplification and the introspection flood), and when a
  legitimate need arose the only recourse was to fork the code.

  The symmetry test in `internal/arch` could not see this gap because it
  compared three limits by hand; the test now walks `graph.Options` **by
  reflection** and enforces that every field matching `Max*` has a
  counterpart in the core. Verified by mutation: removing an entry from the
  mapping makes the test fail.

- **The GraphQL error policy now looks at the error's SOURCE, not its TYPE.**
  The presenter asked "is this an `*errors.Error`" and handed anything that
  was not to the client as is; the core's rule is the exact opposite.
  Measured: when the storefront service returned an unclassified error, the
  response (status 200) carried the text
  `pq: SSL connection error host=db.internal user=gobit password=s3cr3t …;
  SELECT * FROM product_products WHERE id=$1` verbatim, and that error was
  **not written anywhere either** — the same error on the REST endpoint came
  back as 500, `internal_error` and a generic message, and logged the real
  text. Now everything that comes from beneath a resolver, typed or not, is
  handed to `WriteError` (the masking and logging rule is not written a second
  time); parse, validation and limit gates, on the other hand, are returned as
  is and are **not logged** as server errors — otherwise a client's typo would
  be a pipe that can fill the log.

  The same distinction has two side effects:

  - **`GRAPHQL_INTROSPECTION=false` now really hides the schema.**
    The switch closed `__schema`, but the validator kept handing out the
    names piecemeal:
    `prodcts` → `Did you mean "products" or
    "product"?`, `Prodct` → `Did you mean "Product"?`, `limitt` →
    `Did you mean "limit"?`. Because the validator collects every error into
    a single response, dozens of names could be tried in one request, and the
    rate limiter counted that as one request. The switch now also sets
    `SetDisableSuggestion`, and for the rules gqlgen cannot reach, the
    suggestion sentence is cut out of the response. The introspection query
    is also rejected without being executed (`INTROSPECTION_DISABLED`);
    `__typename` is not a root and keeps working.
  - **A malformed JSON body is no longer reflected.** gqlgen's POST transport
    APPENDS the body it cannot decode to the error message (`… body:…`), so
    up to 64 KiB of attacker-controlled text went into the response and into
    the logs of any intermediary layer that records responses. Because the
    transport's errors arrive without a code, they are recognised and their
    text is replaced with our own constants: `REQUEST_DECODE_FAILED` and —
    when the body of a client that did not declare its size is truncated —
    `REQUEST_BODY_TOO_LARGE`, which now states the limit as a number.

- **Four measured gaps in GraphQL hardening closed: response bytes,
  introspection, the query cache and fragment expansion.** All four were
  measured against the real handler, not guessed.

  1. **Response size passed through no gate.** The complexity model prices
     the *number* of fields, not bytes: the document
     `products(limit:100){ items { a0:description … a488:description } }`
     costs exactly **50,000**, so it sat right at the ceiling and passed — an
     8.5 KiB request produced a **204.9 MiB** response (24,620 times the size),
     and the rate limiter counted it as *one* request. Two gates were added:
     how many times the same field may be selected under the same object
     (`MaxFieldRepetition`, default 20; counted per sibling scope, aliases
     ignored) and the **actual** response bytes (`MaxResponseBytes`, default
     4 MiB). The second looks at measurement, not estimation. When the limit
     is hit, **no half-written JSON is sent**: if no byte has gone out yet,
     the oversized body is discarded and a complete error envelope is
     written; if part of it has gone out, the connection is dropped with
     `http.ErrAbortHandler`.
  2. **Introspection was outside both gates.** The depth count skipped the
     `__schema`/`__type` roots, and gqlgen's complexity walk skipped fields
     of type `__Schema`; so the measured depth was 0, the complexity 0, and
     the operator had no setting to turn. Measured: a `__schema` document with
     302 aliases returned 5.00 MiB and got 200 even with
     `Options{MaxDepth: 1, MaxComplexity: 1}` — while the same setting
     rejected `products { count }`. Introspection is now *counted*: the
     number of roots (`MaxIntrospectionRoots`, default 2) and the subtree's
     own depth ceiling (`MaxIntrospectionDepth`, default 15) are limited
     separately. The separate ceiling also removed the old rationale that
     required raising the data limit above 13.
  3. **The query cache kept rejected documents.** gqlgen adds a document to
     the cache right after validation, while the limit extensions run after
     that; so a document that never reached the service still took up space.
     Measured: 100 rejected 65 KB documents left **171.8 MiB** of retained
     heap after `runtime.GC` (26 times the 6.5 MB upload) — and on top of
     that the storefront's real documents were being evicted from the cache.
     The cache is now bounded by **bytes** rather than by entry *count*
     (8 KiB per entry), and a document is stored only **after it has passed
     every gate**. `SetParserTokenLimit` is also set (8,192; it had never been
     called before, i.e. it was unlimited): because the token limit runs
     *inside* parsing it is the cheapest gate, and it rejects the
     302/448-alias introspection documents that fit within the body limit
     without parsing them to the end of the document.

  4. **Fragment expansion was exponential, and every computation that walks
     the tree hung there.** A chain of
     `fragment f(k) on Product { ...f(k-1) ...f(k-1) }` is valid, contains no
     cycle (the only thing validation rejects) and at 26 levels is
     **1,127 bytes** — but its expansion is 2²⁶ selections. Measured: the
     endpoint could not finish this document in ten seconds. The trap was not
     in a single walk; the depth count, the new field-repetition count and
     gqlgen's own complexity walk all three descend into the fragment
     definition without memoization. So the fix bounds not a walk but the size
     of the tree: `MaxSelections` (default 10,000, just above the token limit)
     runs before every other gate and cuts the traversal short the moment the
     budget runs out — so that, while enforcing the limit, it does not do
     precisely the work the limit prevents.

  The calibration table gained a **bytes column** (README and `limits.go`):
  the old table measured only the field count, so it never asked about
  exactly the dimension it missed. The complexity figures in the table are
  now pinned by measurement in `graph/limits_test.go` (the product-page row
  said 1.4 thousand because it did not count the root query cost; measured, it
  came out at 2,368). The new limits are configured through
  `graph.Options`/`product.Options`.

- **Hardening of the GraphQL endpoint: depth, complexity, body and
  introspection.** On this endpoint the cost of a request is decided by
  whoever **writes** the query; the rate limiter, meanwhile, counts a document
  that carries hundreds of root queries through aliases as a single request
  too. Three gates were added, and each catches the document the others
  cannot see (the rest are in the entry above): depth (`GRAPHQL_MAX_DEPTH`,
  default 10), complexity (`GRAPHQL_MAX_COMPLEXITY`, default 50,000) and a
  64 KiB body limit — since the first two can only be measured after the
  document has been parsed, only the last one bounds the cost of parsing. The
  complexity model **multiplies the cost of list fields by the element
  count** (a fixed cost would make exactly the expensive query look cheap)
  and additionally charges root queries the price of a database round trip.
  The limits **can be raised, not removed**: a zero/negative value is invalid
  and stops startup. Introspection became configurable
  (`GRAPHQL_INTROSPECTION`) and its default was left **on**: the schema is a
  file that sits inside this repository, so turning it off hides nothing from
  an attacker but blinds code generators. `product.New` now takes
  `product.Options` (breaking: because the module does not know the config,
  the limits are passed in from the composition root).

- **GraphQL storefront read surface (`POST /store/v1/graphql`).** The catalog
  is read from a second surface: the `products` and `product` queries, with
  the fields `StoreProduct` returns today. The schema
  (`internal/modules/product/graph/schema.graphqls`) is the hand-written
  contract, and the Go side is generated from it (gqlgen, `make gen`).
  Resolvers call the **storefront service** — they do not go down to the
  repository and no new SQL is written: a second implementation of the sales
  channel visibility rule would leak the catalog the day it diverged. Channel
  IDs are read from the `Principal` and have **no** argument in the schema;
  because the endpoint sits under `/store/v1`, the publishable key and the
  rate limit come automatically from the stack. There is no write surface and
  no GET (POST only; since the response varies by channel, GET would bring no
  caching benefit, only a cost). Price and stock are JSON scalars because
  other modules own them; the error body goes through the core's
  `WriteError`, so the masking rule and the error codes are the same as in
  REST.

- **`FileProvider` and the `file` module — plan Section 5.6 completed.** The
  last of the four provider abstractions. `POST /admin/v1/uploads`
  (multipart) → the returned address → `GET /files/{anahtar}`. The generated
  URL plugs directly into the existing product image flow, so a real consumer
  path exists without touching the `product` module.
  This is the first place in the repository that accepts **arbitrary bytes**
  from a client; the security rules are structural: the storage key is
  GENERATED (the client's file name never enters any path expression, so path
  traversal is impossible), the content type is not asked of the client but
  detected from the content, there is an allow list (not a deny list) and SVG
  is excluded, the size limit is enforced both on the body and on the file,
  `Content-Type` is written from the stored type when serving, and `nosniff`
  is present on every response.

### Fixed

Settings that **silently** broke an operator's setup were closed off. What
they had in common is that none of them produced an error and all of them
became visible only in production — when a limit was exceeded, on the first
login attempt, or when images went missing:

- **The event bus did NOT separate two installations sharing the same
  Redis.** `cmd/server` built the bus with a zero-valued
  `eventbus.RedisConfig`: both the stream prefix and the consumer group fell
  back to the package default, so `REDIS_KEY_PREFIX` NEVER reached the event
  side. The guard keys were separated; the events were not.

  Sharing the group is the worse of the two: by the very definition of a
  consumer group, only **one** of the group's consumers receives a message,
  so production's `order.placed` event could be consumed and swallowed by
  staging — the order is placed, the confirmation notification goes nowhere,
  and no error shows up anywhere.

  The namespace is now derived from the prefix (`<prefix>:events:<event>` and the
  group `<prefix>`), and the derivation lives in a single place,
  `eventbus.RedisConfig.WithNamespace`; the package defaults are also read
  from that derivation (`DefaultStreamPrefix = DefaultGroup + ":events"`), so
  a default installation and a separated one cannot half-diverge. With the
  default prefix the result is **exactly the same as today**: an upgraded
  installation keeps its stream and its group in place.

  The consumer name works in the opposite direction — it separates not
  installations but the **processes** within the same group — and so it was
  not tied to the namespace. Instead, `EVENT_BUS_CONSUMER` was added: the
  godoc of `RedisConfig.Consumer` said a stable identity (the StatefulSet pod
  name) had to be "given explicitly", but there was NO environment variable
  to give it with. Giving the same name to two instances silently leads to
  double processing (both read that name's pending list at startup), and a
  single process cannot see this; that is why the resolved name is
  **logged** at startup.

- **`TRUSTED_PROXY_HOPS=0` behind a reverse proxy collapsed the rate limit
  into a store-wide one, and did so silently.** With the value at zero,
  `X-Forwarded-For` is never read and the key falls back to `RemoteAddr`;
  behind a reverse proxy / ingress / CDN that address is the proxy's ON EVERY
  REQUEST, so `RATE_LIMIT_PER_MINUTE` becomes not "600 per customer" but "600
  per minute for the whole store", and a single customer can lock up the
  storefront.

  **The default did not change and startup does not stop**, because the
  costs of the two mistakes are not of the same class: a value set too high
  makes a client-invented address count as real, and an attacker bypasses the
  limit ENTIRELY by taking a fresh bucket on every request — a security hole;
  a value set too low only loosens the protection. Zero is the CORRECT answer
  for an installation that faces the internet directly, and the configuration
  cannot know which case applies. What was added is the repository's own
  pattern: a shared installation that starts with zero hops while the rate
  limit is on now produces a **warning** at startup (the same gate as the
  `GUARD_BACKEND=memory` and `FILE_ROOT` warnings). The definition of "risky"
  lives in config (`Config.RateLimitKeyIsPerClient`); the side that writes the
  warning lives in `cmd/server`.

- **`EVENT_BUS=inmemory` logged only INFO in a shared environment**, while
  its equivalent `GUARD_BACKEND=memory` produced a WARN — the same trade-off
  was visible in one and invisible in the other. The in-memory bus WORKS but
  is not durable: delivery is asynchronous, and if the process crashes or
  shutdown does not finish within `SHUTDOWN_TIMEOUT`, undelivered events are
  lost without a trace. Now WARN in a shared environment, INFO in local
  development.

- **With `RATE_LIMIT_PER_MINUTE <= 0`, the fact that the limiter was not
  built at all was not reported, not even with a single line.** Turning it off
  is a legitimate choice (in ADR 0007 zero means "off"), but it also leaves
  the login endpoint without a quota, and an "off" that nobody knows about
  cannot be told apart from an accidentally written zero. Now WARN in a shared
  environment, INFO in local development.

- **A fresh database + an empty `ADMIN_BOOTSTRAP_*` pair started silently.**
  Leaving both empty passed `config.Validate`, and rightly so: for an
  ALREADY-INSTALLED system it is a legitimate choice, and validation cannot
  see the question "is it installed". But if the database is empty too, the
  result is an unmanageable installation — there are no users, the admin
  surface is fully protected apart from the login endpoint, and there is no
  way to create the first user over HTTP; the store surface is closed too,
  because the publishable key is also produced by an admin endpoint. The
  server started anyway, `/health` and `/ready` came back green, and the
  failure stayed invisible until the first login attempt.

  The seed step now reads the user count IN EVERY CASE, and zero users + a
  seedless configuration **stops startup** in shared environments
  (`admin_bootstrap_required`) and produces a warning in local development.
  There is no uncertainty here — `FILE_ROOT` settles for a warning because it
  is not certain that the configuration is wrong, whereas it is certain that
  a zero-user installation is unmanageable. The distinction is the same as
  for `JWT_SECRET`, and the promise "`make up &&
  make run` works without a .env" is kept.

- **`Config.LocalFileRootIsDurable` (named LocalFileRootIsPortable at the
  time) looked only at `filepath.IsAbs`.**
  `FILE_ROOT=/tmp/gobit-uploads` is absolute, passes the "do not give a
  relative path" advice, and the warning would go quiet — yet `/tmp` (and
  `/var/tmp`, `/dev/shm`, `TMPDIR`) are cleaned up by the operating system,
  and since they are tmpfs on most distributions they do not even wait for a
  reboot. So the silent data loss that the `Config.FileRoot` godoc REJECTED
  for the default came back through a one-line setting. The criterion is now
  not "is it independent of the working directory" but "does it stay in place
  when the process restarts"; the name was brought in line with the
  behaviour as well: `LocalFileRootIsDurable`.

- **The `validateFile` godoc read as if it ENFORCED the absolute-path
  requirement**, whereas validation does not stop anything and `cmd/server`
  only logs a WARN. The documentation was brought in line with the behaviour:
  durability is a warning, not a validation, and its rationale is in the
  `LocalFileRootIsDurable` godoc.

- **The comment on the b2b registration in `cmd/server` contradicted
  itself**: it said "when this line is deleted ... a pure B2C setup is
  obtained WITHOUT CHANGING THE CODE", whereas deleting the line is a code
  change. The sentence was corrected, and why an environment variable that
  turns b2b off was **not added** was written down: a switch accidentally set
  to `false` would remove the spending limit without producing any error —
  that is, it would be a new member of the very silent-failure class this
  section closes. The code path, for its part, cannot be left half-done;
  `TestEveryModuleIsRegisteredInTheCompositionRoot` asks whoever deletes the
  line to write the decision down together with its rationale. The cost of
  leaving the module in a B2C setup is small and visible, too: two empty
  tables and a rule that never fires.

Findings from the independent verification that followed the wiring of the
cart workflows:

- **A saga step error lost the CODE of the underlying error.**
  `internal/core/workflow`, when wrapping a failing step, inherited the
  error's CLASS (`Kind`) from the underlying error but overwrote its CODE with
  its own constant (`workflow_step_failed`). The transport layer writes a
  single machine-readable field into the body (`error.code`), so every saga
  error flattened into ONE value for the client. The concrete cost was the
  B2B spending limit: a purchase that exceeded the limit got `409`,
  `spending_limit` appeared NOWHERE in the body, and the storefront could not
  tell "your limit is not enough" from "transient conflict, try again" —
  whereas `409` is precisely the class that a retry does not solve. The code
  is now preserved (`stepFailureCode`); a step error with no code gets the
  engine's own constant. ONLY the code is carried over: the message and
  `Details` stay in the chain and are still masked on `KindInternal` errors.
  The LIMIT of the change was drawn with a test too — when compensation
  fails, the outer code STAYS `workflow_compensation_failed`, because what
  has to be read there is not why the step failed but that the system has
  been left inconsistent.

- **Failing CLOSED returned the wrong status class (`404`).** When the
  line pricing / cart completion workflow could not be resolved, the `cart`
  module passed the container's error class through as is: an unregistered
  name was `KindNotFound` → `404`, a registration of the wrong type
  `KindInvalid` → `422`. Money-wise the behaviour was right (the line is NOT
  WRITTEN), but the class was wrong: `404` tells the client "there is no such
  endpoint", the `5xx` alert chain never rings, and intermediaries can cache
  the response and keep the failure going even after the setup has been
  fixed. The wrapping is now `KindInternal`; what it tells the operator is
  preserved, the text sent to the client goes through the core's masking
  rule, and only the `cart_module_setup_failed` code remains. The same class
  of bug was also fixed in the `order` module's spending-rule wrapper.

Six findings from an adversarial security review:

- **The idempotency middleware killed the upload stream.** The ENTIRE body of
  a multipart request carrying an `Idempotency-Key` was buffered in memory
  for the fingerprint, streaming lost its point, and the middleware's 1 MiB
  buffer kicked in BEFORE the upload endpoint's own limit — the client got
  "body too large" below the limit it had configured. Streamed bodies are no
  longer recorded.
- **`/files` was outside the guard stack**: unauthenticated AND without a
  quota, and every request a database read on top of that. Being
  unauthenticated is not the same as being unprotected;
  `GuardOptions.OpenPrefixes` was added. The health endpoints are
  deliberately left outside.
- **~11x response amplification through a multi-range `Range`**:
  `ServeContent` limits the total bytes of the ranges, not their NUMBER. A
  single range is kept; with multiple ranges the header is dropped.
- **`Cache-Control: immutable` was wrong**: the key is never reused, but the
  content CAN BE DELETED; a shared cache would keep serving a deleted file for
  another year. The lifetime was cut to one hour and `immutable` was removed.
- **`FILE_ALLOWED_TYPES` accepted types that execute in the browser.** In an
  installation that lists `text/html` the chain works and becomes stored XSS;
  `nosniff` does NOT stop this, because the response really is of that type.
- Cleanup of the temporary upload file was not deferred (a leak on panic).

- **`NotificationProvider` and the `notification` module.** Plan Section 5.6
  counts FOUR provider abstractions (payment, fulfillment, notification,
  file); only two of them existed in the code. This work closes the third,
  and a second gap at the same time: `order.placed` was published but **had
  not a single subscriber** — the search plugin listens to product events,
  not order events. Notification is the first real consumer of that event.
  The default provider is `log`, and it **says that it did not really
  send**: it writes "notification NOT SENT" at WARN level, does not log the
  recipient, and prints only the keys of the template data, not their values.
  A silent "it went out" lie would have meant believing the order
  confirmation had reached the customer.
  An unknown `NOTIFICATION_PROVIDER` name stops startup.
  The delivery log **does not store the recipient address**: the email is
  already on the order record, and a second copy would increase the number of
  places it has to be deleted from.
  `(template, reference)` is unique — no notification goes out twice for the
  same order.
  The subscriber reads the email **from the record, not from the event** (the
  event payload puts no PII into the durable stream); for this,
  `order.interop` opened a narrow read surface (`OrderContactJSON`). The
  end-to-end test nails down exactly this distinction.

- **Smoke tests: a real process, real migrations, a real signal.**
  While unit + integration tests (~76% coverage) and lint passed CLEAN,
  running the application by hand had turned up four failures; all four were
  hiding in the wiring of `main.go`, the startup migrations, config loading
  and signal handling. `internal/e2e` cannot see them: it drives the router
  through `httptest`, so it is NOT a real startup.
  `internal/smoke` builds the server binary and runs it **as a process**, and
  ties that class of error to CI: a cold start + the README flow, three
  instances starting concurrently against the same empty database (the
  regression of the seed race), five wrong configurations stopping at startup
  with an understandable message, both forms of the OTLP address being
  accepted and the `METRIC_EXPORT_INTERVAL` name clash not coming back, and a
  clean shutdown with exit code 0 after SIGTERM.
  Two regressions were verified **by mutation**: when the seed fix is
  reverted, the concurrent-startup test fails; when the name clash is brought
  back, the tracing test fails.
  It runs with `make smoke`; in CI it is a SEPARATE job — "integration
  failed" and "the application does not start" must not show up on the same
  line.

### Removed

- **The `cart_customer`, `cart_region`, `order_customer` and `order_region`
  link definitions.** All four were WRITTEN on every cart/order and never
  TRAVERSED. This is **not a lost feature**: every read these ties carried is
  already done by columns. `carts.region_id` / `carts.customer_id` and
  `orders.region_id` / `orders.customer_id` are both the source and indexed;
  the customer and region filters (`ListCarts`, `ListOrders`,
  `order/queries/orders.sql`) run on exactly those columns. The link table
  was a second copy of the same relationship; it wrote rows, paid the cost of
  the cardinality constraint, and produced no behaviour in return.

  This reverses a deliberate design decision. The ties had been declared to
  serve as "a mirror opened onto the Query layer", and the `ManyToMany`
  cardinality had been chosen precisely for the sake of that mirror
  (uniqueness was already guaranteed by the column). No reader that looked
  into the mirror ever appeared: neither a `query.Expansion` nor a module API.
  What found this was the `TestTheLinkDefinitionsAreTraversed`
  invariant in `internal/arch/consumers_test.go` — the link-surface side of
  the rule "every capability produced has a consumer". The previous case of
  the same class was the product ↔ sales channel failure.

  The compensating deletes went too, and **the code came out simpler for
  it**: because the tie was not in the same transaction as the cart row,
  `CreateCart` rolled the cart back when the tie could not be created,
  `UpdateCart` rolled back a customer handover, and `CreateOrder` linked
  BEFORE writing the order and cleaned up the tie when the write failed. Now
  both are a single write transaction; there is no longer any such thing as a
  compensation path, a compensation of the compensation, or a "ghost order"
  window. The `core.link` dependency of the `cart` and `order` modules is gone
  entirely (`service.Linker`, `Options.Links`, `Definitions()`).

  **Database: NO migration was written, and this is deliberate.** The link
  schema is the product of the startup declaration, not of a migration, and
  its owner is `core/link` (ADR 0005); a module's migration dropping another
  subsystem's table is exactly what `b2b`'s down migration also deliberately
  does not do. Concrete consequences:

  - The `link_cart_customer`, `link_cart_region`, `link_order_customer` and
    `link_order_region` tables are **left orphaned** on existing
    installations. They are harmless: no code path touches them, and the IDs
    they point to are never produced again. Cleanup is an OPERATIONAL decision
    and is done by hand (`DROP TABLE IF EXISTS link_cart_customer, link_cart_region,
    link_order_customer, link_order_region;`). The reason it is not
    automated is written in ADR 0005: dropping the table by looking at the
    code would mean that a definition that went missing temporarily because
    of a deployment mistake deletes all of its ties — and **a deleted row
    does not come back**.
  - The rows for these four names remain in the `link_definitions` table.
    They **produce no conflict** at startup: `LinkService.Define` reads and
    compares only the row of the name it declares itself (upsert +
    `RETURNING`, see `core/link/service.go`); it does not scan the ledger
    against the code. A row that does not come from the code is never read.
    The only conditional consequence is this: if a link is later declared
    with the same NAME but different ends, startup stops with
    `errors.Conflict` — which is the ledger doing its job, not a failure.

### Known limitations

The following were INVESTIGATED in this round, decided on, and DELIBERATELY
left open. A gap that never makes it onto the record is a gap nobody closes.

- **`POST /store/v1/carts` still takes `region_id`.** `currency_code` was
  REMOVED from this body (see above); the region ID stayed, and it is in the
  same class — the region selects the tax RATE. The blast radius shrank by
  two steps: the region's actual existence is now validated (the currency is
  read from it), and the choice's effect on the price list is gone. The right
  place to close it is still not the handler: a workflow that already does
  the derivation exists — `create_cart` resolves both the region and the
  currency from the country code. The body needs to be reduced to
  `country_code` and the endpoint handed over to that workflow; this was not
  taken into this round because it requires adding a method that is
  deliberately absent from the workflow's cross-module surface today, and
  breaking the store contract once more. Rationale in the
  `api.createCartRequest` godoc.

- **Storefront carts have no OWNERSHIP check — this is the model, and it is
  now WRITTEN DOWN.** The endpoints under `/store/v1/carts/{id}` do not verify
  that the requester owns the cart. This is a "capability URL" model: the
  cart ID is generated from a 48-bit timestamp + 80 bits of cryptographic
  randomness, it cannot be guessed, and knowing it carries the right of
  access. It also arises out of necessity — the store surface's only identity
  is the publishable key, and that is not a SECRET; there is no customer
  session. The same statement was already written in the `order` module; for
  `cart` it was written nowhere, and now it sits in the package documentation,
  together with the model's rule (there is NO LIST endpoint on the storefront
  side; a list endpoint would turn knowing a single ID into reading every
  cart).

  What the model does NOT COVER was named separately: a capability URL says
  "I can access the ID I hold", it does NOT say "I am this customer". Yet the
  `customer_id` in request bodies is an unproven claim of ownership, and the
  cart's customer determines which company's window the b2b spending limit is
  deducted from — so the claim can use up someone else's window. The service
  protects only a single boundary (a cart that has a customer cannot be
  handed over to someone else). The only correct closure is a customer
  session (Phase 8), and no made-up authorization mechanism was BUILT in this
  round.

## [0.3.0] — 2026-08-31

The API now describes itself: a client that works from the schema can be
generated.

**There are NO breaking changes.** The exported API of the `core/openapi`
package only grew (methods were added, none were removed), and the removed
`List` component was not published in v0.2.0 in the first place — it was
added and removed within the same unpublished window, so it never made it
into a client anyone generated.

### Added

- **The whole API surface is described (196 endpoints).** The schema now says
  what each endpoint takes and what it returns. Measured:
  `openapi-generator v7.10.0` validates the schema with **zero findings** and
  generates a TypeScript client with 237 models.
  The `POST /admin/v1/users` example sums up the difference —
  before, `postAdminV1Users(): Promise<void>` (no body, no return, unusable),
  after, `postAdminV1Users(req: PostAdminV1UsersRequest): Promise<…201Response>`.
  A client can be generated with `make openapi-client DIL=…`; no SDK is
  VENDORED into the repository, because since the schema is generated from
  the router, versioning a second artefact and keeping it in sync would be a
  needless burden.
- **The OpenAPI schema now describes bodies.** The schema was syntactically
  valid but semantically EMPTY: `Doc.Describe` was not called anywhere, and
  each operation carried only `operationId`, `tags`, `security` and the
  GENERIC error responses (401/422/429/500). `POST /store/v1/carts` had
  neither a `requestBody` nor a 2xx response — from that, a client generator
  would produce methods where everything is `any` and the return type is
  `void`.
  Body schemas are now **derived from Go types by reflection**: a
  hand-written field list falls behind the day a field is added to the DTO,
  and nobody notices.
  The derivation mimics the behaviour of `encoding/json` (tags, `omitempty`,
  unexported fields, flattening of embedded structs, and **shadowing**).
  Modules describe their own endpoints through the optional
  `openapi.Describer` interface; the `module.Module` contract did not change.
  Today the storefront endpoints of `cart` and `product` are described.

### Changed

- **The unused `List` component is no longer published.** The real generator
  reported it as an "unused model"; it was a dead class in every generated
  client. Wiring it by default to the undescribed list endpoints was tempting
  but would have been wrong: an endpoint cannot be written into the schema as
  returning a list until it has been verified that it really does.
- **Schema component names were normalised** (`cartDTO` → `Cart`). A
  component name is not an internal detail but the published contract; client
  generators produce class names from it. Without normalisation,
  `StoreProduct` (exported) and `cartDTO` (unexported) would have stood side
  by side in the same document, and the generated client would have had two
  different naming schemes.

## [0.2.0] — 2026-08-31

What was found after the roadmap was finished. There is a common pattern:
most of the work in this release is not new features but writing consumers
for capabilities that were **built but had no consumer** — the sales channel
was validated but not read, the event bus was ready but had a single event,
`Host.AddModule` had never been used.

### Breaking changes

Breaking changes may land in minor releases throughout `0.x`. Those in this
release affect only code that **embeds the modules**; clients that use the
HTTP API are not affected.

- The `product` module REQUIRES the `core.eventbus` service during
  `Register`; without it, startup stops. Silently skipping it would have meant
  the index silently going stale while the catalog kept working.
- The `product/repository.Store` interface grew
  (`ProductVisibleInSalesChannels`, `VisibleProductIDs`); code that writes its
  own implementation must add these.
- `product/service.GetStoreProduct` now also takes the sales channel IDs.
- The `Inventory` and `Fulfillment` narrow interfaces of `workflows/checkout`
  grew (`LocationsWithStock`, `SelectLocation`).

### Added

- **Domain events and a real plugin: search.** `order.placed` was the ONLY
  event in the repository — the event bus was fully built (in-memory + Redis
  Streams, consumer group, XACK), it was a core contract in plan Section 5.4,
  and `Host.Subscribe` was ready for plugins, but there was almost nothing to
  subscribe to.
  `product` now publishes `product.created` / `product.updated` /
  `product.deleted` (the doctrine of the order events: a narrow payload, every
  value a string, no personal data in the durable stream).
  `plugins/searchpg` is the first **real** plugin to consume them: it brings
  its own module, its own table and migration (`Host.AddModule` had never been
  used until now), does PostgreSQL full-text search, and exposes the
  `GET /store/v1/search` and `POST /admin/v1/search/reindex` endpoints.
  There is deliberately no external service: thanks to the plugin boundary,
  moving to Meilisearch/OpenSearch later changes nothing anywhere else.
  **Search is not a bypass of channel filtering** — the plugin indexes only
  IDs, `product.interop` fetches the records, and the visibility rule stays in
  one place.
- **Multi-warehouse: stock is reserved per line, from the right warehouse.**
  The "SINGLE LOCATION ASSUMPTION" in the `complete_cart` saga was removed —
  the code promised this change "in Phase 7"; Phase 7 had finished and the
  assumption had stayed.
  `CompleteCartInput.LocationID` is now **optional**: if it is set, the old
  behaviour is kept exactly; if it is empty, the location is chosen per line
  and the lines of one order can be reserved from different warehouses.
  The division of labour is deliberate — "which warehouses have enough
  stock" is a **stock fact** (`inventory.interop.LocationsWithStock`), "which
  one do we ship from" is a **shipping decision**
  (`fulfillment.interop.SelectLocation`).
  If the chosen warehouse has run out at reservation time, the next candidate
  is tried; this happens only on a conflict, and other error classes are not
  retried.
- **The `product↔sales_channel` tie and storefront catalog filtering.** The
  last missing tie on the plan's "important links" list was built: the
  channel a publishable key is bound to now really determines the catalog.
  Previously the key was validated and `Principal.SalesChannelIDs` was
  filled, but no module read it — every key saw the same catalog.
  The filter is applied in the database (`EXISTS`/`NOT EXISTS`), so
  pagination and the total count work on the filtered set. The channel is
  read from the identity, NEVER from the query string.
  New endpoints: `POST`/`DELETE`/`GET /admin/v1/products/{id}/sales-channels`.

## [0.1.0] — 2026-08-31

The whole of the plan's Phase 0–9 roadmap. A headless commerce core that runs
as a single binary, with NO compile-time dependency between modules.

### Added

**Core**
- Module contract and lifecycle (`Register` → migration → `Routes`),
  hand-written DI container ([ADR 0002](docs/adr/0002-di-container-el-yazmasi.md)).
- Module Links — relationships between modules WITHOUT foreign keys;
  cardinality is enforced by a database constraint
  ([ADR 0005](docs/adr/0005-link-semasi-migration-disinda.md)).
- Query layer — cross-module reads; N+1 is structurally impossible
  ([ADR 0004](docs/adr/0004-query-veri-erisimi.md)).
- Saga engine — compensation in reverse order, retry, idempotency key, panic
  isolation; execution state in Postgres.
- Event bus — in-memory (development) and Redis Streams (production).
- A separate migration folder and version table per module; cancellable
  migrations ([ADR 0003](docs/adr/0003-migration-iptali.md)).

**Commerce modules**
- Catalog: `product`, `pricing`, `inventory`.
- Cart: `cart`, `customer`, `region`.
- Order: `payment`, `order` — the `complete_cart` saga.
- Phase 7: `fulfillment`, `promotion`, `tax`.
- Identity: `auth` — admin user, JWT session, publishable/secret API key,
  sales channel.

**Security**
- Two surfaces, two identities: `/admin/v1` takes a Bearer token or a secret
  key, `/store/v1` a publishable key.
- Authorization (scope) is enforced endpoint by endpoint in ALL modules
  (`<module>:read` / `<module>:write`, with `admin` as the superscope);
  privilege escalation is additionally blocked in the service layer.
- First admin seed (`ADMIN_BOOTSTRAP_*`) — runs only when there are no users
  at all, and absorbs the race on concurrent startup.
- Session revocation: password change and `POST /admin/v1/auth/logout`; both
  drop ALL of the caller's sessions.

**Hardening**
- Rate limit, idempotency and authentication middleware; failure behaviour
  varies by component ([ADR 0007](docs/adr/0007-sertlestirme-arizada-davranis.md)).
- A shared rate-limit and idempotency store with `GUARD_BACKEND=redis` —
  for multi-instance deployments.
- OpenTelemetry traces + metrics; if no collector is given, tracing is
  genuinely off.
- Plugin system (compile-time registration) and a `payment-stripe` skeleton.
- OpenAPI schema generated from the router tree (`/openapi.json`).

**Verification**
- Architectural invariants are enforced by tests: module isolation, the ban
  on cross-module FKs, plugin isolation, godoc format, integer money.
- End-to-end tests build the modules with the PRODUCTION wiring; the
  authorization invariant checks every admin endpoint by walking the router
  tree.
- A basic load test (`make load-test`).

### Fixed

Three failures found before this release by actually running the
application, which running the tests alone did not reveal:

- **Seed race on concurrent startup.** When several instances started against
  an empty database at the same time, all but one died with
  `admin_bootstrap_failed`. A conflict is now treated as a race, not a
  failure.
- **`OTEL_EXPORTER_OTLP_ENDPOINT` SILENTLY swallowed the format from the
  spec.** Given `http://host:4317`, the application logged "tracing set up"
  and sent no spans at all. Both forms are now accepted.
- **The name `OTEL_METRIC_EXPORT_INTERVAL` clashed with OpenTelemetry.**
  The spec wants an integer in milliseconds, while this package reads a Go
  duration; a value that followed the spec brought the application down at
  startup. The variable became `METRIC_EXPORT_INTERVAL`.

### Fixed

- **`make migrate-up` was talking nine phases out of date.** It told the
  operator "the core/db migration runner will come into play in Phase 1";
  Phase 1 had finished nine phases earlier and migrations were applied
  automatically at startup. The targets were brought in line with reality, and
  the fact that there is NO rollback path was stated explicitly.
- **Coverage measurement was off by 22 points.** CI measured without
  `-coverpkg`, so a package was not counted when ANOTHER package's test
  covered it. Two separate numbers are now reported: unit only (~55%) and
  unit + integration (~76%).
- **N+1 on the search path.** Visibility, which was asked per ID, was turned
  into a batch query; since it is generated from the same SQL template, the
  rule still lives in one place.
- **A race in multi-warehouse.** The candidate list is read without a lock,
  while the reservation is locked; if the chosen warehouse has run out in the
  meantime, the next candidate is tried. Previously the order failed outright
  — and with stock sitting in another warehouse, no less.

### Known limitations

- Session revocation is all-or-nothing only; a single device cannot be
  signed out.
- Cross-module signatures are not checked at compile time
  (the accepted cost of [ADR 0001](docs/adr/0001-modul-arasi-iletisim.md)).
- Single-location assumption in stock.
- **A product with no channel assignment is visible in every channel.** The
  rule is deliberate and backward compatible, but it has a trap: deleting the
  last channel tie does not hide the product, it opens it up to every
  storefront. Use `status` to hide it. The strict alternative ("an unassigned
  product is visible in no channel") should be considered for the next minor
  release — the day it is switched on, it empties existing catalogs.
- **There is no migration rollback path.** Every module has `.down.sql` files
  and their reversibility is checked by tests, but there is no surface to call
  them; rollback is done by hand. The forward direction is automatic at
  startup.
- The load test is in-process; it does not produce a capacity plan.

[Unreleased]: https://github.com/bdrtr/gobit/compare/v0.9.0...HEAD
[0.9.0]: https://github.com/bdrtr/gobit/releases/tag/v0.9.0
[0.8.0]: https://github.com/bdrtr/gobit/releases/tag/v0.8.0
[0.7.0]: https://github.com/bdrtr/gobit/releases/tag/v0.7.0
[0.6.0]: https://github.com/bdrtr/gobit/releases/tag/v0.6.0
[0.5.0]: https://github.com/bdrtr/gobit/releases/tag/v0.5.0
[0.4.0]: https://github.com/bdrtr/gobit/releases/tag/v0.4.0
[0.3.0]: https://github.com/bdrtr/gobit/releases/tag/v0.3.0
[0.2.0]: https://github.com/bdrtr/gobit/releases/tag/v0.2.0
[0.1.0]: https://github.com/bdrtr/gobit/releases/tag/v0.1.0
