# ADR 0260 — A panel screen reads only what its privilege owns

**Summary:** A test requests every route the panel ships as an operator holding
exactly its privilege and holds all it reads and writes to the module declaring
that privilege; the product and variant pages now read prices and stock only
for an operator holding `pricing:read` and `inventory:read`.

- **Status:** Accepted
- **Date:** 2026-09-30

## Context

The panel spells its privileges itself, and `internal/arch` holds each spelling
to a scope some module declares (ADR 0156). That catches a misspelling. A screen
listed under a plausible but wrong privilege, one module's scope over another
module's data, was caught by nothing, and the known limits said so.

Walking the screens found two: the product and variant pages printed a
variant's whole price set and its stock per location to an operator holding
only `product:read`, and a refused price or stock write drew the variant page
for an operator holding only the write. The API refuses both without the
owning module's privilege.

## Decision

Each route with a privilege is requested, in the panel's own test, by an
operator holding exactly that privilege, and every entity, link end and admin
surface it reaches must belong to the tree declaring the privilege; whatever a
second privilege adds must belong to that privilege's tree. The product and
variant pages read and print a variant's prices only under `pricing:read` and
its stock only under `inventory:read`, and a refused price or stock write draws
the variant page only for an operator holding `product:read`.

## Consequences

- Ownership is read from source: an entity or an admin surface belongs to the
  tree that registers it in the container, a link's far end to the tree that
  registers its entity, and a privilege to the tree whose `Scope` constant
  declares it. Nothing in the check is a list of owners.
- A write is walked twice, accepted and refused by its surface, so the page a
  refusal draws is held too; the refused walk is what found the redraw.
- Twelve mutations were tried, among them moving a screen or a write under
  another module's privilege, guarding a section with another's, and blinding
  the source reader; each fails the gate.
- The currency scales are the one read allowed under any privilege: the region
  fields `/store/v1/regions` publishes to a key carrying no privilege. A read
  asking for any other region field is held to its owner, and an entry nothing
  uses fails the gate.
- An operator holding `product:read` alone no longer sees prices or stock on
  either page. Each section names the privilege that would show it, and an
  installation whose catalog operators held only `product:read` has to grant
  the other two for them to see what they saw before.
- A plugin's screen is the plugin's code and is not walked, and a path a
  screen takes only for a particular value in a record is walked as far as a
  record holding every asked-for field goes. The known limit now names these.

## Rejected

- A table of owners kept in the test: it would be written by the same hand as
  the scope table it checks.
- Walking as the admin scope: it satisfies every privilege, so it cannot tell a
  right guard from a missing one.
- Letting `product:read` show prices because the storefront shows them: the
  storefront shows a shopper's price, and the page shows the whole set, list
  and tier prices included, with the stock at every location.
- Matching entity names to the privilege's prefix: a name is not ownership,
  and `order_line_item` is the order module's while `region` is not.
