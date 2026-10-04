# Who would call an analytics contract — measured 2026-10-05

The evidence behind
[ADR 0390](../adr/0390-an-analytics-product-hears-gobit-through-the-bus.md),
taken on the tree at 5c58cda6. Paths and symbols only, no line numbers: the
files move, the findings are what they hold.

## Every contract has a caller on the core's side

The exported interfaces of `core/provider`, `Provider` aside, and the function
that calls one of their methods while naming the contract or a type of the
method's signature. Each call site was read; the gate's own run credits the same
files.

| Contract | Calling package | Calling function | What it names |
|---|---|---|---|
| `PaymentProvider` | `internal/modules/payment/service` | `CreateSession` | `CreateSessionInput` |
| `SessionInspector` | `internal/modules/payment/service` | `reconcileOne` | the contract, by type assertion |
| `FulfillmentProvider` | `internal/modules/fulfillment/service` | `quote`, `CreateFulfillment` | `QuoteInput`, `CreateFulfillmentInput` |
| `ShipmentTracker` | `internal/modules/fulfillment/service` | `TrackShipment` | the contract, by type assertion |
| `NotificationProvider` | `internal/modules/notification/service` | `send` | `Notification` |
| `TemplateHolder` | `internal/modules/notification/service` | `OrderCompleted` | the contract, by type assertion |
| `FileProvider` | `internal/modules/file/service` | `Upload` | `UploadInput` |
| `ErrorReporter` | `core/errorreport` | `Sink.Report` | `ErrorEvent` |
| `Classifier` | `internal/jobs/reviewsuggest` | `classify` | `ClassifyInput` |

The classifier is named in the composition root only by a resolve:
`addReviewSuggestJob` in `internal/app` hands it to the job, and the job calls it
through a local interface narrowed to `ID` and `Classify`. The registration
methods on the plugin host and the modules' registries store a provider and call
nothing on it but `ID`.

One credit is not the call it looks like: `core/errorreport/handler.go` calls
`Sink.Report` with an `ErrorEvent`, a method of the same name on the sink rather
than on the reporter. `Sink.Report` itself calls the reporter, so the contract
is credited either way; the shape is the gate's known blind spot, below.

## Where the implementations live

A method declaration of the contract's name, production files only:

```
$ grep -rlE '^func \([a-z]+ \*?[A-Za-z]+\) Quote\(' --include='*.go' --exclude='*_test.go' internal plugins core
internal/modules/fulfillment/manual/manual.go
$ grep -rlE '^func \([a-z]+ \*?[A-Za-z]+\) Track\(' --include='*.go' --exclude='*_test.go' internal plugins core
internal/modules/fulfillment/manual/manual.go
$ grep -rlE '^func \([a-z]+ \*?[A-Za-z]+\) Classify\(' --include='*.go' --exclude='*_test.go' internal plugins core
plugins/aianthropic/classifier.go
$ grep -rlE '^func \([a-z]+ \*?[A-Za-z]+\) HoldsTemplate\(' --include='*.go' --exclude='*_test.go' internal plugins core
plugins/notificationsmtp/plugin.go
```

`FulfillmentProvider` and `ShipmentTracker` are implemented in a module and in no
plugin; `Classifier`, `TemplateHolder` and `ErrorReporter` only in plugins; the
payment, notification and file contracts on both sides. Where an implementation
lives does not decide the shape; who calls it does.

## What each plugin does with the bus and the outside

| Plugin | Subscribes | Sends out |
|---|---|---|
| `webhookout` | yes | HTTP |
| `webpush` | yes | HTTP |
| `analytics` | yes | nothing |
| `searchpg` | yes | nothing |
| `aianthropic` | no | HTTP |
| `errorotlp` | no | HTTP |
| `errorsentry` | no | HTTP |
| `files3` | no | HTTP |
| `paymentpaytr` | no | HTTP |
| `notificationsmtp` | no | SMTP |
| `paymentstripe` | no | nothing, a skeleton |

A plugin that both hears the bus and speaks to the outside exists twice, and
neither goes through a provider contract.

## No consumer is counted

```
$ grep -rliE 'posthog|segment\.io|mixpanel|amplitude|google-analytics|gtag' --exclude-dir=.git .
internal/app/seed.go
internal/app/seed_integration_test.go
docs/measurements/0061-a-locale-has-no-source.md
```

All three are false positives of the case-insensitive `gtag`: the seed's
`flagTags` and a measurement's `langtag`. The in-tree `examples/storefront` is
inside that search; the Next.js storefront kept outside the tree
(`gobit-storefront`) has no hit in its `src` either. No `.go` or `.sql` file
names a visitor id:

```
$ grep -rliE 'visitor_?id|visitorid' --include='*.go' --include='*.sql' .
$ echo $?
1
```

## What a subscriber receives, and what joins

`docs/extending.md` lists `order.placed` with `customer_id`. `webhook-out`
withholds it through `redactedFields` in `plugins/webhookout/module.go`, and
names the removal in the body's `redacted` list; a plugin that subscribes gets
the payload as published. The cart topics carry `cart_id` and no identity, and
the analytics plugin's migration says so beside its table, keyed on the event's
own id.

The two storefronts keep the cart id differently. `examples/storefront` keeps it
in `localStorage` under `gobit-storefront-cart`, where a page script reads it.
The out-of-tree `gobit-storefront` keeps it in the `gobit_cart` HttpOnly cookie,
which its `src/lib/gobit/cart.ts` says no script reads: there an event joins on
the cart only when the storefront's server sends it, or when the server hands
the page the id. `order.placed` carries the customer. A visit before a cart has
no id the server knows.

## What a plugin path has to add

D236 in `docs/gaps.md`: every outbox event is published twice, on the fast path
and by the relay. `core/eventbus` calls a failing handler twice more, a quarter
of a second and a second later, and has no dead letter (ADR 0240). `webhook-out`
absorbs both: its enqueue is `ON CONFLICT (endpoint_id, event_id) DO NOTHING`,
and the delivery id in its body does not change between retries. A plugin that
calls a product directly counts every event twice and loses an outage's events
unless it keeps a record keyed on the event id and sends durably.

## The gate and its mutants

`TestEveryProviderContractHasACaller` in `internal/arch/provider_caller_test.go`
and `TestTheProviderCallerGateReadsItsShapes`, which runs the same reader on
fixtures, run together with `-count=1` after each mutation; the base run was
green. A result names the test that failed when it is the fixture one. "M1" adds
an `Analytics` interface embedding `Provider` with a `Track` method, and an
`AnalyticsEvent` struct, to `core/provider`, with the three published-name
lines in `internal/arch/testdata/published-names.txt`. With M1 applied and the
gate file removed, the whole `internal/arch` package was green: ADR 0153's
finding still held on this tree. Every mutant's production code built.

| # | Mutant | Result |
|---|---|---|
| M1 | the inert interface | fails, names `Analytics` |
| M2 | M1, a `RegisterAnalytics` method on the plugin host storing it in the container, and a resolve of it in `internal/app/setup.go` | fails: no method call |
| M3 | M1, a registry in `internal/modules/cart/service` whose `Register` stores it under `ID` | fails |
| M3b | M3 with `ID` declared on `Analytics` itself rather than embedded | fails |
| M3b' | M3b with `ID` removed from the excluded methods | passes: the `ID` exclusion is what refuses M3b |
| M4 | M1 and a compile-time assertion that the manual payment provider implements it | fails: an assertion is not a function |
| M5 | M1 and a `core/plugin` function calling `Track` with an `AnalyticsEvent` | fails |
| M5' | M5 with `core/plugin/` removed from the excluded trees | passes |
| M6 | M1 and a `core/providertest` suite calling `Track` | fails |
| M6' | M6 with `core/providertest/` removed from the excluded trees | passes |
| M7 | M1 and a `plugins/analytics` function calling `Track` | fails |
| M7' | M7 with `plugins/` removed from the excluded trees | passes |
| M8 | M1 with `Close` declared on `Analytics`, and an `internal/app` function resolving it and calling `Close` | fails |
| M8' | M8 with `Close` removed from the excluded methods | passes |
| M9 | M1 and an `internal/modules/cart/service` function calling `Track` with an `AnalyticsEvent` | passes: the gate admits a caller |
| M10 | the unit widened from the function to the file, then M1 and a function in `internal/modules/fulfillment/service/tracking.go` that only stores the contract | passes: the file already calls `ShipmentTracker`'s `Track` |
| M10' | the same function with the unit left at the function | fails |
| M11 | `classify` builds its input in a helper and no longer names `ClassifyInput` | fails, names `Classifier` |
| M12 | the contract reader matches struct types instead of interfaces | fails at the floor: none with a method |
| M12b | the contract reader stops after the first file of `core/provider` | fails at the floor: one with a method, `Classifier` |
| M12b' | M12b with the floor assertion removed | passes: the floor alone sees a reader that reads part of the package |
| M13 | the import path the caller reader matches is misspelled | fails: not one contract has a caller; the fixture test fails too |
| M14 | an `Analytics` interface that embeds only `Provider` | fails: nothing a caller could call |
| M15 | a `PaymentInspector` interface embedding `PaymentProvider` and `SessionInspector`, with no method of its own | passes: it has their methods and the payment module's callers |
| Ma | the alias check removed, so any package's selector names a `core/provider` type | fixture test fails: another package's `Alpha` credited |
| Mb | a function literal at package level no longer a unit | fixture test fails: the literal's call to `Beta` not credited |
| Mc | a contract with no method of its own no longer given its embedded contracts' methods | fixture test fails: the composite `Both` has no caller |

M3 fails without the `ID` exclusion too, because a contract that declares a
method of its own is not given those it embeds, and `Provider` is never a
contract; M3b is the shape the exclusion holds, the one `ErrorReporter` has.

What the gate admits, by construction: a caller written to pass it; a core
subscriber that calls the contract with what the bus already carries (M9's
shape, in the core); and a function that names the contract and calls a method
of the same name on something else, as the error-report handler does with the
sink.
