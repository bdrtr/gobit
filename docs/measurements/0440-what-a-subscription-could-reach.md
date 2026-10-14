# What a subscription could reach — measured 2026-10-11

Evidence for [ADR 0440](../adr/0440-a-push-device-is-a-push-services-and-its-customers.md)
and gaps D292, D293 and D294. Read on a tree at 6fd949b6 (web-push on the
end-to-end ground, D290); the lanes ran against Docker's `postgres:16-alpine`
through testcontainers, with the e2e harness's customer identity bound
(`storefrontIdentity`, which proves the customer an `X-E2E-Customer` header
names).

## 1. The reproduction, before

A throwaway test in `internal/e2e`, run on 6fd949b6 with nothing else changed
but the ground's template, which gained a `url` block naming the order. Every
storefront call carried the publishable key and nothing else; the broadcast is
the operator's own `POST /admin/v1/webpush/broadcast`.

| Subscribed endpoint | Answer |
|---|---|
| `http://127.0.0.1:<port>/internal/admin/purge?all=1` (a local server) | 201 |
| `http://127.0.0.1:<port>/hop` (answers 307 to a second local server) | 201 |
| `http://169.254.169.254/latest/meta-data/iam/` | 201 |
| `http://[::ffff:127.0.0.1]:1/mapped` | 201 |
| `http://10.0.0.1/private` | 201 |
| `https://192.168.1.1/router` | 201 |

The broadcast answered `attempted 6, delivered 2, removed 0, failed 4` after
10.013 s, the per-device timeout spent on the addresses nothing answered. The
local server received a POST at the purge path and one at the hop path, 182
bytes each; the second server received a POST at the path the 307 named, 182
bytes.

Then a registered customer was created, a stranger subscribed a device of
their own with that customer's id in `customer_id` and no proof (201), and the
customer's order was placed through the checkout flow. The stranger's endpoint
received one POST; decrypted with the stranger's own subscription keys, it read

```
{"title":"Order 1","body":"It is placed.","url":"/orders/order_06GJR91XGFC54QCES9WAHZHPX4"}
```

which is the customer's display id and order id.

The two end-to-end tests written in the fix's direction, run on 6fd949b6 with
the ground listing its fake push service:

```
--- PASS: TestTheWebPushRoutesAnswerWhatTheyDescribe
--- FAIL: TestAStrangerCannotPointTheSenderAtAnAddress     (201 for the local server, wanted 422)
--- FAIL: TestADeviceReceivesOnlyItsProvenCustomersOrders  (201 for the unproven customer, wanted 401)
```

## 2. The endpoint in the log (D294)

`sender.send` wrapped the client's error, a `*url.Error`, whose text is
`Post "<endpoint>": <cause>`, and the fan-out logged it under `error` beside
the device's id, the line the code said carried no endpoint.
`TestAFailedPushNamesNoEndpoint` sends to `https://fcm.googleapis.com/fcm/send/SECRET-REGISTRATION-TOKEN`
through a resolver answering 10.0.0.1 and holds the error to the host.

## 3. After

The same two end-to-end tests on the fix:

```
--- PASS: TestTheWebPushRoutesAnswerWhatTheyDescribe
--- PASS: TestAStrangerCannotPointTheSenderAtAnAddress
--- PASS: TestADeviceReceivesOnlyItsProvenCustomersOrders
```

The local server, the metadata address, the mapped loopback, both private
addresses, `https://fcm.googleapis.com.attacker.test/…` and an https endpoint
at the listed http fake each answer `422 webpush_endpoint_refused`. An endpoint at the listed fake that answers 307
is stored and pushed, and the address the 307 names hears nothing. The
unproven subscription answers `401 identity_refused`, one proving another
customer `403 identity_mismatch`, and the customer's listing holds no device;
the customer's own proven device is pushed the order, and its push decrypts to
the order's id.

The plugin's integration lane holds the same rules on its own tables: a row
stored before the rule at an unlisted local server is removed and neither an
order nor a broadcast reaches it (`attempted 2, delivered 1, gone 1`), each of
six refused subscriptions stores no row, a body refused for its endpoint asks
the identity nothing, and migration 000003 leaves no binding and both devices.

## 4. The query an operator runs before upgrading

Checked on `postgres:16-alpine` against twelve rows: the five endpoint shapes
the four browsers mint (FCM with and without `:443`, Mozilla, Apple, WNS) were
left out, and the seven others were listed — the metadata address, a private
address, FCM over plain http, `fcm.googleapis.com.attacker.test`, FCM with
credentials, FCM on port 8443 and `evilnotify.windows.com`.

## 5. Where a customer id reaches

The premise of D293's exposure is that a customer's id is no secret. Read on
6fd949b6: `cartDTO` (`internal/modules/cart/api/api.go`) answers `customer_id`
on every storefront cart read, so the shop's pages and the scripts on them hold
it; the customer's storefront routes carry it in their paths
(`/store/v1/customers/{id}/…`), which gobit's request log writes; and before
v0.12.0 every receiver of `cart.created` held the cart's id and read the cart
(D282).
