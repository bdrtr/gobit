# The release a plugin expects — measured 2026-09-28

The evidence behind [ADR 0224](../adr/0224-a-plugin-names-the-releases-it-works-with.md).

## 1. What there was

| Part | State |
|---|---|
| the plugin contract | `Name` and `Setup`; `Registry.Add` returns nothing; `Install` checks names, then runs every Setup |
| a declared version or core requirement | none in `plugins/`, `contrib/` or `examples/` |
| out-of-tree modules | `examples/plugin` (the only out-of-tree `Plugin`), `examples/starter`, `examples/storefront`, `contrib/identity-session`, `contrib/identity-passkey`; each requires gobit `v0.0.0` with a `replace` to the tree |
| the library's release | read from the build by `internal/scaffold.Stamped` for `gobit new` alone (ADR 0182) |
| what Go's `require` says | the lowest release; no upper bound, and `exclude` applies only in the main module |
| tags | v0.5.0 to v0.9.0 |

## 2. The reader

`TestTheLibraryReleaseIsReadFromTheBuild`: gobit as the main module and as a
dependency answer their release; no build information, `(devel)`, `+dirty`, a
`replace` and a build without gobit answer nothing. `gobit new`'s tests read the
same rule through `scaffold.Stamped`, which now delegates.

## 3. The registry

`TestAPluginIsInstalledOnlyWithTheReleasesItNames`: `>=v0.9.0 <v0.11.0` admits
v0.9.0, v0.10.3 and v0.11.0-rc.1 and refuses v0.11.0 and v0.8.9, and a refusal
runs no Setup, a plugin naming no range included; each comparison decides its
edge at v0.9.0. `TestARangeThatCannotBeReadIsRefusedEvenUnchecked`: an empty
range, a blank one, a bare version, a word and `~` are refused with and without
a stamped release; an unstamped build installs `>=v9.0.0` and logs that it did
not check it.

## 4. On the production path

`TestTheCompositionRootRefusesAPluginsUnreadableRange`: a plugin the embedding
program hands to `installPlugins` with the range "0.9 or later" stops the
start before its Setup. The out-of-tree example compiles against the published
surface with its range once its `go.sum` gained `golang.org/x/mod`.

## 5. Mutations

| # | Mutation | Killed by |
|---|---|---|
| V1 | no check at install | the registry tests, the composition root test |
| V2 | an unstamped build checked | the unreadable range test |
| V3–V7 | each comparison off by its edge | the release test |
| V8 | one-character operators read first | the release and unreadable range tests |
| V9 | no `v` added | the release test |
| V10 | an empty range taken | the unreadable range test |
| V11 | any bound enough | the release test |
| V12 | an unknown comparison taken | the unreadable range test |
| V13 | a `replace` trusted | the reader test |
| V14 | build metadata trusted | the reader test |
| V15 | the dependency not looked up | the reader test |
| V16 | `gobit new` reading no stamp | the `gobit new` test |

Sixteen mutants, all killed on the first run.
