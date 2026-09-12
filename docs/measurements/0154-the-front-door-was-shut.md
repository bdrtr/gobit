# The front door was shut

Evidence for [ADR 0154](../adr/0154-a-project-can-be-started-from-the-binary.md).

Measured 2026-09-12, while choosing the next item from the feature list's A
section. The row was measured against the tree and the measurement was then given
to a second reader whose only instruction was to refute it; the version section
did not survive that pass, and what replaced it changed the decision.

## The row was right about the absence and undercounted the CLI

There is no `new` verb, no template tree, no generator: nothing in this repository
writes a project. `text/template` appears in two plugins and nowhere else, and the
only `os.MkdirAll` in production code belongs to the local file provider.

The operator binary is larger than the row says. Its dispatch accepts nine named
verbs — help, migrate, stuck, recover, jobs, deadletters, seed, refold-invoices,
mfa-reset — and starting the server has no verb at all.

## What did not survive: the version

The measurement reported that the newest tag "pins a library without most of this
tree". That is understated to the point of being wrong, and the correction is the
whole decision.

Measured against the real module proxy:

```
$ go list -m github.com/bdrtr/gobit@latest
github.com/bdrtr/gobit v0.8.0
$ git ls-tree --name-only v0.8.0
... cmd config deploy docs internal migrations plugins ...      # no gobit.go, no core/
```

So `require github.com/bdrtr/gobit v0.8.0` plus `import "github.com/bdrtr/gobit"`
fails at `go mod tidy` with "does not contain package". The facade landed after
that tag. `@latest` resolves to the same thing, so it fails the same way.

What the proxy DOES serve for any pushed commit is a pseudo-version:

```
$ go list -m github.com/bdrtr/gobit@9cddc77
github.com/bdrtr/gobit v0.8.1-0.20260912094500-9cddc77b3bc7
```

Note the form. With a tag reachable the proxy bumps the PATCH and prefixes the
timestamp with `-0.`; with no tag it serves `v0.0.0-<time>-<hash>`. The two are
not interchangeable — a binary writing the wrong one names a version the proxy
does not serve, and `go mod tidy` in the generated project rewrites it silently to
something the generator never chose.

So the pseudo-version is not one of four options: it is the only one that works
today, and the release is what would let the template write a semantic version.

## The end-to-end proof, run against the real proxy

Not a lane — a single measurement, because the lane below deliberately cannot do
it offline:

```
$ make build && ./bin/gobit new /tmp/relcheck
requires github.com/bdrtr/gobit v0.8.1-0.20260912094500-9cddc77b3bc7
$ cd /tmp/relcheck && go mod tidy && go build -o app . && ./app help
gobit dev — headless commerce, one binary.
```

The generated project resolved the library from the proxy, built, and printed the
operator surface. That is the claim of this slice, proven once by hand.

## Three traps in the templates, all measured

**A directory containing `go.mod` is silently removed from the embedded set.**
Built a throwaway program with `//go:embed templates` over a tree holding
`templates/proj/go.mod` plus `templates/other/file.tmpl`: the build is GREEN, the
walk reports no error, and `templates/proj` is absent. An `all:` prefix does not
lift it. With `proj` as the only subdirectory the build fails instead — so the
failure mode depends on what else is in the tree, which is the worst kind.

This also kills the row's own proposed slice, which was to embed
`examples/starter`: that directory's whole point is having a `go.mod`.

**A template ending in `.go` breaks three things at once.** Two repository gates
parse every non-test Go file in the tree, and so does `go build ./...`; a
`main.go` carrying `{{ .Module }}` is not Go.

**`.env.tmpl` would be untracked.** The repository's own `.gitignore` carries
`.env.*` with a single exception for `.env.example`, so a template named that way
is silently not committed — which has broken gates here before.

All three are closed by the file NAMES, and the mapping from template name to
written name is data.

## The gate that was hiding the omission

`TestUsageNamesEveryVerbTheDispatchAccepts` iterated a hand-written list of seven
names, of which five were verbs. Measured against the switch: `seed` and
`refold-invoices` were in no usage-text test at all. So a verb added to the
dispatch and forgotten in the help text passed every lane — on the exact surface
this slice extends (gap D90).

Its population is the switch's own case clauses now, read with go/ast. The case
expressions that are string literals are deliberately excluded: beside the help
verb they are `-h`, `-help` and `--help`, aliases of a verb the text already names.

## The mutations

| # | Mutation | Result |
|---|---|---|
| 105 | the pseudo-version loses its `-0.` suffix | **bit** (1) |
| 106 | a prerelease base tag is bumped | **bit** (1) |
| 107 | the go.mod template loses its require line | **bit** (1) |
| 108 | the generated main imports a path that does not exist | **bit** (1) |
| 109 | the generated .env names a setting gobit does not read | **bit** (1) |
| 110 | the `go` directive falls behind this module | **survived**, then bit |
| 111 | a template is embedded and listed nowhere | **bit** (init) |
| 112 | an unknown version guesses the newest tag | **bit** (1) |
| 113 | the replace path is left relative | **bit** (1) |
| 114 | a project is written into an existing directory | **bit** (1) |
| 115 | a lost template field writes `<no value>` | **bit** (2) |
| — | `new` is removed from the help text | **survived**, then bit |

Two survivals, and both are the same class: a check whose subject was not the
thing it claimed.

The help-text one is a `Contains` on a three-letter word. Removing the usage line
left the gate green because the flags section below still carried "new". The gate
asserts the usage LINE now — `<binary> <verb>` — which is what the text owes a
reader.

The go-directive one is worse because it looked derived. It generated a project
passing the root go.mod's directive IN, then asserted the generated go.mod carried
it — proving that the template writes what it is given. With the command's
constant set to "1.21" it stayed green. Its subject is that constant now, read
from the source and compared with the module's own `go` line.

Mutation 107 is worth a line for the opposite reason: the unit test caught it and
the end-to-end lane did not, because `go mod tidy` adds a missing require back.
The lane's subject is "does it build", not "what did the template write".

## What is NOT closed

- **No lane can prove the template works at the version it PINS.** Every
  out-of-tree proof in this repository rewrites the go.mod to point at the
  checkout, so the lane compiles the template against HEAD. Closing it needs a
  release whose tag contains the published surface.
- **A generated project's own help calls itself `gobit`.** The binary name is a
  constant in the composition root, so a project named `shop` tells its operator
  to run `gobit migrate status`.
- **The generated project is guest-only.** The starter's signed-in-customer
  adapter is not in the template, and the module it needs has no tag at all.
- **No Dockerfile and no CI file.** A deployment builds its own image.

## What was not measured

Whether anybody has tried to `go get` this library and given up. It would leave no
trace anywhere reachable.
