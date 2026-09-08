# Performance and operations — measured 2026-09-04

Moved out of `docs/gaps.md` on 2026-09-08 unchanged. A measurement is
evidence, not a decision: it is kept in full and read when a number is
questioned, not on the way to understanding the repository.

Three of five are in place. The two that are not are the two that decide how
this behaves at scale, and one of them has already shown up as a workaround.

### In place

**Connection pool is fully configurable** — `MaxConns`, `MinConns`,
`MaxConnLifetime`, `MaxConnIdleTime` with defaults of 10/2/1h/30m
(`core/db/db.go`). Worth knowing why the settings are verified by
READING THEM BACK from `pool.Pool().Config()` rather than from the config
struct: a mutation that deleted `pgCfg.MaxConns = cfg.MaxConns` outright passed
every test, because the startup log kept printing the CONFIGURED number while
the pool ran on the library default. The operator would have read a limit that
was not in force.

**N+1 is treated as a named defect, not a habit.** Set fetches are single
queries by contract, and the godoc says so where it matters
(`payment/service/service.go`: *"PaymentCollectionsByIDs fetches the identifier
set in a SINGLE query (no N+1)"*). Joins are written out explicitly in the
`.sql` files rather than assembled in Go.

**Images: no in-application resizing, and the CDN is the documented posture.**
Nothing decodes or resizes an image anywhere. The file module states the split
plainly (`file/service/service.go`: *"in an object store the file is served
by the CDN, the application never…"*), `plugins/files3` says the same about the
bucket, and `file/api/serve.go` sets cache headers so a CDN or reverse proxy
may legitimately store the response. The local disk provider exists for
development and says so.

**Graceful shutdown and health endpoints** — `/health` and `/ready` as separate
answers, with `ShutdownTimeout` letting open requests finish
(`core/http/server.go`). The split is load-bearing: Postgres GATES
traffic (`/ready` 503s without it) while Redis only DEGRADES, and which side a
dependency lands on is decided by what its loss does to a request.

### Gaps

1. ~~**No pprof and no benchmarks. Zero `func Benchmark` in the repository.**~~
   **CLOSED 2026-09-05.** Five benchmarks on the paths that run per request,
   and a pprof listener of its own that is off unless configured.

   The first numbers, on an 8845HS:

   | benchmark | per op | allocs |
   | --- | --- | --- |
   | `StorefrontQuery` (24 products, 3 variants each) | 374 us | 8,421 |
   | `ComputeDiscounts` (20 lines, 4 promotions) | 4.5 us | 18 |
   | `AllocateAcross` (20 lines) | 1.3 us | 2 |
   | `AssembleTotals` (20 lines) | 124 ns | 0 |
   | `ApplyTaxResponse` (20 lines) | 89 ns | 0 |

   The finding is the spread. The cart arithmetic — the part that was written
   most carefully, with the remainder rules argued line by line — allocates
   NOTHING and costs about a tenth of a microsecond. The GraphQL read surface
   costs three thousand times as much and allocates 8,421 times per request,
   which is the same order as the database work it was compared against
   (67 ms -> 0.65 ms on the count query). It is the obvious first place to look,
   and nothing before this could have said so.

   The listener is separate rather than a route on the API, because a profile
   takes as long as it was asked to take and `WRITE_TIMEOUT=30s` cuts the
   30-second CPU profile exactly in half. It is unauthenticated, so a
   non-loopback bind is REFUSED at startup outside development, and an arch
   test keeps `net/http/pprof` — which publishes itself through a package-level
   global on import alone — out of every other file.

   The original finding: this is the sharpest inconsistency in the codebase,
   because measurement discipline is otherwise strong: the SQL side is measured
   with `EXPLAIN` read inside integration tests, load fixtures run to 52,000
   rows, and godocs carry real numbers (2.9 ms, 0.56 ms, 67 ms → 0.65 ms). All
   of it is DATABASE-side.

   Nothing measures the Go side. There is no profile endpoint to attach to a
   running process and no benchmark to catch a regression in a hot path — so an
   allocation regression in pricing, promotion computation or JSON encoding
   would be invisible until it showed up as latency in production.

2. ~~**Pagination is offset-based everywhere.**~~ **CLOSED 2026-09-05.**
   The four listings whose tables grow without bound take a cursor: products
   (admin and storefront, REST and GraphQL), orders, customers and carts.

   Measured on 52,000 products with the listing index in place:

   | page | offset | keyset |
   | --- | --- | --- |
   | first | 0.31 ms | 0.06 ms |
   | ~5,000 in | 4.63 ms | — |
   | ~50,000 in | 34.71 ms | 0.08 ms |

   Offset is linear in depth because the database walks and DISCARDS every
   skipped row; keyset is flat because the ordering key goes into the index
   condition. The 423x at the deep end matters less than the SHAPE: a catalog
   that grows makes offset worse and leaves keyset where it was.

   Offset is NOT removed and the change is NOT breaking. A page-numbered admin
   screen needs to jump to page seven, which a cursor cannot do, and at the
   depths such a screen reaches offset is cheap. `after` is additive, and the
   two are refused together because they name different positions.

   The finding worth carrying forward is the SQL shape. Writing the bound as
   `@after IS NULL OR (created_at, id) < (...)` measures perfectly and then
   degrades: Postgres plans a statement per call for its first five executions
   and folds the OR away, so a test sees an Index Cond; on the sixth it switches
   to a generic plan, the OR survives into a Filter, and the seek becomes a full
   index walk — 50,001 rows removed by filter, 4.3 ms instead of 0.065 ms, with
   no code change at that moment. The sentinel form
   (`COALESCE(@after, 'infinity')`) has no OR left to survive and holds under
   both plans. An integration test reads the plan rather than a timing, because
   a timing cannot tell the two apart on a small table.

   **The rest of the listings keep offset alone, and that is a decision rather
   than a remainder.** Offset only costs anything at DEPTH — the table above is
   the whole argument — and a listing whose table is configuration-sized never
   goes there. Tax rates, shipping options, regions, currencies, countries,
   sales channels and customer groups are counted in hundreds; a cursor on them
   would be ceremony, and every parameter that exists has to be documented,
   tested and honored forever. The rule to apply to the next listing is
   therefore: **a cursor where the rows grow with the shop's trade, offset alone
   where they grow with its configuration.** Listings scoped to one parent — a
   product's variants, an order's returns, a company's employees — are bounded
   by the parent and fall on the offset side too.

   The original finding: 101 `limit` and 96 `offset`
   occurrences across the module APIs; no cursor, no `after`, no `before`.

   The cost is already visible rather than theoretical. Offset pagination needs
   a total count for the UI to render page numbers, and that count query was
   measured at 67 ms on the storefront listing and made OPTIONAL to get it to
   0.65 ms. Making the count optional is a workaround for offset pagination, not
   a fix — and deep pages still make Postgres walk and discard every skipped
   row, which gets worse exactly as the catalog grows.

   What it would touch: every list endpoint's response envelope, which is a
   published API shape, so it is a breaking change and belongs in one deliberate
   pass rather than module by module. The ordering columns already exist
   (`created_at DESC` with an id tiebreak in most indexes), which is the part
   that is usually missing.

3. **The catalog's text search has no index that can serve it, and its ceiling
   is now measured** — 2026-09-05.

   Not a defect and not a surprise: the predicate carries a leading wildcard, so
   no B-tree helps even if one existed on `title`. What was worth measuring is
   the SHAPE, and it is the opposite of what "no index" usually implies. A term
   matching almost the whole catalog is answered in 0.03 ms because the ordered
   scan stops at 25 rows; a term matching ONE product costs 9.1 ms and reads all
   730 pages of the table. At 16 clients a selective search runs at 638 to 856
   per second against 11,564 for an unfiltered listing.

   The number to plan against is the throughput one, not the latency: **a few
   hundred concurrent selective searches per second is the first ceiling this
   repository has that is not the response path.** The panel is nowhere near it
   and never will be; a storefront search box reaches it long before the catalog
   grows. The full record — plans, buffer counts, the prepared-statement plan
   flip, the count's behavior and the options not taken — is
   `docs/measurements/catalog-search-cost.md`, and its consequences for B2's remaining work
   are in the B2 section below.

---
