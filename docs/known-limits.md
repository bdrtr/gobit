# Known limits

What this framework does NOT do, and the argument for each absence.

It is for the person deciding whether gobit fits: an operator sizing a
deployment, an engineer embedding it, a reviewer asking what was traded away.
Nothing here is a to-do list. Every entry was investigated, decided on, and its
justification lives in the godoc of the code it constrains — an opening nobody
wrote down is an opening nobody closed.

The entry point for the framework itself is the [README](../README.md); the
decisions are under `docs/adr/`.

This file is in English because ADR 0012 makes language a property of the file
and every new file is English.

---

This document describes **today**: what follows are limits that still hold on
`main`. A version name is deliberately NOT written — it would be a dated claim
needing an update at every release cut, and one that goes quietly stale when it
does not get one. What has closed DROPS OUT of here; which release closed it
stands in [`CHANGELOG.md`](../CHANGELOG.md), because that is a record of the
past and is not corrected retroactively.

## Identity and authorization

- **gobit issues no customer identity, and since
  [ADR 0043](adr/0043-gobit-requires-an-identity-it-still-does-not-issue.md) and
  [ADR 0057](adr/0057-one-comparison-holds-the-storefront-customer-claim.md)
  yours decides every storefront request that names a customer.** This is the
  one limit in this file that an embedder has to act on rather than merely
  accept. Fifteen routes ask: the profile, the six of the address book and the
  three of the wishlist, b2b's company and employee reads, and the two cart
  bodies — cart creation and the guest-to-registered handover, the last two only
  when the body carries a `customer_id`. The answer comes from `corehttp.Identity`, a one-method
  interface the framework publishes and does not implement; the embedding
  application registers its own from an ordinary module, in the container, under
  the name `corehttp.IdentityName` (`"core.identity"`). Bound, it decides all
  fifteen: a request naming somebody else gets `403 identity_mismatch`.
- **With NO identity bound all fifteen refuse, and until ADR 0125 four of them
  did not.** Every one of them answers `401 identity_not_bound` — closed rather
  than open, which is
  [ADR 0007](adr/0007-sertlestirme-arizada-davranis.md)'s row for an unconfigured
  authenticator, and an installation upgrading past `v0.8.0` without binding one
  loses its address book, loudly. The four ADR 0057 added — b2b's two reads and
  the two cart bodies — served the claim unchecked until
  [ADR 0125](adr/0125-serving-an-unverified-customer-claim-is-a-choice.md), so a
  caller who knew an identifier, which travels in every order response, read that
  person's company and allowance and opened a cart in their name. That is now a
  CHOICE rather than a default: `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM=true`
  restores it for an installation that wants it, and the two modules still log a
  WARN naming the empty slot. Guest carts are untouched by either answer. The way
  to close the four properly is unchanged and it is one line of wiring: **bind a
  verifier**. What gobit still does not do is VERIFY the proof itself, and since
  [ADR 0126](adr/0126-a-customer-identity-passes-a-published-suite.md) it does
  hold the SHAPE: `core/identitytest.Contract` refuses an implementation that
  hands the claimed identifier back, invents one, returns one beside an error or
  eats the request body. It opens no signature and never could, so a green run
  means the shape is not wrong rather than that the session scheme is sound —
  and nothing makes an embedder run it. `POST /store/v1/customers` is outside the set because it mints the record;
  so is the order module's storefront read, which names a cart rather than a
  person.
- **Since [ADR 0127](adr/0127-a-working-identity-ships-outside-the-module.md)
  there IS one to bind, and what it does not close is written here rather than
  discovered.** `contrib/identity-session` is a working customer identity in a Go
  module of its own: a signed cookie, argon2id passwords, its own table, storefront
  sign-in and sign-out, an operator endpoint, and since
  [ADR 0133](adr/0133-a-shopper-opens-their-own-account.md) self-registration behind
  a proven address, and since
  [ADR 0373](adr/0373-a-shopper-resets-a-forgotten-password.md) a password reset
  behind the same proof, and since
  [ADR 0377](adr/0377-a-shopper-moves-their-account-by-proving-the-new-address.md)
  an address change behind a link to the new address. `contrib/identity-passkey` adds both WebAuthn ceremonies and,
  since [ADR 0130](adr/0130-a-person-can-see-their-passkeys-and-remove-one.md),
  listing a person's keys and removing one. Neither is in gobit's own dependency
  graph — a separate `go.mod`, decided on a measurement: go-webauthn brings nine
  modules gobit does not otherwise have, and an installation that wants a password
  should not pay for them. Binding one is the one line of wiring the bullet above
  asks for, and it is not a verifier gobit vouches for; it is one that passes
  `core/identitytest.Contract`, which holds the shape and opens no signature. Its
  limits:
    - **One session cannot be ended alone.** A session is a signed cookie with no
      record of its own, which keeps a write off every sign-in; an admin session
      has one since [ADR 0267](adr/0267-a-session-can-be-closed-alone.md). What
      a shopper's credential keeps since
      [ADR 0374](adr/0374-a-replaced-password-ends-the-sessions-before-it.md) is
      the moment their sessions count from: a password reset, an operator
      replacing a password and `POST /store/v1/auth/sessions/revoke-others` move
      it, and every cookie issued before it proves nobody — all of them or none,
      at the price of one primary-key read on each request whose cookie is
      otherwise good. A customer with no password here, signed in by the passkey
      module alone or by an embedder's own code, has no such moment, and a
      credential store an installation binds itself keeps one only if it offers
      `SessionAnchors`. Rotating the signing key does NOT log anybody out
      (`RetiredSecrets`, ADR 0129) — which is the point, and therefore not a
      revocation either. A key that LEAKED is dropped outright, which logs
      everybody out and is the correct price.
    - **A stolen cookie IS the account.** With the passkey module bound it can
      register its own key and remove the owner's. The rule those endpoints enforce
      is "an account keeps a way in", not "only the owner changes credentials", and
      a gate that looked like the second while enforcing the first would be worse
      than none — so it is named instead of guarded. Changing the password and
      moving the account are the exceptions: each asks for the current one
      ([ADR 0375](adr/0375-a-signed-in-shopper-changes-their-password.md),
      [ADR 0377](adr/0377-a-shopper-moves-their-account-by-proving-the-new-address.md)).
      What these modules do about the rest is tell: a password replaced or an
      account moved ([ADR 0379](adr/0379-an-account-is-told-what-changed.md)), a
      passkey added or removed
      ([ADR 0381](adr/0381-an-account-is-told-its-passkeys-changed.md)), each
      only when the installation binds the seam. Telling is not stopping, and
      stopping it is outside these modules: a shorter TTL, a re-authentication
      step of its own.
    - **A copied passkey is caught only by its signature counter, and only on a
      device-bound key.** A key whose counter does not advance is suspended
      ([ADR 0382](adr/0382-a-passkey-whose-counter-does-not-advance-is-suspended.md)),
      so a copy that signs in ahead of the owner keeps its access until a
      sign-in whose count does not advance, and the sessions it holds last until
      they expire. A copy that lands on the count just recorded, within four
      minutes of its recording, is refused as a finish sent twice is and
      suspends nothing. A backup-eligible passkey, which is what a synced one
      is, lives on several devices by design and is never refused over its
      count. Two sign-ins of one counting key that finish in reverse order
      suspend it, and a person whose only way in was a suspended key cannot sign
      in to remove it: the shop removes it through `Store()`.
    - **A credential store an installation binds itself may answer nothing at all.**
      `Credentials` exists so a shop can keep keys in LDAP or a users table it
      already has, and such a store cannot erase rows out of gobit's tables or hold
      a pending registration. Then the data-subject sweep reports `Retained` with
      the reason and self-registration is not mounted
      ([ADR 0132](adr/0132-the-contrib-identity-modules-answer-a-data-subject.md),
      ADR 0133) — which is true, and is what a controller needs to hear instead of a
      deletion that did not happen.
- **A shopper can always decline to name a customer, and that is not closable.**
  A cart without a `customer_id` belongs to a guest, and on a guest order the
  b2b spending rule is not even asked. Requiring the field would not help:
  `POST /store/v1/customers` mints a fresh guest record, bound to no company and
  therefore ruleless, with nothing but the publishable key. The correct sentence
  for the limit is not "the spending limit is not enforced" but "the limit is
  applied only to a purchase that **declares** its customer" — and since
  ADR 0057 a declaration is one the request can prove. The measurement is with
  the B2B spending rule in [`docs/commerce-flows.md`](commerce-flows.md); the
  boundary is [ADR 0008](adr/0008-musteri-kimligi-guven-siniri.md)'s, and the
  side that verifies is still the embedding application.
- **Storefront carts carry no ownership check** — the model is a capability URL:
  the cart identifier is minted from a 48-bit timestamp plus 80 bits of
  cryptographic randomness, it cannot be guessed, and knowing it carries the
  right of access. The storefront therefore has no list endpoint, because a list
  endpoint would turn knowing one identifier into reading every cart. The rules
  of the model, and what it does NOT cover, are written with the cart flows in
  [`docs/commerce-flows.md`](commerce-flows.md).
- **An admin session names its browser, not its place.** Since
  [ADR 0276](adr/0276-a-session-names-the-browser-that-opened-it.md) a session
  lists the `User-Agent` of the sign-in that opened it, as the browser gave it,
  beside when it began and ends. It keeps no address and no place: the client's
  address is resolved by the rate limiter's proxy trust, which the auth module
  does not see, and a place would need a geolocation database gobit does not
  ship.
- **The panel's privileges are per SCREEN, not per record or per field.** Since
  [ADR 0156](adr/0156-a-panel-screen-costs-a-privilege.md) every panel path is
  listed with the scope it requires, and the scope decides whether the screen
  opens at all. What it cannot do is narrow what an opened screen SHOWS: an
  operator holding `order:read` reads every order, not the ones for their
  region or channel. The read layer the screens go through knows nothing about
  principals, so there is nowhere below the route for a narrower answer to come
  from.
- **A panel screen is held to its privilege's module only as far as a walk
  reaches.** Since [ADR 0260](adr/0260-a-panel-screen-reads-only-what-its-privilege-owns.md)
  a test requests every route the panel ships as an operator holding exactly
  its privilege and holds everything it reads and writes to the module that
  declares the privilege. A plugin's screen is the plugin's own code and is
  walked by nothing, and a path a screen takes only for a particular value in a
  record is walked only as far as a record holding every asked-for field goes.
- **gobit has no agent surface.** A shopping agent is a storefront client
  ([ADR 0400](adr/0400-a-shopping-agent-is-a-storefront-client.md)): it reads
  `/openapi.json` without a key and shops through `/store/v1` with the shop's
  public publishable key. Whom it acts for is whatever your bound
  `corehttp.Identity` proves, and only on routes that name a customer; a guest
  cart asks nothing, and completing a cart asks nothing, because its id is the
  capability. A provider's charge is bounded by whatever credential your
  provider receives in `payment_data`, against an amount the client cannot set.
  The one bound across orders is a company employee's spending limit (b2b),
  which caps the customer's spend in its period whoever completes the cart.
  Nothing else bounds what an agent spends: whoever holds a customer's cart id
  spends that customer's store credit and points, a gift card code is a bearer
  credential, and an offline method places the order owing — so an expired
  delegation still completes a cart it opened. Nothing holds a category or line
  bound or bounds one delegation's spend, nothing limits one agent apart from
  its address, and no audit row names an agent. A discovery document
  (`/.well-known/…`, `/llms.txt`) is yours to serve from a module on the shop's
  origin, as `examples/storefront` serves its pages.

## Sales channel scope

- **A product with no channel assignment is visible in every channel.** The rule
  is deliberate and backward compatible (on the day it was turned on, the strict
  alternative would have emptied every existing catalog) but it has a trap:
  deleting the last channel binding does not hide the product, it opens it to
  every storefront. `status` is what hides it. The single source of the rule is
  the SQL template in
  `internal/modules/product/repository/saleschannel.go`.
- **The scope is enforced when units enter the cart, not at completion.** A line
  is scoped when it is added and again when its quantity rises
  ([ADR 0281](adr/0281-raising-a-line-asks-the-channel-again.md)), and a raise
  asks each of its add-ons its product's list and its channel, as their add did
  ([ADR 0393](adr/0393-an-add-on-is-written-under-its-line.md)); lowering it,
  removing it, a merge and completing the cart ask nothing. So a product moved to
  another channel, or an add-on taken off its product's list, after it entered a
  cart can still be bought in the quantity the cart holds, and no more of it.
  Asking at completion too was refused: a catalog edit
  would make a customer's full cart unpayable, the alternative whose
  justification is written with the sales channel rule in
  [`docs/security.md`](security.md).
- **A channel's price reaches the cart, not the catalog.** A cart opened through
  a key bound to one channel is priced and discounted by rules naming
  `sales_channel_id` ([ADR 0397](adr/0397-a-cart-is-priced-in-the-channel-it-was-opened-in.md)). The storefront's product
  reads list only rule-free prices, and the wishlist price alert quotes a cart
  naming no channel, so both show the price without the channel.
- **The cart's channel prices it; the request's channels scope it and pick the
  warehouses.** A line written through another storefront's key, or completed
  under another channel, is charged the cart's channel's prices. A price ruled
  on the channel alone ties with the region's and the cheaper wins; a channel
  price meant to beat the region's names the region too. A channel price is not
  a privilege: the key is in every browser of its storefront.
- **Some carts name no channel.** A key bound to several channels opens such
  carts, and so do an operator who names none and every cart opened before the
  upgrade. A channel an operator mistypes is recorded and never matches. A merge
  prices the moved lines in the target's channel.
- **A channel has no currency, and some orders record no channel.** An order
  records the channel its cart was opened in
  ([ADR 0410](adr/0410-an-order-records-the-channel-it-was-sold-in.md)); an
  order from a cart that named none, and every order placed before the
  upgrade, records none, and the admin list's channel filter and the trials
  read it as none.
- **No point of sale.** A counter sale is an operator's cart in the store's
  channel, completed with an offline method and paid in a second act; gobit
  records no terminal, drawer or change, prints no receipt and has no
  card-terminal provider
  ([ADR 0418](adr/0418-a-counter-sale-is-an-operators-cart.md)).

## The category tree

- **The category listing walks one level.** The storefront listing, its
  facets and the GraphQL listing take `category_tree_id` since
  [ADR 0261](adr/0261-a-storefront-category-lists-its-subcategories.md), a
  promotion rule reaches the subtree through `category_tree_ids`
  ([ADR 0259](adr/0259-a-category-rule-can-reach-the-subcategories.md)), and the
  query layer's product filter and the panel's catalog filter take it too
  ([ADR 0282](adr/0282-the-panel-lists-a-category-with-its-subcategories.md)).
  `parent_id` on the category listing walks one level, which is what a menu
  asks for.

- **A move whose ancestry is deeper than sixty-four levels is refused, even when
  it would have been legitimate.** The update that reparents a category walks up
  from the new parent to make sure the category is not being placed inside its
  own subtree, and that walk has to be bounded or an ancestry that already holds
  a ring would not terminate. Reaching the bound is treated as a refusal rather
  than as permission, because an ancestor past it would go unseen and the ring it
  closes would be written. Sixty-four is far past any catalog a person maintains,
  and the trade is stated in ADR 0085.

## A product's history

- **A product's revisions are its admin view, not everything about it.** Since
  [ADR 0221](adr/0221-a-product-keeps-its-revisions.md) every write through a
  product's own routes records its view: own fields, variants, options, images,
  tags, categories and attribute values. Its relations, sales channels, prices
  and stock links are not in it, and removing a collection, type, tag, category
  or attribute changes every product carrying it without a revision of each; the
  next revision shows it. A restore writes back the descriptive content and
  leaves the status, the schedule, the variants, the options and the images as
  they are. A product written before the migration has no past before its first
  write since.

## Tax

- **In a tax-inclusive market the discount stays a GROSS figure beside a NET
  subtotal.** The tax is taken out of the amount actually being charged, so the
  line's subtotal becomes the extracted base plus the discount that was applied
  to the gross. `Subtotal - Discount` is therefore the net taxable base and not
  "net minus net". The alternative — extracting a net discount too — needs a
  second rounding that has to cancel the first exactly, and ADR 0086 refuses to
  depend on that.
- **A tax rate trial cannot try every change a write makes** (ADR 0387). It
  amends one rate of a country's root, through the local table: a province's
  rate is refused because the cart sends no province, a rate under an external
  provider because that provider's table is not this module's, and a new
  default rate, a rate standing on another, and a region's provider or
  inclusion cannot be tried at all; each is charged on the next cart once
  written. The trial taxes the lines with today's tables, classes and
  catalog, so a figure for a product whose type changed since the sale is the
  type's today.
- **An amount moved after the sale is documented when someone asks.** A
  credit, a delivery change, and a return's or a claim's refund each become a
  document amending the order's sale document
  ([ADR 0406](adr/0406-an-amount-moved-after-the-sale-is-a-document.md)), issued
  on the order's page or `POST /admin/v1/orders/{id}/invoice/amendments`;
  nothing issues one by itself, and an act moved before the order was invoiced
  is documented after it. A row is one unit carrying its share. A registered
  buyer's own return invoice is kept only as the shop's refund, with its number
  in the metadata. The order journal moves a document's tax off `tax_payable`
  on the day the document is issued and back when it is rejected or canceled
  ([ADR 0419](adr/0419-a-documents-tax-is-on-the-books-at-the-document.md)); an
  act no document names, and a document issued on the admin route that names no
  act, correct no tax, and an order canceled under a live refund document owes
  negative tax on these books until the document is canceled.
- **An exchange's money is on no document.** Its funding and its refund are
  booked by the order journal and printed on nothing: the figure is typed by
  the operator and names neither goods nor tax, so no row of the sale could
  carry it (D247).
- **A tax charged too low is the shop's, and the order journal does not say
  so.** A placed order keeps the tax it charged on its lines, its invoice and its
  `tax_payable`, which then understates what the shop owes by the difference
  ([ADR 0394](adr/0394-a-sold-lines-price-and-tax-are-not-raised.md)). A
  restatement, sales against tax_payable with no money, is a record nothing
  writes; ADR 0387's trial counts the orders a rate change would have taxed
  differently.

## Installation and operation

- **The optional identity modules have two settings an operator can only get
  wrong once.** Both are named where they are configured, and both are here because
  the cost lands after a deploy rather than at startup.
    - **Changing `Options.RPID` abandons every passkey already registered.** A
      credential is bound to that value by the authenticator that minted it, so a
      domain move — or dropping a subdomain — leaves every existing key unusable and
      no migration can carry them over. The module records the relying party a row
      was written under and answers only for the configured one, so an abandoned row
      is not counted as a way into an account
      ([ADR 0131](adr/0131-a-passkey-belongs-to-one-relying-party.md)) — it was, and
      that made the last-way-in rule remove the only WORKING key. Everybody holding
      one still has to register again.
    - **Self-registration's default rate limit is per PROCESS.** It is kept in the
      instance's own memory, so an installation behind several instances gets that
      many times the bound until it binds a shared limiter
      ([ADR 0133](adr/0133-a-shopper-opens-their-own-account.md)). The default exists
      because one request makes the shop send mail to an address a stranger chose;
      the published description says what it is, because a limit that is a fraction
      of itself still looks like a limit.
- **The admin panel creates a product and its variants, and writes the
  EDITABLE part of the rest of the catalog.** The panel under `/admin/ui`
  ([ADR 0011](adr/0011-yonetim-paneli-dorduncu-agac.md)) names its screens in its
  package doc (`internal/adminui/doc.go`), and its forms write what exists: a
  product's title, handle, status and schedule
  ([ADR 0013](adr/0013-panel-write-surface.md),
  [ADR 0177](adr/0177-a-draft-can-be-scheduled.md),
  [ADR 0179](adr/0179-a-product-can-be-scheduled-to-leave.md)), its related products
  ([ADR 0181](adr/0181-the-panel-edits-a-products-neighbors.md)), its add-ons
  ([ADR 0232](adr/0232-the-panel-edits-a-products-add-ons.md)), a variant's bundle
  ([ADR 0236](adr/0236-the-panel-edits-a-variants-bundle.md)), a variant's BASE price per
  currency, PHYSICAL stock per location, and the signed-in person's own second
  factor and sessions ([ADR 0266](adr/0266-the-panel-enrolls-a-second-factor.md),
  [ADR 0268](adr/0268-the-panel-shows-a-persons-sessions.md)). There is no
  single-item page for inventory or for a sold line — the detail of a stock item
  IS its per-location levels on the variant page, and the context of a sold line
  IS the order the line is attached to.

  **The moderation queue is the exception to the sentence above, and to the
  paragraph's whole shape** ([ADR 0076](adr/0076-the-panels-migration-begins-with-the-review-screen.md)).
  It writes — approving and rejecting a review — and it writes through NO panel
  form and no module admin surface: it is a client of `/admin/v1`, which is what
  [ADR 0030](adr/0030-the-panel-becomes-an-admin-api-client.md) decided every
  screen becomes. It is the first, the others are still rendered on the server,
  and the order they move in is not decided. Until they do, the panel
  carries two shapes and this entry describes both.

  A draft product and its variants are created in the panel since
  [ADR 0307](adr/0307-the-panel-creates-a-product-and-its-variants.md), and a
  variant takes a base price in a new currency there, its price set created and
  linked when it has none
  ([ADR 0309](adr/0309-a-variant-takes-a-price-in-the-panel.md)), and begins to
  keep its stock, its inventory item made and linked
  ([ADR 0310](adr/0310-a-variant-begins-to-keep-its-stock-in-the-panel.md)).
  Creating anything else and deleting anything still happens over
  `/admin/v1`, with `Authorization: Bearer`: a stock location, an option, an
  image. Campaign prices and prices
  carrying a RULE are not shown in the panel and cannot be edited there either —
  the form knows only the base price. This is not a presentation preference: the
  price write is lossless and writes the prices it does not see back
  UNCHANGED, but it does not let them be edited.

  There is one more write limit. Editing one price regenerates ALL the price
  identifiers in that set, because the writer underneath does not update the
  set, it rewrites it; those identifiers are named only by pricing's own
  `price_rule` rows, so the effect stays inside the module. A save made over
  somebody else's is refused and shown again: the product form carries the
  product's version
  ([ADR 0222](adr/0222-a-product-write-names-the-version-it-read.md)), and the
  price and stock forms carry the amount and the count they were drawn with
  ([ADR 0280](adr/0280-a-price-and-a-count-name-what-they-were-drawn-with.md)).

  Every new write means a primitively typed admin surface method in the owning
  module and a form in the panel; both show up in a diff. The panel does not
  import a module, so the write path has to go through the surface the module
  publishes.

  The panel's session cookie reaches the admin API since
  [ADR 0076](adr/0076-the-panels-migration-begins-with-the-review-screen.md),
  and a state change it authenticates there must carry the panel's own Origin.
  The moderation queue below writes that way; the server-rendered forms still
  write through a module's surface.

  The catalog screen shows a price as a raw minor-unit integer, and says so,
  when the currency's number of decimal places is NOT KNOWN. The scale is read
  from the region record; in an installation with no region defined at all one
  sees `19990 TRY (minor units)`. Assuming a fixed 100 would show the WRONG
  amount for currencies with 0 and 3 digits, such as JPY and KWD.
- **A cart's metadata reaches no rule.** A new price rule whose attribute
  begins with `cart.` is refused with 422 `pricing_rule_attribute_reserved`
  ([ADR 0403](adr/0403-a-carts-metadata-chooses-no-price.md)), and a new
  promotion rule with 422 `promotion_rule_attribute_reserved`
  ([ADR 0407](adr/0407-a-carts-metadata-reaches-no-rule.md)). One written
  before stays and matches no cart. The panel's price forms and the import
  carry a price rule unchanged; the variant page and the promotion page mark
  both kinds; the admin calculator and the admin computation still match them
  when asked with the attribute. To retire a price rule, remove its *price*; to
  retire a promotion rule, first put the condition on what the server decides
  — a publishable key bound to one sales channel and a `sales_channel_id`
  rule, a customer group, or a code — then remove it. Deleting either rule
  alone makes its price or promotion apply wherever its other rules hold. A
  fact only the embedder's server knows has no rule to reach.
- **Search AND the e-mail guards depend on the database cluster's CTYPE setting,
  and that setting is fixed at initdb time.** Three things leave case folding to
  PostgreSQL: the storefront's own `?q=` filter (`title ILIKE`), the `search-pg`
  plugin's index (`to_tsvector`), and the `CHECK (email = lower(email))`
  constraint that `auth`, `customer` and `b2b` each put on their e-mail column. A
  cluster created with `--locale=C` folds ASCII only, so a search for a lowercase
  word carrying a non-ASCII letter does NOT FIND the product whose title carries
  the uppercase form of that letter — with no error, silently.

  The third one fails differently and it is worth stating on its own: the
  constraint keeps accepting rows, it just stops refusing the wrong ones. On such
  a cluster it still rejects an unfolded ASCII address and accepts an unfolded
  non-ASCII one, so the last defense behind gobit's own folding is absent for
  exactly the addresses that need it. **This is defense in depth, not the
  mechanism** — every write path in those three modules folds in Go first
  (ADR 0038), so an installation is not storing unfolded addresses because of it.
  What is lost is the backstop against a direct SQL write. The startup probe
  reports this path as `case_lower`. (The letter pair that shows it is pinned by the probe in
  `core/db/casefold.go` and quoted in
  [ADR 0015](adr/0015-postgresql-cluster-contract.md). It is not repeated here
  because ADR 0012 forbids a Turkish letter in a translated file, and an
  ASCII substitute would demonstrate nothing — an ASCII pair folds on every
  cluster.)

  `deploy/docker-compose.yml` now uses `--locale=C.UTF-8`, and the application
  probes the state at startup and warns when it is broken. But **an existing
  data directory keeps its old locale**: fixing an installation created with
  `--locale=C` takes a dump/restore. If you bring your own Postgres, the
  cluster's CTYPE has to be a UTF-8 aware locale; an ICU provider is NOT
  ENOUGH — it fixes `ILIKE` and leaves the search index broken.
- **The database has to start transactions at READ COMMITTED, and the process
  refuses one that does not.** Every lock in gobit that guards a total — a
  balance, a spending limit, a stock level, a budget — is taken first and the
  total read after, and that read is fresh only at READ COMMITTED. A database or
  role whose `default_transaction_isolation` is REPEATABLE READ or SERIALIZABLE
  therefore stops the process at startup, and a default changed while it runs
  refuses the next connection the pool opens, with the level named and the
  statement that sets it back
  ([ADR 0166](adr/0166-a-connection-at-another-isolation-level-is-refused.md)).
  A managed provider whose default you cannot change leaves only the role or the
  connection's `options` to set it on. What it prevents was measured: at
  REPEATABLE READ a spending limit that covers one order let eight through, and
  every call answered with success.
- **Search scores EVERY matching document; the cost grows linearly with the
  catalog.** A GIN index cannot satisfy the `ORDER BY`, so returning a single
  page reads and scores ALL of the matching rows. Measured (52,000-document
  index, a word occurring throughout the catalog, LIMIT 20): the ranking used to
  take **663 ms** and today takes **24 ms**, and **24 ms** of that is the match
  scan itself — that is, there is nothing left to win from the ranking, the
  remaining cost is the scan, and on a 500,000-product catalog the same word
  rises to half a second. Going below that means the index satisfying the
  ordering as well (RUM); adding a mandatory EXTENSION is
  [ADR 0015](adr/0015-postgresql-cluster-contract.md)'s dated decision and
  cannot be taken as a one-line speedup.

  A second limit follows it: **an exclusion carries no relevance.** Scoring is
  done with the positive part of the query, so `gomlek -mavi` is still ordered
  by relevance; but a query consisting ONLY of an exclusion (`-mavi`) leaves no
  positive signal to rank by, and the results come back in indexing order.
- **The storefront listing's TOTAL COUNT gets more expensive as the catalog
  grows.** The `count` field of the
  `GET /store/v1/sales-channels/{sales_channel_id}/products` response has to
  count the whole of the set the sales channel filter is applied to; the page
  size does not change that. Measured (52,000 products, 52,000 channel
  assignments, local Postgres): the plain unfiltered count **2 ms**, the
  channel-filtered count **64 ms**, all the remaining SQL of the same request
  **1 ms**. So on a large catalog almost the whole of a storefront request is
  the counter.

  This is not a defect but the price of the pagination contract: if a total is
  asked for **and can be counted**, a total is counted. There is now one place
  where it cannot be: beside `in_stock` or a price bound (ADR 0040, ADR 0041)
  the matches are chosen after the rows are read, so an explicitly requested
  total is REFUSED with 422 `product_count_unavailable` rather than counted or
  quietly dropped. Every request that could be counted before still is. The list
  query itself does not carry this cost (measured: 0.14 ms), so the only thing
  that gets slower is `count`.

  The counter CANNOT BE MADE CHEAPER, but it can now be NOT ASKED FOR:
  `?with_count=false` (in GraphQL, not selecting the `count` field) does not run
  the counting query at all and the envelope carries no `count` field. The
  default did not change. Why it cannot be made cheaper was measured: the
  channel filter runs one subquery per product and that subquery is ALREADY
  index-only (`EXPLAIN`: `Heap Fetches: 0`), so the set to be walked cannot be
  shrunk, only left unwalked. The panel's product list never pays this price —
  it deliberately pages without counting
  (`TestProductListPagesWithoutCounting`).
- **Building a cart writes rows in the SQUARE of the line count** — but it no
  longer RUNS statements in the square. Every request that adds a line rewrites
  the amount of all the cart's lines, so building a 100-line cart still writes
  5,050 line amounts; what changed is that this is done with 100 UPDATEs instead
  of 5,050. The time spent under the cart's lock thereby became almost
  independent of the line count (measured, 100 lines: the write phase 8.0 ms →
  0.55 ms).

  The remaining limit is in two places: the number of ROWS written is still
  quadratic, and the real time under the lock is now the commit's WAL flush
  (measured on a durable cluster, 6.2 ms independently of the line count) —
  there is no way to shorten that at this layer.

  Today's protection is a CEILING: a cart carries at most 100 distinct lines and
  opening one beyond that is refused with `cart_workflow_line_limit_reached`, on
  an add and on a merge alike. The cart module counts the lines under the cart's
  lock where it decides a line is new ([ADR 0227](adr/0227-the-cart-counts-the-lines-it-opens.md)),
  so it is a hard upper bound; a cart opened before the ceiling with more lines
  keeps them and is still priced.
- **An interrupted payment leaves reserved stock waiting for MANUAL
  intervention.** The cart flow runs synchronously inside the HTTP request; if
  the process dies in the middle (a deploy, an OOM, a pod eviction) the
  compensation NEVER runs and the stock reserved up to that moment hangs in
  `inventory_reservations`. The `SHUTDOWN_TIMEOUT` default is **15 seconds** and
  the saga's budget is **2 minutes** — so an ordinary deploy can produce this.
  The application WARNS about that gap at startup.

  It is no longer silent: when an execution's LEASE expires the next attempt
  closes it and, if work had been done, writes `compensation_failed` and logs at
  ERROR saying manual intervention is needed (so it reaches the collector when
  error reporting is on). Which reservation is left hanging stands in the
  `output` field of the step records.

  It can now also be LISTED: `gobit stuck` prints the half-done executions and,
  for each, which of its steps is still holding what. It covers both classes at
  once, because the status query alone is not enough: if the process died in the
  middle of the saga and the customer never came back, the record does not even
  GET a `compensation_failed` — it stays `running` forever, and the command
  finds that class as "lease expired and still holding a step".

  **The compensation can now be run FROM THE RECORDS**
  ([ADR 0017](adr/0017-recovering-abandoned-sagas-from-the-record.md)): a caller
  returning with the same key finds the abandoned execution, the shared state is
  rebuilt from the steps' own durable outputs and the chain runs; the record
  becomes `failed` and releases its key, so the stock is released and the
  customer can pay for their cart again.

  **At one point it deliberately STOPS, and that point is the payment.** The
  engine writes a step's record after Invoke returns, so a process dying inside
  the collection leaves no trace at all; if recovery counted it as "never ran",
  a customer whose card had been charged would have their stock released, their
  key freed, and would be charged a SECOND TIME. That is why a collection step
  with no record stops the recovery, and the decision is left to manual
  intervention.

  **Recovery can now also be TRIGGERED:**
  `gobit recover <execution-id> -confirm <execution-id>` runs an execution's
  compensation chain. The engine's own recovery happens by coincidence — a
  caller returning with the same key triggers it — and that covers the customer
  who retries, nobody else. An abandoned cart has no returning caller;
  `gobit stuck` lists it and there would be NOBODY TO RELEASE it.

  The command carries the same gate as the other irreversible command
  (`migrate down`): without `-confirm` repeating the identifier, nothing runs at
  all. The engine's own refusals are a second gate and the command CANNOT
  OVERRIDE them — a live lease, a record in a terminal state, and a collection
  step with no record stop the run whatever is typed.

  A scheduled sweeper is still deliberately ABSENT: recovery runs work that has
  side effects, and handing that to an unwatched background job is the "decide
  silently" class this repository refuses. Here a HUMAN makes the decision and
  names the execution.

  **Recovery is EXCLUSIVE.** An abandoned record is in nobody's ownership, so
  every caller arriving with the same key finds it; without a claim they would
  all run the compensation chain (measured with four concurrent callers: the
  chain ran FOUR times). The engine now CLAIMS the record BEFORE recovering: a
  single conditional UPDATE, holding only while the record is still `running`
  AND `updated_at` is the value the decision was based on. Once the winner
  stamps it the others are eliminated, and the lease is refreshed throughout the
  recovery. Measurement: the same four callers, ONE compensation.

  The capability is OPTIONAL (`workflow.ClaimingStore`); no method was added to
  `Store` so that the contract of anyone who wrote the store elsewhere does not
  break. The price of that is that a wrapper EMBEDDING `Store` silently hides
  the capability — an embedded interface carries only its own methods. The
  decision is in
  [ADR 0017](adr/0017-recovering-abandoned-sagas-from-the-record.md).
- **A workflow step cannot be watched as it runs, and its timing is read by
  SQL.** The saga engine takes no hook, observer or step callback. Each step's
  name, status, attempts, start and end are written to
  `workflow_execution_steps` as it ends; `gobit stuck` prints a held saga's
  steps by name and status, and how long a step took or how often it was tried
  is a query against that table. A compensated step's row is the exception: its
  `ended_at` is the compensation's end and its `attempts` counts Invoke alone,
  while compensation retries are in the log. A failed step is logged at ERROR,
  and the error collector receives its workflow and step. A saga that wants a
  span opens it inside its steps. The decision is in
  [ADR 0385](adr/0385-a-workflow-step-has-no-observer.md).
- **Error reporting is a SIGN, not a copy of the event.** The `error-sentry` and
  `error-otlp` plugins ([ADR 0014](adr/0014-error-reporting.md)) send the
  collector the failure code, the safe message and the `request_id`; everything
  else stays in the log. This is deliberate — the reporter never sees the error
  itself, so it cannot leak it — but the consequence is this: whoever reads a
  report must have access to the log as well.

  Three concrete limits: the report an API failure produces carries no METHOD
  and no PATH (`corehttp.WriteError` logs the error, the code, the status and
  the `request_id`; the access log line that does carry both is skipped on
  purpose, because it would report the same failure a second time) — a failure
  that reaches `slog.ErrorContext` on its own is not bound by that, and several
  do not: the panic recoverer's line and the audit middleware's two failure
  lines log method and path, while the panel's two failure writers
  (`internal/adminui/failure.go`) and the callback guard's error lines log the
  path, and the default allow list lets both keys through as the request's
  SHAPE; the "safe message" rests on a godoc promise and no audit MECHANICALLY
  verifies that a caller did not write an email address into it; and the
  default allow list holds no business identifier at all, so fields such as
  `user_id` enter a report only if the installation adds them to the list.
- **There is no multi-tenancy.** One tenant = one installation = one database =
  one process; several INSTANCES are not several TENANTS, because instances
  share the same database and the same catalog. The detail is with the
  single-instance discussion in [`docs/security.md`](security.md), the decision
  in [ADR 0009](adr/0009-cok-kiracililik-kurulum-siniri.md).
- **Migration rollback is for ONE owner and does not KNOW the order.** The
  surface now exists (`gobit migrate status`,
  `gobit migrate down <owner> -confirm <owner>`) and the forward direction stays
  automatic at startup. But the command rolls back one owner, not several: the
  operator calls the modules that have to be rolled back together one after
  another, and the command does not say which order is the right one. Because
  there are no cross-module foreign keys, this is not a constraint today.

  The second limit is a WAIT, and it cannot be interrupted: golang-migrate takes
  the advisory lock with `context.Background()`, so while somebody else holds
  the lock neither a deadline nor Ctrl-C ends the waiting (measured: a version
  read whose context expired in 5 s had still not returned 15 s later).
  `migrate status` takes the lock while reading versions too, so a command run
  in the middle of an ongoing deploy can wait silently.
- **The location policy expresses region SCOPE and PREFERENCE order, and nothing
  else.** Stock distribution ("put the location with the most stock first"),
  cost, and an order-level decision ("ship all lines from a single location")
  CANNOT BE EXPRESSED; why each of them cannot is written in
  [ADR 0010](adr/0010-depo-secim-politikasi.md). Priority is per **location**,
  so "A first for R1, B first for R2" cannot be written either — the only thing
  writable per region is exclusion.
- **A wrong region binding CLOSES the store until an operator repairs it.**
  Binding a region identifier that does not exist (or deleting a region and
  reopening it under the same name — the new record gets a new identifier)
  eliminates that location for every cart; in a single-location installation the
  result is that every completion is refused although the catalog is full. The
  cart is NOT consumed: the refusal is raised in the flow's first step and
  BEFORE any stock is reserved, so there is nothing to compensate — no earlier
  step exists and the failing step took no reservation to release — and the
  execution is written `failed`, a transition that RELEASES the idempotency key
  (see `workflow.StatusFailed`), so the same cart can be paid for the moment the
  binding is corrected. The failure is visible, but the visibility has a limit:
  only the CODE reaches the storefront body
  (`fulfillment_no_serviceable_location`); the dump that names what the
  candidates are actually bound to is in the server log and in the
  `workflow_executions` record. The way back is a single admin write — but it
  depends on the operator being able to reach that record.
- **A region binding is a CONSTRAINT, not a PREFERENCE.** An operator who binds
  two locations to separate regions has accepted that the order FALLS when the
  first location's stock runs out in a race. "A first, B when it runs out" is
  written with PRIORITY, not with a region binding.
- **Deleting the last region binding does not hide the location, it opens it to
  ALL regions** — the same as the sales channel rule, with one difference: there
  the price is visibility, here it is a dropped order.
- **A backordered line is filled whole, at one warehouse, from the warehouses
  ranked when it was ordered.** Units that arrive go to the oldest waiting claim
  they complete ([ADR 0392](adr/0392-a-backordered-line-waits-for-its-units.md)).
  A claim waits until the units sellable at one of its warehouses reach it;
  units short of it stay on sale meanwhile, other shoppers can buy them, and
  later, smaller claims are filled first. A warehouse opened or bound to the
  channel after the order does not fill its claim, a claim the checkout could not
  rank is filled from nowhere, and a line whose claim the checkout failed to
  record and an order placed before the upgrade have none. A claim the checkout
  recorded but could not settle (a warning on its answer) still owes the units
  written off before it. A filled claim opens no parcel, no date is promised,
  and nothing caps how many units are sold ahead of a receipt
  ([ADR 0425](adr/0425-selling-ahead-of-stock-is-a-backordered-line.md)).
- **A restock date is an estimate.** `restock_expected_at` is the first moment
  expected supplier receipts leave units for sale at the request's warehouses
  after waiting backorders take theirs
  ([ADR 0399](adr/0399-stock-on-its-way-has-a-date-the-storefront-shows.md)).
  The checkout does not read it, no order line carries it, a bundle shows none,
  a receipt whose moment has passed drops out until someone receives, cancels
  or re-records it, and a changed date is a cancel and a new receipt. A
  receipt names no supplier, cost or purchase order: `reference` carries the
  embedder's number.
- **A delivery estimate is the storefront's.** A shipping option carries how
  many business days its delivery takes, the least and the most, and a variant
  its restock moment
  ([ADR 0421](adr/0421-a-shipping-option-says-how-many-days-it-takes.md));
  gobit turns no day count into a date, since it holds no shop time zone or
  calendar, ranks no warehouse by time, and records no promise on the order. A
  warehouse farther from a region than an option assumes delivers later than
  the option says.
- **A reduction's reference price is known only from the day the price history
  began.** Since [ADR 0167](adr/0167-a-price-keeps-its-history.md) every price
  write keeps a snapshot, and a storefront sale price carries the lowest price
  of the thirty days before its reduction began — but the history starts at the
  upgrade, and nothing before it was kept (ADR 0047 deleted it). For thirty
  days after upgrading, and for any sale already running then, the storefront
  carries no `lowest_prior_amount`; it is absent rather than approximated, and
  an installation that has to announce a reduction in that time needs the
  number from somewhere else. The reference is also the storefront's price only
  — quantity one, no customer group or region rule — and it is computed from
  every snapshot of the set, so a set edited thousands of times makes its
  storefront read heavier ([measurement 0167](measurements/0167-what-a-price-was.md)).
- **An order line's price origin is known only from the upgrade, and it does
  not name a promotion.** Since [ADR 0168](adr/0168-an-order-line-remembers-the-price-it-was-charged.md)
  a line keeps the price row it was charged and that row's list, but a line
  sold before the upgrade has none, and neither does one placed by a saga
  recovered from a plan written before it; the API leaves `price_origin` out
  rather than guess. Which promotion reduced a line is not kept at all: the
  promotion engine reports discounts per line and per promotion, never one
  promotion's share of one line.
- **An order read at a moment cannot say everything it was.** Since
  [ADR 0171](adr/0171-an-order-can-be-read-as-it-stood.md) an order can be read
  as it stood at a past moment, since
  [ADR 0411](adr/0411-an-order-read-at-a-moment-says-where-it-was-going.md)
  with the shipping address and the deliveries in force then, and on the
  timeline (ADR 0170) the status on an opening entry is still today's. Three things have no past: the contact and
  addresses before an erasure (the reading says `erased_since`), a claim
  evidence that was removed, and whether an order archived before archiving
  was dated was completed or archived at a moment after its completion. A cart
  keeps no history at all. Nothing records what a dispute would ask for beyond
  the order either, such as the buyer's IP, the carrier's checkpoints or what
  the customer was sent
  ([measurement 0170](measurements/0170-what-the-timeline-left-out.md)), and
  gobit assembles no dispute file
  ([ADR 0427](adr/0427-a-cart-keeps-no-past-and-gobit-files-no-dispute.md)).
- **A read at the edge of "now" can miss a row that commits a moment later.**
  A row's moment is taken when it is written and its transaction commits after
  it. [ADR 0241](adr/0241-a-row-written-under-a-lock-is-stamped-when-written.md)
  and [ADR 0242](adr/0242-a-moment-the-process-reads-is-read-after-the-lock.md)
  took the moments that order state after the lock, which narrowed the gap to
  the rest of the write and did not close it
  ([measurement 0241](measurements/0241-the-change-that-waited.md)). A reader
  paging the stock ledger, the invoices or the orders newest first, or
  exporting a journal window that ends at the present, can pass a moment whose
  row is not committed yet, and the next page or window starts after it. The
  row is there once it commits: read a window that ended a few seconds ago, or
  read it again.
- **A 304 from the catalog saves the wire, not the server.** Since
  [ADR 0391](adr/0391-a-catalog-read-answers-a-revalidation.md) the catalog reads
  tag their body and answer a matching `If-None-Match` with 304, but the tag is a
  hash of the bytes, so the read, the enrichment and the encoding run before the
  comparison; a listing's count runs too unless the client sends
  `with_count=false`. A stamp that would let the read stop early was refused: the
  body moves with the clock and with writes in three modules that publish no
  version.
- **GraphQL reads the catalog only.** `POST /store/v1/graphql` has no mutation
  and no cart, checkout, order or customer type
  ([ADR 0414](adr/0414-graphql-reads-the-catalog-and-nothing-else.md)); a client
  that reads the catalog there reads everything else over REST.

## The limit of the invariants

- **The cross-module JSON SCHEMA is not checked at compile time.** A narrow
  interface plus resolution by name from the container is
  [ADR 0001](adr/0001-modul-arasi-iletisim.md)'s accepted price: a FIELD NAME
  drifting apart leaves both packages' unit tests green, and the two ends meet
  over a real container in e2e. The composite data crosses as `json.RawMessage`
  and nothing but a test that runs both sides can compare the two schemas.

  The SIGNATURE is no longer part of this limit and the distinction is the
  point. Since [ADR 0136](adr/0136-the-compiler-checks-every-interop-pair.md)
  `internal/arch/interop_pins_test.go` assigns every container-resolved producer
  to the interface its consumer declares, so a method that changes, moves or
  disappears fails the BUILD. The sentence that used to stand here — that the
  compiler never sees the two sides together — is what kept that file from being
  written, and while it stood two shipped storefront coupon endpoints answered
  500 to the first customer who typed a code (D73).
- **A rule set is a conjunction over one operator vocabulary, not an
  expression language.** A price, a shipping option and a promotion offer only
  when every rule holds; `in` gives a choice within one attribute, and there is
  no OR across attributes and no grouping
  ([ADR 0396](adr/0396-a-rule-condition-is-read-by-one-evaluator.md)). The
  three read a rule through `internal/core/condition`; `any_in` is promotion's
  alone (ADR 0144; pricing refuses it, ADR 0327), a tax rate rule names a
  reference by equality and the most specific wins (ADR 0094, ADR 0101), a
  customer segment is evaluated by its own flow (ADR 0217), and a webhook
  filter is equality (ADR 0218).
  Pricing caps no value count and only fulfillment trims a value. A shipping
  rule written before ADR 0396 with a non-integer threshold still hides its
  option.
- **`TestEveryWorkflowIsSetUpInTheCompositionRoot` is a SYNTACTIC proxy.** It
  asks the question "can a wrong configuration stop startup" as "does the path
  to setup go through a `go` expression"; when the `go` is hidden behind a
  one-line indirection the audit passes while the property does not hold
  (measured in a real process). The shapes it catches are the ones written by
  accident, the shape it misses is the one that would have to be written
  deliberately — but the sentence "startup fails closed" does NOT FOLLOW from
  this invariant. The scope is written in
  `internal/arch/registration_test.go`.
- **The constant-time gate sees the files that make a secret, not the ones that
  only hold one.** Since
  [ADR 0257](adr/0257-a-secret-is-compared-in-constant-time.md) a file importing
  a MAC, digest, derived-key or randomness package compares no two values with
  `==` or `bytes.Equal` unless the comparison is listed as holding no secret. A
  secret read from storage and compared in a file that imports none of those
  packages is not seen, and timing itself is still observed by no test.

- **An exchange's goods and its money are related only by a human.**
  `order_exchanges` carries no items and its `difference_due` is a figure the
  operator types; the dispatch guard compares the payment collection against that
  figure and nothing else. Since ADR 0145 a replacement can send a variant the
  order never sold, so an operator can promise a jacket against a shirt's
  difference and nothing will object. Pricing the item at settlement needs a
  pricing read and a decision about which price applies to a replacement, and
  neither has been made. An exchange that sends the line's own variant at a
  difference is a sold line's price raised, which ADR 0394 forbids and nothing
  refuses, and it is printed on no document.

- **A lost authenticator is answered only at the machine.** A person enrolls,
  replaces and removes their own factor in the panel
  ([ADR 0266](adr/0266-the-panel-enrolls-a-second-factor.md)) or through the
  API, and replacing or removing a proven one takes its current code. Recovery
  codes are absent: the way back from a lost phone is
  `gobit mfa-reset <email> -confirm <email>`, which needs shell access and
  answers the case in one act, so a second readable secret would be a second
  thing to store and re-issue for nothing.

- **An operator finishes a telephone order only with an offline method.** Since
  [ADR 0286](adr/0286-an-operator-completes-a-telephone-order.md) the cart's
  admin surface writes the addresses and the shipping method and completes the
  cart, and the checkout refuses that completion unless its provider is an
  offline method ([ADR 0284](adr/0284-an-offline-method-is-paid-when-the-shop-says-so.md)).
  The panel takes the whole order
  ([ADR 0290](adr/0290-the-panel-opens-a-telephone-order.md),
  [ADR 0291](adr/0291-the-panel-completes-a-telephone-order.md)), billing address
  included ([ADR 0303](adr/0303-a-telephone-order-takes-a-billing-address.md)),
  and corrects the operator's own cart
  ([ADR 0300](adr/0300-an-operator-corrects-their-own-cart.md)); an operator
  writes only to a cart an operator opened
  ([ADR 0299](adr/0299-an-operators-writes-reach-only-an-operators-cart.md)).
  What the page offers by name depends on the operator's other privileges, and
  each falls back to a typed id: the variant by its product's title under
  `product:read` ([ADR 0293](adr/0293-a-telephone-order-finds-a-variant-by-its-title.md)),
  the caller by e-mail and their default address under `customer:read`
  ([ADR 0297](adr/0297-a-telephone-order-finds-the-caller-by-email.md),
  [ADR 0304](adr/0304-a-customers-cart-starts-from-their-default-address.md)),
  the channel under `auth:read`
  ([ADR 0305](adr/0305-a-telephone-order-chooses-its-channel-by-name.md)) and the
  offline method under `payment:read`
  ([ADR 0306](adr/0306-a-telephone-order-chooses-its-offline-method-from-a-list.md));
  the shipping option is always chosen from the options the cart can take
  ([ADR 0292](adr/0292-a-cart-lists-the-shipping-options-it-can-take.md)).
  A card payment taken over the telephone is not offered, because the operator
  would hold the card; a caller who pays by card is sent the cart's link.

- **An unpaid offline order expires only where its method was given a wait.**
  An order placed with an offline method owes its total and holds its stock.
  Since [ADR 0289](adr/0289-an-unpaid-offline-order-expires-by-its-methods-wait.md)
  `PAYMENT_OFFLINE_WAIT_DAYS` gives a method days, and a job cancels the order
  whose session of that method is still authorized past them; a method left
  out, which is every method by default and cash on delivery by design, keeps
  its order until the shop cancels it. An order whose payment captured
  something beside the transfer is never canceled by the job. The cancel gives
  the stock back ([ADR 0285](adr/0285-the-shops-cancel-gives-the-stock-back.md))
  and closes the payment session once its event is handled
  ([ADR 0288](adr/0288-a-canceled-order-holds-no-payment.md)). The order list
  shows the orders still awaiting their payment
  ([ADR 0294](adr/0294-the-order-list-filters-the-orders-awaiting-their-payment.md)).

- **Store credit names its order, not the finer cause.** Since
  [ADR 0274](adr/0274-a-store-credit-names-the-order-it-compensates.md) an
  issue can name the order it compensates and the history is read for one
  order; the order is not looked up, and "this is for return R-19" is still a
  convention in `reference`. Credit can expire since
  [ADR 0258](adr/0258-store-credit-can-expire.md).

- **An installation that trusts an unproven customer claim has neither store
  credit nor loyalty points as a tender.** With
  `STOREFRONT_TRUST_UNVERIFIED_CUSTOMER_CLAIM` on, the cart's customer is not
  proven, so neither person-bound provider is registered
  ([ADR 0152](adr/0152-a-shop-can-hold-money-for-a-customer.md),
  [ADR 0165](adr/0165-a-customer-can-pay-with-their-points.md)): the two
  tenders disappear rather than becoming a way to spend somebody else's balance.
  They are one option, `PersonBoundTenders`, because the argument is about the
  person and not about credit or points. A shop that needs both has no answer
  here.

- **At the storefront the tenders that pay first come in one order, and an
  order has one gift card.** Since
  [ADR 0269](adr/0269-a-balance-pays-part-of-an-order.md) a completion names
  the customer's store credit and points in `pay_first_with` beside a
  `gift_card_code`, and each holds what it has of what is still unpaid before
  `payment_provider_id` pays the rest. The card always pays before the
  balances, the balances pay in the order named, and `payment_provider_id`
  cannot be `gift_card` beside a code, so two cards cannot share an order. The
  payment module itself splits a collection across any sessions an operator
  opens (`POST /admin/v1/payment-collections/{id}/payment-sessions` takes an
  amount, and a balance's session holds part when its data says
  `partial: true`)
  ([ADR 0209](adr/0209-a-gift-card-pays-first-and-a-provider-the-rest.md)).

- **A balance of points can be negative.** A refund reverses the points the
  refunded capture earned, and the customer may have spent them already; the
  refund is not refused for that — money goes back before points do — so the
  ledger records a debt of points, the next earn fills the hole first, and the
  `loyalty_points` tender declines against it until it is filled. The balance
  endpoint reports it as a negative number, and that is the decision rather
  than a defect ([ADR 0165](adr/0165-a-customer-can-pay-with-their-points.md)).
  Store credit has no analogue: an issue is never reversed, and its negative
  rows, a hold and an expiry (ADR 0258), are written under the lock that read
  the balance.

- **A missed cart event leaves the funnel one short, forever.** Since
  [ADR 0153](adr/0153-a-shop-can-see-where-its-carts-go.md) the cart publishes
  what happened to it and `plugins/analytics` counts it, but the bus calls a
  failing handler only twice more, within a second and a quarter (ADR 0240),
  and there is no repair path: the outbox relay covers the ordinary loss and
  nothing covers a longer outage. A reconciliation job that
  re-derived the counts from the modules' own tables was refused — it would be a
  second history of the same facts, over rows a shop is allowed to delete.

- **The funnel starts when the plugin is installed, and knows only days and
  regions.** It says nothing about carts opened before `analytics` was named in
  `PLUGINS`; a cart opened on one day and completed on the next is counted on two
  different days, so a same-day ratio is an approximation; and there is no
  per-customer and no storefront view, because both would need an identity in a
  payload that deliberately carries none.

- **`plugins/webhookout` forwards every published topic, `cart.created`
  included.** Its own gate fails the build in both directions. A receiver is
  owed only the topics it registered for, only the events its filters match, and
  only the fields it lists (ADR 0218), so a receiver of `cart.created` is still
  owed one delivery per opened cart in the regions it names. Its rate is per
  minute and nothing finer: `max_per_minute` caps what one pass sends it, and
  the pass runs once a minute
  ([ADR 0275](adr/0275-a-webhook-receiver-sets-its-rate.md)).

- **No analytics contract: an outside analytics product hears gobit through the
  bus.** `core/provider` holds no analytics slot
  ([ADR 0390](adr/0390-an-analytics-product-hears-gobit-through-the-bus.md)). An
  installation registers a `webhook-out` receiver and runs whatever turns its
  signed envelope into the product's request; the delivery id is the key that
  drops a repeat. A plugin that subscribes and speaks the product's protocol
  itself receives every outbox event twice and a failing handler is called only
  three times in about a second, so without a record keyed on the event id and
  a send that outlives an outage the product counts twice and loses an outage's
  events. Such a plugin also receives `order.placed`'s `customer_id`, which
  `webhook-out` withholds. A page's events join the server's on the cart id and
  on the customer, and the cart id only where the page can read it: a storefront
  that keeps it in an HttpOnly cookie has to send the event from its server or
  hand the page the id. An anonymous visit before a cart has no id the server
  knows.

- **No experiment runs inside gobit.** The visitor key, the assignment and its
  consent are the embedder's
  ([ADR 0402](adr/0402-an-experiment-runs-outside-gobit.md)). On the bus a
  guest's conversion joins on `cart.completed`'s cart id, but its revenue does
  not: `order.placed` carries no cart id. The completion response and the order
  read carry both. A price that differs by customer group is the merchant's
  price: gobit does not guard it as an experiment or tell the shopper it is
  personalized.

- **An order's archiving and its after-sales transitions publish nothing.**
  Placing, canceling and completing an order publish `order.placed`,
  `order.canceled` and `order.completed`; archiving one and moving a return, a
  claim or an exchange do not, so neither a plugin nor a webhook receiver can
  react to them, and there is no generic transition hook
  ([ADR 0386](adr/0386-a-completed-order-says-so-on-the-bus.md)).

- **An order confirmation is pushed at most once, and not late.** The
  `web-push` plugin records an order's event before it pushes, so a process
  that stops between the two, or a push service that refuses a device, leaves
  that push unsent; an order placed more than four hours before its event
  arrives, a redriven one included, is not pushed. The exception is an order
  whose `placed_at` cannot be read: a delivery of it that arrives after the
  record has forgotten its id, a day later, pushes it again
  ([ADR 0389](adr/0389-an-order-confirmation-is-pushed-at-most-once.md)).

- **No lane proves a generated project works at the version it PINS.** Every
  out-of-tree proof rewrites the generated `go.mod` to point at the checkout, so
  the lane compiles the template against the tip of the tree. It cannot tell
  "works at the pinned version" from "works at HEAD", and the two differ for any
  binary built after the tag it would pin.

- **A generated project is GUEST-ONLY.** The signed-in-customer adapter the
  starter example carries is not in the template, and the module it needs has no
  released tag at all, so the storefront routes that name a customer refuse
  until the embedder binds an identity. It does call itself by its own name
  ([ADR 0254](adr/0254-a-program-calls-itself-by-its-own-name.md)).

- **A plugin's admin screen is not sandboxed from the panel.** Since
  [ADR 0155](adr/0155-a-plugin-can-put-a-screen-in-the-panel.md) a plugin can put
  a screen in the panel, and its script runs on the panel's own origin with the
  operator's session cookie — which is what makes it work and also what makes
  installing a plugin a decision about trust. The content policy bounds where
  code may come FROM, not what it may do once it is there.

- **A registered screen gets the frame and a script, and nothing else.** There
  is no way to add a column or a section to an existing screen; ADR 0030's
  refusal of server-renderer extension points is why there are no template
  slots, and a plugin's view of a customer or an order is a screen of its own
  ([ADR 0416](adr/0416-a-plugin-adds-a-screen-not-a-section.md)). Since [ADR 0157](adr/0157-the-panels-address-belongs-to-the-panel.md) a
  plugin cannot serve a page of its own at the panel's address either — the
  registry refuses the binding at startup, because the rules that live there (a
  content policy, an origin check, a privilege per screen) are applied by the
  panel and a route beside it would get only the first two. The other half of this bullet — "and no page-level scope" — was true
  until [ADR 0156](adr/0156-a-panel-screen-costs-a-privilege.md) and is gone: a
  registered screen now declares the privilege it requires and a registration
  without one is refused at startup.

- **The panel's after-sales forms are the common case, not all of the API.**
  The order page lists the order's returns, claims, exchanges and replacements
  ([ADR 0270](adr/0270-an-orders-after-sales-are-read-like-its-lines.md)), at
  most 25 of each kind, takes every act the API takes on one
  ([ADR 0271](adr/0271-the-panel-acts-on-an-orders-after-sales.md)) and opens
  each kind ([ADR 0272](adr/0272-the-panel-opens-an-orders-after-sales.md)),
  a return with each line's part of the refund and a replacement with one
  variant the order never sold
  ([ADR 0279](adr/0279-the-panel-opens-a-return-and-a-replacement-with-their-detail.md)).
  A replacement that sends more than one such variant is still an `/admin/v1`
  call.

- **The order page corrects an order as the API does, and only an exchange can
  raise a sold line's price.** Any order but a canceled one takes a credit, and
  a pending one has its delivery put on a quoted option and its shipping address
  corrected
  ([ADR 0388](adr/0388-the-order-page-credits-changes-a-delivery-and-corrects-the-address.md)).
  A price or tax charged too low is not raised after the sale
  ([ADR 0394](adr/0394-a-sold-lines-price-and-tax-are-not-raised.md)): the buyer
  buys the line again before it is written off or returned, or the shop bears
  it. An exchange that sends the line's own variant at a difference raises it
  anyway, and nothing refuses that (above). The billing address is not
  corrected before an invoice is issued
  ([ADR 0195](adr/0195-a-shipping-address-can-be-corrected.md)), and opening a
  parcel on an option no delivery stands on stays an `/admin/v1` call
  ([ADR 0332](adr/0332-the-panel-opens-a-parcel-on-the-delivery-chosen.md)).
  A parcel opened before
  [ADR 0409](adr/0409-an-orders-parcel-holds-the-units-it-ships.md) holds
  nothing the dispatch bound counts, and an addition riding in its parent's
  parcel ([ADR 0197](adr/0197-an-addition-travels-in-its-parents-parcel.md))
  commits none of its units. A write-off landing while a parcel for the same
  units is being opened is seen by neither: the units can end on the shelf
  and in the box ([gaps D265](gaps.md)).

- **A return parcel buys no label.** The order page opens one from its return
  and moves it like any parcel
  ([ADR 0413](adr/0413-a-returns-parcel-is-opened-from-its-return.md)), as
  `POST /admin/v1/fulfillments` does naming its return
  ([ADR 0384](adr/0384-a-return-parcel-brings-back-the-return-it-names.md)); the
  provider is handed no destination and no direction, the storefront opens and
  shows none, and the order's shipment list, its timeline and the Parcels
  screen's order column do not show it. The page offers the order region's
  return options that no cart fact rules. Receiving a return does not look at
  its parcel, and cancelling a return leaves its parcel to the operator. A label
  waits for the first carrier plugin whose API opens one.

- **A shipping quote counts every unit sold but a gift card's.** The cart's
  quote and a delivery change price on the units, and an engraving or a wrap is
  a unit as its ring is, so a per-item rate charges for it
  ([ADR 0393](adr/0393-an-add-on-is-written-under-its-line.md)). A gift card's
  units are left out
  ([ADR 0404](adr/0404-a-gift-card-is-no-item-to-the-shipping-quote.md)),
  except in a quote that cannot read the products and on an order line written
  before order migration 000028; a shop that posts its cards cannot charge them
  per item. A parcel takes an add-on line or a card line as it takes any line.

- **An order placed before ADR 0393 lists its add-ons after its lines.** Its
  order read, its invoice, its dossier and the line entity print every line of
  its own first; its page nests them
  ([ADR 0250](adr/0250-an-orders-page-lists-what-was-sold.md)).

- **An order's margin is the margin it was placed at, over its goods alone.**
  Since [ADR 0401](adr/0401-an-order-line-keeps-what-its-goods-cost.md) a line
  keeps its variant's cost in the order's currency, net of tax, and
  `placed_margin` is the net sales of the order's lines that are not gift cards
  less that cost. A canceled unit, a return, a credit, a provider's fee and a
  carrier's cost do not move it. Exchange and replacement goods are outside it,
  and an addition is an order with its own. A line sold before the upgrade or in
  a currency its variant has no cost in has no cost, and the order then states
  no margin; an order whose lines are all gift cards states none at all. A
  bundle costs its own entry. gobit converts no currency. The panel prints a
  line's cost and the placed margin on the order page and the margin on the
  order list, and writes a variant's cost one currency at a time
  ([ADR 0412](adr/0412-the-panel-shows-what-an-orders-goods-cost.md)); the
  sales report and the CSV export and import carry neither. No other margin is
  kept: no provider's fee, carrier's cost, after-sale margin or cost of goods
  in the journal
  ([ADR 0426](adr/0426-the-margin-gobit-keeps-is-the-placed-margin.md)).

- **The in-process harness consumes events like a server.** `InProcess` opens the
  whole application, so its modules subscribe — which is what a test wants, and
  what makes it a member of the consumer group when the installation is on the
  Redis bus. Its bus is the in-memory one by default, so the ordinary case shares
  nothing; pointed at a Redis installation it takes messages the server is owed,
  the way every verb did before
  [ADR 0160](adr/0160-a-command-does-not-take-the-servers-events.md).
- **The bus keeps a message only when it kills its consumers.** A message
  that has been delivered three times without an acknowledgement is kept in the
  Redis bus's dead-letter stream, stands on the outbox relay's alarm and is
  listed, redriven or discarded by `gobit deadletters`
  ([ADR 0273](adr/0273-the-bus-keeps-the-message-it-gives-up-on.md)). A
  handler that returns an error three times is logged and the event counts as
  processed ([ADR 0240](adr/0240-a-failing-handler-is-called-again.md)), and a
  message trimmed by `MaxLen` while it was pending is gone before it can be
  kept. The pile has no bound; a human empties it.

- **The load test is in-process** (`make load-test`, `internal/e2e`): it tests
  correctness under load, it does not produce a capacity plan.
