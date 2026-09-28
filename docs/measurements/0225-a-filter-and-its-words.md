# A filter and its words — measured 2026-09-28

The evidence behind [ADR 0225](../adr/0225-the-graphql-storefront-reads-the-vocabulary.md).

## 1. What there was

| Part | State |
|---|---|
| the GraphQL roots | `products` and `product`; no mutation |
| the listing's filters | `collectionId`, `categoryId`, `tagId`, `optionValue`, `attributes` (handles), `variantIds`, `inStock`, `price` |
| where their words were read | REST alone: `GET /store/v1/collections`, `/categories`, `/tags`, `/product-attributes`, and under `/store/v1/sales-channels/{id}` the `option-values` and `product-facets` |
| the REST vocabulary | the service's `ListCollections`, `ListCategories` (public only), `ListTags`, `ListAttributes`; offset pages; not scoped to a channel |
| the field gates | `bindings()` in `schema_test.go`, written by hand: seven types |
| the schema's object types | ten besides `Query`; `ProductList`, `ProductAttribute` and `AttributeOption` bound in `gqlgen.yml` and on no gate (D153) |

`ProductList` entered the schema with the surface (41e6ea1) and the other two
with ADR 0219 (d420bf2); neither gate had read their fields.

## 2. The resolvers

`TestTheVocabularyIsTheRESTReadsOwn`: one document asking all four passes the
collections' and tags' pages as given, the categories' parent and page with
the public-only option set, and answers each list's items and count as the
service returned them. `TestAFailedVocabularyReadIsAnError`: each of the four,
with the service failing, answers the error's code and no data, though the
fake holds a tag to return.

## 3. The price

`TestTheVocabularyIsPricedByItsPage`: under a ceiling of 1,200, each of
`collections`, `categories` and `tags` passes with `limit: 1` and is refused
with `limit: 100`, the refused document reaching no service call.
`TestEveryListFieldIsPriced` reads the vocabulary's list fields: the attributes
and their options priced, each page's `items` priced on its root.

## 4. The gates

`TestEveryObjectTypeIsBound`: every object type of the compiled schema but
`Query` is on the gates' list. With it, `ProductAttribute` declares its
attribute id left out, `AttributeOption` its id, attribute id and rank, and
`ProductList` passes with nothing left out.

## 5. On the production wiring

`TestTheGraphQLStorefrontReadsTheVocabulary`: through the publishable key, one
document answers under a new parent its shown child and not its internal one,
the new attribute with its kind, and the collections and tags pages equal to
the REST reads' at a limit of 100.

## 6. Mutations

| # | Mutation | Killed by |
|---|---|---|
| G1 | internal categories listed | the resolver test, the end-to-end test |
| G2 | the parent dropped | the resolver test, the end-to-end test |
| G3 | the collections' limit and offset swapped | the resolver test |
| G4 | the tags' limit dropped | the resolver test |
| G5 | the categories' limit dropped | the resolver test |
| G6 | the attributes unpriced | the list pricing gate |
| G7, G9, G10 | the collections, categories or tags unpriced | the page pricing test, once written |
| G8 | a bound type left out of the gates | the binding gate |
| G11 | the attributes not read | the resolver test, the end-to-end test |
| G12–G14 | a failed tags, collections or categories read answered empty | the failed read test, once written |

Fourteen mutants, all killed; G7 and G12 survived the first run, nothing
holding the price of a page or the error of a read, and the two tests were
written for them.
