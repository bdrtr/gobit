# What a rotation has to not break — measured 2026-09-11

Serves [ADR 0129](../adr/0129-a-signing-key-rotates-without-logging-anybody-out.md).

## The state before

`contrib/identity-session` signed and verified with ONE key. Changing it was a
deploy that made every cookie in every browser stop verifying at the same
instant, so rotating a key cost every shopper their session — which is the reason
keys do not get rotated, not a reason they should not be.

The module's own package doc said so in a sentence: *"It does not rotate the
signing secret."* That sentence is what this record answers, and it was written
down rather than left implicit for exactly this reason — a named limitation is
findable.

## What the rotation has to hold

Four properties, and each is a way the feature can look done and not be.

| Property | How it fails silently |
|---|---|
| a cookie from the old key still verifies | nothing changes; the rotation was pointless |
| a cookie minted after it is signed by the NEW key | everything verifies and nothing rotated |
| a key removed from the list stops verifying | the only remedy for a leak does not work |
| a sealed value follows the same keys | two answers to "which keys does this installation accept" |

The second is the one that looks exactly like success: an implementation that
accepted both keys and went on SIGNING with the old one would pass every test
about sessions still working, and would have rotated nothing.

The fourth costs little on its own — a ceremony token expires in two minutes —
but a verifier rotating one path and not the other would be found by somebody's
failed passkey sign-in rather than by a test.

## What is refused at startup

A retired key held to the same length floor as the current one, because a
retired key still ACCEPTS everything it signed and the likeliest way a short one
gets in is an operator putting a placeholder in the list while they work the
rotation out.

And a "rotation" that lists the current key as retired, which reads as a rotation
and is not one: every cookie still verifies, nothing changed, and the operator
believes the old key is out of service.

## Where the timing argument ends

The verify loop stops at the first key that matches, which is a timing signal
about WHICH of an installation's own keys signed a cookie the caller already
holds. That is nothing they can use, and the comparison against each key is still
constant time — which is the part that matters, because that one is about the
MAC's bytes rather than about which key produced them.

## The mutations

| Mutation | What fell |
|---|---|
| retired keys are not tried | the rotation test and the sealed-value test |
| signing uses the retired key | the signs-with-the-new-key test |
| a retired key's length is not checked | the floor test |
| the current key may be listed as retired | the not-a-rotation test |

Four, each on its own case, all with `-count=1`.

## The gate that could not see contrib, and what it was hiding

Writing this record named two symbols in `contrib/identity-session` and the
documentation gate refused both: it resolves an unqualified type-and-member name against the
types of THIS module, found several `Options` and none with the field.

Qualifying them with the package name made the gate pass — and a deliberately
wrong field passed too. So the qualification had bought silence rather than
verification. The contrib trees are separate Go modules, so `go/build` cannot
reach them through this module's package resolution, while their import paths
still begin with this module's — so the audit called them in-repo, found no such
package, and said nothing.

The gate's package walk now includes the two contrib trees. It is their own list
rather than `productionTrees`, because that list is what every other audit in the
package narrows to and those audits are about gobit's own module; this one is
about whether a sentence points at something real.

It paid on the first run: `contrib/identity-session/password.go` had a godoc
link to `[Store.Credential]` and the type is called `Credentials`. That link
shipped two records ago and nothing could see it.
