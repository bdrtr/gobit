# What a Turkish cluster folds — measured 2026-10-20

Evidence for [ADR 0460](../adr/0460-an-ascii-capital-i-folds-on-every-cluster.md)
and gap D315.

## The cluster

`postgres:16-alpine` (built `--with-icu`), the image the integration lanes
run. A second database on the same container:

```sql
CREATE DATABASE gobit_turkish TEMPLATE template0
    ENCODING 'UTF8' LOCALE_PROVIDER icu ICU_LOCALE 'tr-TR' LOCALE 'C.UTF-8';
```

`SELECT lower('I')` there answers U+0131, the dotless i. A glibc cluster
created with `--locale=tr_TR.UTF-8` answers the same (found by the review
that raised D315, on `postgres:16` with `localedef -i tr_TR`).

## Before: order 000046's function

`TestASearchFoldsTheAsciiCapitalIOnATurkishCluster`
(`internal/modules/order/search_turkish_integration_test.go`), with the
order module migrated to 000046, `order_search_document(NULL, stored) @@
order_search_query(q)`:

| stored | q | found |
|---|---|---|
| `Iris WILLIS` | `iris willis` | no |
| `Iris WILLIS` | `IRIS WILLIS` | no |
| `Iris WILLIS` | `\u0130ris W\u0131ll\u0131s` | no |
| `Iris WILLIS` | `willis` | no |
| `\u0130ris W\u0131ll\u0131s` | `IRIS WILLIS` | no |
| `iris willis` | `IRIS WILLIS` | no |

The Turkish spelling is written with escapes, as the test writes it: U+0130
is the dotted capital I, U+0131 the dotless i. The other six of the twelve
pairs were found. A stored capital I keeps a dotless i in the document, and
a q's capital I folds to one too, so even `IRIS WILLIS` missed `Iris
WILLIS`, whose third letter is a small i.

## After: order 000047's function

All twelve pairs are found. On the default `C.UTF-8` cluster the search
integration tests of the three modules (D311's) answer as before.
