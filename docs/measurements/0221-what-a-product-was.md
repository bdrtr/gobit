# What a product was — measured 2026-09-28

The evidence behind [ADR 0221](../adr/0221-a-product-keeps-its-revisions.md).

## 1. The writes before

| Service write | Tables | Transaction | Product row lock | Event |
|---|---|---|---|---|
| CreateProduct, UpdateProduct | product, tags, categories, options, images, variants | service | no | product.created / updated |
| CreateVariant, CreateOption | product_variant, product_option(_value) | service | yes | none |
| UpdateVariant, SetVariantOptionValues, DeleteOption, DeleteOptionValue | variant, option rows | service | no | none |
| DeleteVariant, AddOptionValue, UpdateProductImage | one row | none | no | none |
| AddProductImage, RemoveProductImage | image and the upload link | none | no | none |
| SetProductAttributes | product_attribute_value | repository | no | none |
| ApplyDueSchedules | product.status | one statement each | row locks in it | product.updated |
| DeleteCollection, DeleteProductType | product.collection_id / type_id of many | service | no | none |
| DeleteTag, DeleteCategory, attribute removals | the vocabulary row; the products' rows hidden by joins | one statement | no | none |

`GetProduct` assembles the admin view from up to nine reads with no enclosing
transaction. No table carried a version; the admin audit log records the
request, not a difference; ADR 0013 records that two operators saving one
product overwrite each other. The CSV import writes through the service
methods. Seventeen service writes reached a table of the view.

## 2. The service

`TestAWriteRecordsARevisionOfTheView`: a creation is revision 1 with nothing
changed, a title edit revision 2 naming `title`, the same edit again nothing.
`TestAProductWrittenBeforeRevisionsBeginsWithWhatItWas`: a product at version 0
gets its old title as revision 1 and the new as 2.
`TestEveryWriteToAProductsContentIsARevision`: twelve writes to options, values,
variants, images and attribute values, each a revision naming its field.
`TestARevisionNamesTheRequestThatMadeIt`. `TestARestoreWritesBackTheContentAsANewRevision`:
title, a subtitle cleared, material, metadata, collection, type, tags,
categories and attribute values back, the status and a second variant left, the
restore's own revision naming seven fields. `TestARestoreLeavesOutWhatWasRemovedSince`:
a removed collection, type, tag, category, option and attribute named in that
order, the standing tag and option kept. `TestARestoreRefusesWhatItCannotWrite`:
a handle taken since 409, version 9 404, version 0 422, nothing written.
`TestRevisionsArePagedNewestFirst`, `TestARestoreIsAnnouncedAsAnEdit`. The fake
store's update applied five of the patch's sixteen fields; it applies all of
them now (D151).

## 3. The surface

`TestTheRevisionsAreReadAndRestoredOverTheAdminSurface`: the listing without
snapshots, one revision with its snapshot, a restore answering the product with
its version and `dropped` as an empty list rather than null.
`TestARevisionIsAddressedByAWholeVersionFromOne`: 0, -1, two and 1.5 refused 422
before the service. The scope tables gained the three routes. The GraphQL field
gate required the version be left out of the storefront schema, with its reason.

## 4. On a real PostgreSQL

`TestARevisionReadBackFromJSONBEqualsTheViewItWasTakenOf`: metadata with nested
objects, a list and 1.50, read back from JSONB, compares equal and a repeated
edit records nothing. The listing's hand-written column list had to gain
`version`; the three integration tests reading the catalog said so, the third
time a column appended to `product` was missed there. `TestAProductWrittenBeforeRevisionsBeginsWithWhatItWasOnTheRealSchema`,
`TestARestoreWritesNullsBackOnTheRealSchema`: subtitle, material and weight NULL
again, a removed tag dropped. `TestTheScheduleRecordsTheRevisionOfWhatItPublished`:
`status` changed, no request. `TestAWriterWaitsForTheRevisionBeforeItsOwn`: a
transaction holding the product's lock appends revision 2; a variant rename
asked meanwhile waits and appends 3 naming `variants`. Its first form renamed
the product, whose own UPDATE waits for the row lock anyway, so taking the lock
out of `revise` survived it (R1). `TestTheRevisionSchemaRefuses`: six
constraints. The relation test removing a product row outright was refused by
the revisions' foreign key; it cascades now, as every other row of a product's does.

## 5. On the production wiring

`TestAProductsRevisionsAreReadAndRestoredOverTheAdminSurface`: created and
edited over `/admin/v1`, two revisions, the edit's naming `subtitle` and `title`
and the request id of its response, the first read back, restored as version 3
with the subtitle cleared, version 9 refused 404, and the storefront body
without `version`. The e2e ground mounts no audit log; the audit middleware
and the revision read the same `RequestIDFromContext`.

## 6. The cost

A product with ten variants, three images, five tags and a description: a
snapshot of 3,316 bytes. Two hundred title edits on a local container: p50
0.87 ms and p90 1.07 ms without revisions, 2.03 ms and 2.44 ms with them.

## 7. Mutations

| # | Mutation | Killed by |
|---|---|---|
| R1 | no row lock | the held-lock test, once written |
| R2 | no first revision of what was | the service and integration tests |
| R3 | a revision of nothing | the service and JSONB tests |
| R4 | timestamps kept | the service and JSONB tests |
| R5 | the stored snapshot compared as JSONB wrote it | the JSONB test |
| R6 | every field changed | the service, integration and end-to-end tests |
| R7 | the version not counted on | the service, integration and end-to-end tests |
| R8 | no request id | the service and integration tests |
| R9 | a creation not a revision | the service, integration and end-to-end tests |
| R10 | the schedule's pass not revised | the schedule test |
| R11–R16 | a variant update, an image, attribute values, an option value, a product update, an option outside `revise` | the arch gate and the service test |
| S1 | a restore keeping what the revision lacked | the NULL and end-to-end tests |
| S2–S4 | tags, categories, attribute values not restored | the restore test |
| S5–S10 | a removed collection, type, tag, category, option, attribute kept or unnamed | the removal test |
| S11 | a taken handle not checked | the refusal test |
| S12 | a restore not announced | the event test |
| S13 | removed tags not filtered in SQL | the NULL test |
| A1 | the listing not mounted | the scope and end-to-end tests |
| A2 | a restore under the read scope | the scope test |
| A3 | version 0 taken | the address test |
| A4 | `dropped` as null | the surface test |
| A5 | the admin product without its version | the surface and end-to-end tests |
| Q1 | the listing oldest first | the end-to-end test |
| Q2 | the first revision read as the latest | the integration tests |
| Q3 | the listing's columns without `version` | the JSONB test |
| Q4 | the version not stamped | the integration tests |
| Q5 | no cascade | the relation test |
| Q6 | no version check | the schema test |

Forty mutants. R1 survived the first run, for the reason in section 4; S7 and
S8 did not compile in their first form and were rewritten.
