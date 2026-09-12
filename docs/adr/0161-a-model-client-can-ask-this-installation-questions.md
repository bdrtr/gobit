# ADR 0161 — A model client can ask this installation questions

**Summary:** A tenth verb speaks MCP on stdio; its tool list is derived from the
OpenAPI document this process serves, and a tool call is an in-process GET
through the assembled router.

- **Status:** Accepted
- **Date:** 2026-09-12

Measurement: [measurements/0161](../measurements/0161-a-hundred-and-twenty-one-tools.md)

## Context

The admin API answers a hundred and twenty-one read operations and the only
callers it has ever had are a browser and curl. A model client asking "which
orders are stuck" had no way in that did not begin with somebody writing a
wrapper — and a wrapper is a second list of endpoints, free to go stale.

Four decisions blocked the work and a fifth appeared while they were being
measured: a process that assembles this installation subscribes its modules to
the event bus, so a server that assembled its own would have taken messages the
running server was owed. That is now settled independently
([ADR 0160](0160-a-command-does-not-take-the-servers-events.md)) and a verb
assembles without consuming.

## Decision

`gobit mcp` reads JSON-RPC on stdin and answers four methods — initialize, ping,
tools/list and tools/call. The tool list is derived at startup from the document
this process serves, one tool per GET operation under the admin prefix, and a
call is an in-process GET through the router this process assembled, carrying a
secret key the installation minted for it.

## Consequences

Nothing is re-implemented. A tool exists exactly when an endpoint does, it says
what the endpoint's own describe block says, and a call goes through every ring
the admin surface has: identity, scope, quota and the audit log. A tool cannot
reach what an admin client could not.

Read-only is a property of two things rather than a promise. The list is built
from GET operations only, and the credential is refused at startup when it
carries the superior scope — measured by asking the installation what the key
holds, through the same endpoint any client would use.

The names are derived from the addresses, because not one operation in this
document carries an `operationId`. A tool therefore moves when its endpoint
moves, which is the honest failure: a tool that survived the move would answer
about something else.

The document cannot say which privilege a tool needs — it carries no scope, and
the two ways to put one there are a specification violation or a silent addition
to the published surface. So the key's scopes decide, and a refused call is
handed back with the API's own error envelope, which names the missing
privilege. The limit is written down rather than papered over.

Forty-three of the tool descriptions are Turkish, because the language ledger
governs FILES and not the text inside them; a model reads them as they are.

Stdio costs a verb that does not terminate, which is new — every other verb does
one thing and returns. It also means the client forks the process, so there is no
address to protect and no OAuth metadata to serve, which is what the protocol
expects of a stdio server.

## Rejected

**An HTTP transport.** Under the admin prefix every JSON-RPC frame becomes an
audited POST against the installation's rate limit, and the endpoint enters the
document — where the server would list ITSELF as a tool. At the root it is
refused by the gate that keeps a state-changing route inside a guarded prefix.

**Building a second document.** The tool list would then describe what this
package believes the endpoints are. Fetching the served schema in-process makes
"the list is the installation's" true by construction.

**Adding `operationId`s to make the names stable.** Growing the document for a
consumer's convenience, and a second name for every endpoint.

**Writing tools by hand for the questions an operator asks most.** That is the
wrapper this record exists to avoid: a list that is right the day it is written.
