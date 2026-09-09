# ADR 0083 — A documented chain of requests is executed, not re-implemented

**Summary:** The one document that walks a reader from an empty database to a
served storefront is now run, as a shell script, out of the file itself.

- **Status:** Accepted
- **Date:** 2026-09-09

## Context

`TestTheRouteAddressesInTheProseExist` checks every route address a document
names, WITH ITS VERB, so a curl pointing at a path that moved is already caught.

A command carries more than its address. The name of a header, the field name in
a body, the jq path pulled out of a response — none of those is a route and none
can be resolved against the router. ADR 0044 recorded the shape this takes when
it goes wrong: "its copy-pasteable curl 404ed".

**Measured on 2026-09-09.** Seven documents hold command blocks, and exactly ONE
of them chains: `docs/security.md` feeds `$TOKEN` into `$SC` into `$PK` and then
into a storefront read. A chain is where the unseen names become load-bearing —
one renamed field makes `$TOKEN` the string `null` and every step after it
answers unauthorized, while the document still looks right on the page.

## Decision

**A command block that feeds a variable from one request into another is
EXECUTED by a test.** `internal/smoke/documented_flow_test.go` reads the block
out of the document, hands it to `sh`, and reads the answers.

**It is executed rather than re-implemented.** A Go copy of those six commands
would be a second version of the flow, free to stay right while the document
went wrong — which is the defect, not the fix.

**Three things are substituted and nothing else**: the address, because the
document names the default port and the harness picks a free one; the
credentials, because the document writes a placeholder where a password belongs;
and step 0's `make run`, because the harness has already started that process.

**`internal/arch/documented_flow_test.go` derives the population** of chained
blocks from the documents and requires a named witness for each. Standalone
commands stay with the route gate.

## Consequences

- **The chain is asserted BY POSITION** over the three lines the block prints —
  the two captured steps print nothing — so a break anywhere in the chain turns
  the line after it into an error envelope and names which step failed.
- **Four mutations of the DOCUMENT proved it**: `.data.token` changed to
  `.data.access_token`, the publishable key header renamed, the key created with
  an empty channel list, and a blinded block parser. Each turns the test red.
- **The gate itself was proved five ways**, including the one that matters most:
  giving another document a chained block fails immediately, so the rule grows
  with the documentation rather than being a fact about one file.
- **The empty catalog is not the point.** The storefront step returns an empty
  list because nothing is seeded; what the envelope proves is that the key was
  created BOUND to the channel and accepted at the address built from `$SC`.
- **The test needs `curl` and `jq`, and fails rather than skips without them.**
  Both READMEs already list the two among this repository's tools, and a skip
  shares an exit code with a pass.
- **The other six documents are still only checked at the address level.** Their
  blocks hold standalone commands, and what a chain makes load-bearing a
  standalone command does not.

## Rejected

- **Executing every curl in the tree.** Most are standalone; the route gate
  resolves their addresses, and running them would need a fixture per command
  for no new class of defect.
- **Re-implementing the flow in Go.** The second copy is the failure mode.
- **Asserting on the shell's exit status.** `curl` answers 0 for an HTTP error
  unless asked otherwise, and the document does not ask — so the answers are
  read out of the OUTPUT, which is what a reader looks at too.
- **Skipping when the tools are missing.** "I ran it and it worked" and "I could
  not run it" may not share an exit code.
