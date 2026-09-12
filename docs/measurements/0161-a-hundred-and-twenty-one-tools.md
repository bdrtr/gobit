# A hundred and twenty-one tools — measured 2026-09-12

What this installation can answer a model client, where the list comes from, and
the four decisions that had to be made first.

## 1. The four decisions, and the fifth

The row was blocked on four, each measured against the tree:

**Transport — stdio, as a tenth verb.** It costs no published name: the verb
lives in the composition root and may read the process's own input, while every
HTTP placement costs something. Under the admin prefix each JSON-RPC frame
becomes an audited POST against the installation's rate limit, and the endpoint
enters the generated document — where the server would list ITSELF as a tool. At
the root, a state-changing route outside every guarded prefix is refused by a
gate.

**Execution — in-process.** A tool call goes through the router this process
assembles, which is the same one `serve` would serve. Nothing is re-implemented
and every ring applies.

**Credential — its own secret key.** The read scopes the questions need, and a
key carrying the superior scope refused before the server opens.

**The document — as it stands.** Every field an MCP tool needs is there except
the privilege, and the two ways to add one are a specification violation or a
silent addition to the published surface.

And a fifth, which the measurement produced rather than answered: a process that
assembles this installation subscribes its modules to the event bus, so an MCP
server that assembled its own would take messages the running server is owed.
That turned out to be true of every verb the binary already shipped, and it was
settled on its own (ADR 0160, D104). The MCP verb assembles with the role that
publishes and does not consume.

## 2. What the derivation actually produced

Run against a real installation — modules registered, routes bound, schema
generated from the router tree:

| | |
|---|---|
| tools derived | 121 |
| tools with a description | 121 |
| tools with an object input schema | 121 |
| distinct names | 121 |

The last row is an assertion rather than a statistic: the derivation flattens a
path into a name, which is exactly the kind of flattening that can make two
addresses meet, and a model calling a shared name would reach whichever the
lookup finds first.

## 3. What the run taught that the reading had not

**Not one operation carries an `operationId`.** The first version of the
end-to-end gate expected `listOrders` and `listProducts` — names invented while
writing the test rather than read from the document. The real list is
`get_admin_v1_orders`, `get_admin_v1_products_id`, and so on for all 121.

That is not a defect. A path-derived name moves when the endpoint moves, which
is the honest failure: a tool that survived an endpoint's move would answer
about something else. What it means is that the names are as stable as the
addresses and no more, and ADR 0044 moved four storefront addresses in a single
day.

## 4. What the document cannot say

The scope. `securitySchemes` emits `http/bearer` and `apiKey`, neither of which
carries scopes, and no operation names the privilege its route requires.

So a tool cannot state what it needs, and a model cannot be told in advance that
a question will be refused. The credential's scopes decide, and a refusal comes
back as the API's own error envelope — which names the code and the missing
privilege, so the model can say which grant is absent rather than reporting the
installation as broken.

Growing the document was rejected for this slice: the scope would then be
re-spelled beside `RequireScope`, which is the drift the known limits already
record against the panel's own table.

## 5. The prose a model reads

Forty-three of the tool descriptions are Turkish, and so are twenty-six of the
parameter descriptions. The language ledger governs FILES — a file is in it or
it is not — and the describe blocks that carry these strings are in it. The rule
is satisfied and the consequence was written down nowhere: the served document
is a published artifact and part of its prose is not English.

It is recorded in the known limits rather than fixed here. The ledger may only
shrink, so the number moves in one direction on its own.

## 6. The gates, and what each is the subject of

| Gate | Subject |
|---|---|
| only the admin reads become tools | the population: a write, a store path and a panel path must not appear |
| a tool is named by its operationId, else by its path | the name a client addresses |
| a tool's input schema is its parameters | what a model fills in |
| the address refuses a missing path parameter | the request that would otherwise reach a DIFFERENT endpoint |
| a tool call becomes a real request | the handler, the address, and the credential on it |
| a refused call is handed back as the API said it | the diagnosis the model needs |
| a notification is answered with silence | the protocol's own rule |
| the tool list is this installation's own schema | the end-to-end claim, against a real assembly |

The last one is the only one that can say the list is the installation's; the
others are about the derivation and are pinned against a document written beside
them.

## 7. Mutations

| Mutation | What failed |
|---|---|
| every method becomes a tool, not only GET | the population check — a write appeared in the list |
| the admin-prefix filter removed | the population check, and the refusal to open with no admin read |
| all three guards on a missing path parameter removed | the address check: the call would have reached the collection |
| the tool call carries no credential | the real-request check: the installation would refuse every question |

The third is the one worth keeping. Two of the three guards are redundant by
construction, and removing one at a time left the claim standing — which reads
as a surviving mutation and is not. Only removing all three showed the test
discriminates.

## 8. What this did not do

It writes nothing and cannot: the tools are GET operations and there is no
method here that reaches another verb. It also answers no question about the
storefront — that surface is a shopper's, scoped by a publishable key this server
does not hold.
