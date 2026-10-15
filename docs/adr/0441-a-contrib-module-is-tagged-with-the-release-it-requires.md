# ADR 0441 — A contrib module is tagged with the release it requires

**Summary:** Every release tags each module under `contrib/` at its own version, and each of them requires the gobit release it is tagged with.
It costs a contrib release for every gobit release, two go.mod lines at every cut, and a commit between releases that is no version of these modules.

- **Status:** Accepted
- **Date:** 2026-10-15
- **Amends:** [0127](0127-a-working-identity-ships-outside-the-module.md) and [0128](0128-passkeys-ship-in-a-module-of-their-own.md), whose modules shipped with no version an embedder could name

Measurement: [measurements/0441](../measurements/0441-what-an-embedder-could-fetch.md)

## Context

`contrib/identity-session` and `contrib/identity-passkey` are modules of their
own so that an embedder takes them by choice, and no release ever tagged them.
Each go.mod required `github.com/bdrtr/gobit v0.0.0`, a version that does not
exist, and a replace into this tree that an embedder's build ignores. A
consumer adding the session module to a project on v0.13.0 (depremtek's new
backend, whose shoppers had no sign-in) found `go get …@latest` failing on
`gobit@v0.0.0` in a module that had not pinned gobit first, and a
pseudo-version building only after it had. Nothing in either module said which
gobit release it was written for: the v0.11.0 upgrade paragraph had to tell an
embedder by hand that an older session module on the new gobit would refuse to
start (D295).

## Decision

Every release tags each module under `contrib/` at the release's own version,
`contrib/<name>/vX.Y.Z` on the cut commit, and each of them requires every
gobit module it imports at that release, built in this tree through a replace.
`TestEveryContribModuleRequiresTheRelease` holds the requirement to the newest
release the CHANGELOG names.

## Consequences

- `go get github.com/bdrtr/gobit/contrib/identity-session@vX.Y.Z` works in any module, and minimal version selection lifts the embedder's gobit to at least vX.Y.Z: the module states the release it needs instead of a paragraph saying it.
- The contrib modules move in lockstep with gobit, tagged at every release whether they changed or not; the version alone says which gobit they go with.
- A cut moves the contrib requires and tidies `examples/starter`, whose gobit the session module now lifts to the release; the gate refuses the first left undone, and the starter's own build (`updates to go.mod needed`) the second.
- A commit between releases is not a version of these modules: its go.mod names the last release, so a pseudo-version of a commit that uses a newer core API does not build outside this tree. An embedder takes tags.
- The first tag is v0.13.1; v0.13.0's commit requires v0.0.0 and stays untagged for them.
- `examples/` are not fetched by anyone and keep `v0.0.0`.

## Rejected

- Versions of their own: a second version line whose fit with gobit's would have to be written down somewhere else.
- Documenting "pin gobit first": the module still could not say which gobit it needs.
- Requiring the published release with no replace: a change to the core and to a contrib module in one commit could not build here.
- Folding the modules into the root: ADR 0127 and 0128 measured the graph that would cost every embedder.
