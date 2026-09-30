# ADR 0278 — Every module with a schema answers for what it keeps

**Summary:** A registered module that ships migrations either declares what
it keeps about a person or is named with what its tables hold, and every
declaring module's test holds its declaration to its schema; file,
fulfillment and notification declare the open values they keep.

- **Status:** Accepted
- **Date:** 2026-10-01

## Context

The data-subject sweep asks only the modules that declare. A module that
keeps a person and declares nothing is absent from every disclosure and every
erasure report, and nothing raises it: the payment module kept customers'
balances that way until ADR 0277. The plugins had a gate for exactly this
(`pluginsWithNoPersonalData`); the modules had none. Three undeclared modules
keep values a person can be in: an upload's name and content, a parcel's
tracking number and the caller's data on it, and a failed send's error. The
settings module declared without an audit against its schema, and its first
one found the shop's country undeclared (D192).

## Decision

`internal/app` requires every registered module with a schema to implement
`personaldata.Declarer` or to be named in `modulesThatHoldNobody` with what its
tables hold, and `internal/arch` requires every declaring package under
`internal/modules` and `plugins` to have a test calling `schemaaudit.Cover`. The file,
fulfillment and notification modules declare their open values, and product,
pricing, promotion, region and tax are named as holding the shop's catalog,
prices, promotions, regions and taxes.

## Consequences

- A new module is asked the day it is registered, and a module that starts to
  declare is held to its schema the same day.
- The three new declarers offer no disclosure and no erasure: their rows name
  an order or nobody, not a customer. The sweep reports them unresolvable in a
  dossier and retained in an erasure, with what they declare.
- An upload's `url` stands for its content, which gobit never reads and a
  photograph can hold a person in; `original_name` is the client's word for it.
- A parcel's tracking number and link are declared: the carrier resolves them
  to the addressee. The address itself is still kept by the order alone.
- A module named as holding nobody is still checked by the name floor of
  `TestEveryPersonColumnIsDeclared`; the entry is a judgement, not a proof.
- The webpush plugin's audit judges every column, not only the ones whose
  names look like a person. The plugins that declare nothing stay under
  `pluginsWithNoPersonalData`; contrib's modules keep their own readers.

## Rejected

- Requiring every module with a schema to declare, an empty declaration for
  the ones that hold nobody: an empty list says nothing about why, and the
  reason is what the next reader needs.
- Holding the "nobody" modules to a full column audit: their tables describe
  the shop, and judging every column of the catalog would be a list longer
  than the argument it records.
