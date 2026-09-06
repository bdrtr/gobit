# Go E-Commerce Infrastructure — Architecture Notes

This document summarises the design decisions of a Go e-commerce core that is embeddable (PocketBase-style), fast, and ships with AI features built in.

---

## 1. Founding Philosophy

- **A library, not a fork.** The core is a Go module; every project pulls it with `go get` and brings it up with a thin `main.go`. The fork model makes projects diverge and turns upgrading into a nightmare.
- **API-first.** Every client uses the same API, the admin panel included.
- **Modular monolith.** A single binary at first, packages split by domain. Microservices are not adopted early.
- **Measure, don't guess.** `pprof`, benchmarks and metrics from day one.

---

## 2. Distribution Model

```
github.com/you/ecom            → core module (semver)
  /catalog /cart /order /payment /inventory /customer /ai
  /app                          → App struct, lifecycle, hook registrations
  /internal                     → everything that is not exposed

github.com/you/ecom-starter    → template repo (100-200 lines)
  main.go                       → build the app, wire the hooks, run
```

### Extension points

These are what remove the need to fork:

| Mechanism | Example |
|---|---|
| Interfaces | `PaymentProvider`, `ShippingProvider`, `TaxCalculator`, `PriceResolver`, `ai.Provider` |
| Hook / event | `app.OnOrderCreated(fn)`, `app.OnReviewModerated(fn)` |
| Router access | The project adds its own endpoints and wraps the core's in middleware |
| Migration merging | Core + project migrations, ordered in a single runner |
| Field extension | Free-form fields such as `Product.Metadata jsonb` |
| Compile-time plugin | `import _ "github.com/you/ecom-iyzico"` → registers inside `init()` (the Caddy model) |

### Rules

- The public API is kept small; `internal/` is used generously.
- A breaking change gets a `/v2` module path.
- "Does this belong in the core or in the project?" is asked on every line. Generalising early is as harmful as forking.
- Optional CLI: `ecom new`, `ecom migrate`.

---

## 3. Technology Choices

| Area | Choice | Note |
|---|---|---|
| HTTP | `net/http` (the Go 1.22+ router) or `chi` | Gin/Fiber are not required |
| Database | PostgreSQL + `pgx` + `sqlc` | No ORM, type-safe SQL |
| Migration | `goose` or `atlas` | |
| Cache / session / cart | Redis | Product-detail and category cache, invalidated on write |
| Search | Postgres full-text → Meilisearch/Typesense | `pgvector` for semantic search |
| Queue | A DB outbox table + worker → NATS / Redis Streams | The DB is enough to start with |
| Log / trace / metric | `slog`, OpenTelemetry, Prometheus | |
| Config | env + `envconfig` / `koanf` | |
| Test | A real Postgres via `testcontainers` | The repository is not mocked |

---

## 4. Layers and Code Layout

```
handler → service → repository
```

- Interfaces are defined on the consuming side.
- `context` is carried everywhere (timeout, cancel, trace).
- The transaction boundary is in the service layer, not in the repository.
- No global state; dependencies are passed through the struct.
- Every `go` gets an exit path; `errgroup` is used.

---

## 5. Data Model Decisions

- **Money:** never a float. Integer minor units (kurus) or `decimal`.
- **Stock:** optimistic locking or `SELECT ... FOR UPDATE`. Overselling is the classic mistake.
- **Product/variant:** combinations (colour-size), stock and price per SKU.
- **Order state machine:**
  `created → paid → fulfilled → shipped → delivered`
  `→ cancelled / refunded`
  The transitions are enforced in code.
- **Idempotency key:** mandatory for payment and order creation.
- **Outbox pattern:** when an order is placed, work such as mail, stock and invoicing goes onto the queue as an event.
- **Pagination:** cursor-based, not offset.

---

## 6. Core Features

### Mandatory
- Product / variant / SKU / stock
- Cart (guest + member, mergeable)
- Coupon / promotion engine
- Order management and timeline
- A payment-provider interface, idempotent webhook handling
- Carrier integration, return / cancellation flow
- Customer, address, auth (JWT + refresh or an opaque session, `argon2id`)
- Admin API + RBAC + audit log
- Health / readiness endpoints, graceful shutdown

### Turkey-specific (the entry ticket)
- e-Fatura / e-Arsiv integration in the core
- Domestic carrier APIs (Yurtici, Aras, MNG, PTT) behind a single interface, tracking webhooks included
- iyzico / PayTR / Param + installment-table computation
- KVKK: explicit-consent records, data export / erasure endpoints

---

## 7. Performance

- The product / category endpoints are CDN-friendly with `ETag` and `Cache-Control`; the cart and personal pricing are separate endpoints.
- Connection-pool settings, no N+1 (with `sqlc` the joins are written out explicitly).
- Images live on a CDN; resizing is not done inside the application.
- Rate limiting, input validation, no PII in the logs.

---

## 8. AI Subsystem (the `ai` package)

The LLM is not a tool called from outside; it is a subsystem of the platform.

### The core design

```go
type Provider interface {
    Complete(ctx context.Context, req Request) (Response, error)
}

type Task[In, Out any] struct {
    Name    string          // "review.moderate"
    Version string          // prompt version, logged with the output
    Schema  json.RawMessage // structured output
    Build   func(In) Request
    Parse   func(Response) (Out, error)
}
```

- Every task has one shape: input → prompt → structured JSON → a validated Go struct.
- The provider is swappable (Anthropic, OpenAI, local). A small model can be wired up for cheap work and a large one for complex work.

### The first task: review moderation (`review.moderate`)

1. The review is stored → `review.created` onto the outbox.
2. A worker sends it to the LLM; the HTTP path does not stay synchronous.
3. The output schema:
   ```json
   { "decision": "approve|reject|flag", "confidence": 0.93,
     "reasons": [], "sentiment": "...", "topics": [] }
   ```
4. Decision table: high confidence + approve → publish; reject → hide; low confidence / flag → the moderation queue. The thresholds come from config.
5. Human decisions are recorded → an audit trail plus an eval set.
6. Retry with backoff; on a provider error the review waits in `pending` and is never lost.

### The AI features after it (on the same plumbing)
- A per-product review summary ("what customers say"), refreshed on a new review
- Q&A drawn from the reviews ("does it run small?")
- Conversational search: natural language → filter
- Attribute extraction from a product photo, and a category suggestion
- A product description / SEO draft (a human does the publishing)
- Semantic / visual search (`pgvector`)
- A support assistant that can see an order and open a return, through tool use
- Fake-review and fraud signals
- Dynamic-price and stock-forecast suggestions (as suggestions, not automatic)

### Rules
- Prompts are not embedded in code; they are versioned files. Every output records the prompt version and the model name.
- Token usage is collected as a metric; a budget per task; a hash-based cache for identical input.
- PII is masked before it is sent; recording that complies with KVKK.
- Prompt injection: the user's text sits in its own block and does not mix with the system instruction. The model's decision is not the last word (threshold + human).
- Eval: 200-300 labelled samples; when a prompt changes, accuracy is measured with `go test`.
- Fallback: if the provider goes down the feature degrades, not the site.

---

## 9. Admin Panel

**Decision:** it ships built in. An embedded panel is half of adoption.

### Structure
- The panel is not part of the core; it is a client of the `/admin/api/*` endpoints.
- The SPA is embedded into the binary with `embed.FS`; a single-file deploy.
- An off-the-shelf design system (shadcn and the like); no chasing an original design.

### Scope
- **In the core:** product/variant/stock, order management and timeline, customers, coupon/promotion, the AI moderation queue, settings, users/roles, basic reports.
- **Outside:** advanced analytics, marketing automation, a page designer.

### Extensibility
- `app.Admin().AddPage("Shipping Labels", url)` → iframe / micro-frontend.
- A JSON schema for the `Metadata` fields → the panel draws the form automatically.
- Plugin "slots" on the order and product detail screens.

### The principle
A narrow panel that works completely beats a broad one that is half-finished. The order and product screens are made flawless first.

---

## 10. Niche Features Likely to Be Asked For

### Commerce models
- Subscriptions and recurring orders (the `subscription` axis is added to the state machine early)
- Multi-vendor / marketplace: per-seller commission, payout, shipping
- B2B: a price list for a customer group, requesting a quote, payment on terms, a minimum order
- Pre-order and the back-in-stock waitlist
- Digital product and licence delivery

### Experience
- Real-time stock / price (SSE / WebSocket)
- Single-page checkout, guest checkout, saved cards, passkey / WebAuthn

### Operations
- The event stream exposed outward (webhook + NATS)
- Feature-flag and A/B-test plumbing
- Multi-store / multi-currency / multi-language in one installation
- An audit log and a "what happened to this order" timeline

---

## 11. Priority Order

1. **The entry ticket:** the e-invoice + carrier + payment trio, flawless.
2. **The differentiator:** the AI assistant, review moderation, conversational search.
3. **The new market:** subscriptions, B2B.
4. **Last:** the marketplace (the highest complexity of all).

---

## 12. Common-Mistakes Checklist

- [ ] Holding money in a float
- [ ] Writing stock without a lock (overselling)
- [ ] Opening the transaction in the repository
- [ ] Goroutine leaks
- [ ] Global state
- [ ] Offset pagination
- [ ] Embedding the prompt in code
- [ ] Mocking the repository in tests
- [ ] Growing the public API needlessly
- [ ] The panel fusing into the core
