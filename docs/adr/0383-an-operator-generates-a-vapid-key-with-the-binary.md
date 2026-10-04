# ADR 0383 — An operator generates a VAPID key with the binary

**Summary:** `gobit webpush-key` prints a new web-push signing key pair: the
private half as the setting line, the public half to check the plugin against.

- **Status:** Accepted
- **Date:** 2026-10-04

## Context

The web-push plugin refuses to start without `WEBPUSH_VAPID_PRIVATE_KEY`, the
raw 32-byte P-256 scalar in base64url, which is not what openssl prints. Its
error for a malformed key sent the operator to "the command in .env.example",
and `.env.example` said that command did not exist (D233); its other key errors
named no way to make one. `webpush.GenerateKey` produced the value the plugin
accepts, and nothing outside the plugin's own tests called it.

## Decision

The binary answers `webpush-key` by printing a pair from `webpush.GenerateKey`:
the line `WEBPUSH_VAPID_PRIVATE_KEY=<base64url>`, and the public half as a
comment to check the running plugin against. It reads no configuration, reaches
no database and no network, takes no argument, and every error the plugin gives
for a missing or malformed key names it.

## Consequences

- An operator sets the plugin up with the one tool every installation already
  has, before any setting exists; a program that embeds gobit answers the same
  command under its own name, because it dispatches through gobit's `Main`.
- Every line of the output other than the setting is a comment, so it can be
  appended to a `.env` as printed.
- The public half printed is the value the plugin logs at startup and
  `GET /store/v1/webpush/vapid-key` serves; the storefront takes it from that
  route, never from this output, so a replaced key cannot leave a storefront
  subscribing under the old one.
- The command prints a secret to standard output, so it lands wherever the
  operator sends that output: a terminal's scrollback, a pipe, a CI log.
- The command's name is spelled twice, as the plugin's `webpush.KeyCommand` for
  its error and as the root's dispatch constant, because the plugin cannot
  import the root; a test reads the command out of the plugin's error and runs
  it through the dispatch.
- Generating a key is not rotating one. Replacing a running installation's key
  still ends every subscription issued under the old one, and nothing in the
  command stops an operator doing it.
- The plugin's errors name the program `gobit`, since a plugin does not know
  what the embedding program calls itself (ADR 0254).
- The plugin is no longer removable with `rm -rf plugins/webpush` plus its
  catalog line (ADR 0018): the command's file, its test, its dispatch case and
  its usage line go with it.
- `new` is no longer the only verb that reads no configuration (ADR 0154).

## Rejected

- An openssl recipe in `.env.example`: it prints a PEM block, and turning that
  into a raw base64url scalar is the conversion the plugin's format exists to
  spare.
- Generating a key at startup when none is set: a key the server invents and
  forgets on restart ends every subscription at every deploy.
- A separate key-generation binary: a second program to build and ship for one
  function the main binary already links.
- Writing the key into `.env` itself: the command would need to know where the
  configuration lives, which is the one thing it is built not to read.
