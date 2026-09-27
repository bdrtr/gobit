# A receiver that asked for less — measured 2026-09-27

The evidence behind [ADR 0218](../adr/0218-a-webhook-receiver-can-narrow-what-it-gets.md).

## 1. What a receiver could say before

| Part | State |
|---|---|
| topics | `webhook_endpoint.topics text[]`, non-empty by a CHECK and at registration; the queue writes a delivery only where `topics @> ARRAY[event]` |
| within a topic | every event and every field, `customer_id` removed and named in `redacted` |
| a change | none: delete the receiver and register again, with a new secret |
| payloads | one builder function per publisher, every value a string (`docs/extending.md`) |
| a request body | at most 2 KB |
| the documents | `known-limits.md` said no receiver chose its topics (D149); `extending.md` named six subscribers and six topics, where there were eight and ten (D150) |

## 2. The census

`TestTheTopicFieldsAreEveryPayloadsFields` resolves every publish whose topic
resolves and reads its payload's keys, from a builder of the same package that
returns a map literal or from a local map grown by index assignments in the
publishing function. It found the ten forwarded topics' fields, and one key no
publisher sends: `product.deleted`'s `status`, which the builder adds only for a
non-empty status and the deletion passes none. That key is a named exception,
and the exception fails when the census stops reading it.

## 3. The validation

`TestANarrowingIsNormalized`: values trimmed and listed once, field lists in the
order given and listed once, nothing given stored as an empty object.
`TestANarrowingThatWouldMatchNothingIsRefused`: twelve refusals, among them a
topic the receiver does not take, a field the topic does not carry, the redacted
`customer_id` as a filter and as a field, an empty filter, fifty-one values and a
value of 129 bytes; fifty values and 128 bytes are taken.

## 4. On a real PostgreSQL

`TestAFilterDecidesWhichEventsAReceiverIsOwed`: of four `cart.created` events, a
receiver filtering two regions is owed two, one filtering a region and a
currency is owed none because the currency did not match, and an unfiltered one
is owed four; a cart without a region matches no filter.
`TestAFieldListNarrowsTheBodyThatIsQueued`: the receiver is sent `order_id` and
`total`, signed, with `customer_id` still named as redacted; the topic it did not
list is sent whole; the queued row holds the narrowed body.
`TestARegistrationCarriesItsNarrowing`: the admin endpoint stores and answers the
filters and fields and refuses a filter on a field the topic does not carry.
`TestAReceiverIsChangedInPlace`: a PATCH replaces what it names and keeps the
description and the secret, the listing shows the filter and `topic_fields`, the
delivery queued before the change stays, a topic taken away while a filter names
it is refused and taken with the filter, an empty change and a URL are refused,
and an unknown receiver is 404. `TestAChangeIsCheckedAgainstTheRowAsItIs`: a
second transaction holds the row and takes a topic away; a change filtering that
topic meanwhile is refused after the commit and stores nothing.
`TestTheNarrowingColumnsHoldObjects`: the two constraints refuse a non-object.
`TestTheNarrowingRollsBackAndItsReceiversStay`: rolling back 000002 keeps the
receiver and drops the columns, and the migration applies again. The two older
migration tests read the version from the files rather than as 1.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| N1 | the filter ignored | the filter test, the change and registration tests |
| N2 | an event without the field matching | the filter test |
| N3 | the field list ignored | the field list test, the registration test |
| N4 | the field list keeping every field | the field list test, the registration test |
| N5 | a registration storing no filter | the filter test, the registration test |
| N6 | a change not locking the row | the held-row test |
| N7 | a narrowing for a topic not taken | the refusal test, the change tests |
| N8 | a filter on a field not carried | the refusal test, the registration test |
| N9 | a listed field not carried | the refusal test |
| N10 | a filter with no field | the refusal test |
| N11 | a field list with no field | the refusal test |
| N12 | a value listed twice | the normalization test |
| N13 | one value too many | the refusal test |
| N14 | a value one byte too long | the refusal test |
| N15 | a field listed twice | the normalization test |
| N16 | a value not trimmed at its end | the normalization test |
| N17 | a registration dropping its filters | the registration test |
| N18 | the registration answer without its filters | the registration test |
| N19 | a change keeping the old filters | the change tests |
| N20 | a change keeping the old topics | the change test |
| N21 | an empty change taken | the change test |
| N22 | a change not checked whole | the change tests |
| N23 | the change route not mounted | the change tests |
| N24 | the listing without the field names | the change test |
| N25 | the listing without the filters | the change test |
| N26 | a filter that is no object allowed | the constraint test |
| N27 | a field list that is no object allowed | the constraint test |
| N28 | a field left off TopicFields | the census, the normalization and field list tests |
| N29 | a field the payload never carries listed | the census |
| N30 | the redacted field listed | the census |

None survived. The registration, held-row and constraint tests were written
before the run, from reading which of the planned mutants the first tests could
not see: the filter and field tests registered through the store rather than the
endpoint, and nothing held the receiver's row. N29 and N30 missed their anchors
on the first run and were run again with them corrected.
