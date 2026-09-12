# What the lanes were handing tests — measured 2026-09-12

D107 was one test reaching a database nobody started. This is what the lanes
hand every other test, what breaks when that stops, and the mutation that proves
the repair is the lane rather than the test.

## 1. What is listening, and what the configuration points at

On the machine where D107 passed:

```
LISTEN  127.0.0.1:6379
LISTEN  127.0.0.1:5432
```

```go
DatabaseURL string `env:"DATABASE_URL" envDefault:"postgres://gobit:gobit@localhost:5432/gobit?sslmode=disable"`
RedisURL    string `env:"REDIS_URL"    envDefault:"redis://:gobit@localhost:6379/0"`
```

The default is not a placeholder. It names the address a developer's own
services answer on, which is what turns a forgotten fixture into a pass.

## 2. Who could have made the same mistake

| | |
|---|---|
| test files calling `config.Load()` | 4 |
| of those, connecting to a database | 2 (`jobs`, `mcp`) |
| test files setting `DATABASE_URL` themselves | 7 |
| test files setting `REDIS_URL` themselves | 1 |

The population is small and it is not the point: nothing stopped the fifth file
from being written the same way, and the fourth already had been.

## 3. What breaks when the lanes stop offering them

The whole integration lane and the whole smoke lane were run with both variables
pointed at 127.0.0.1:1, where nothing listens:

| Lane | Result |
|---|---|
| `go test -race -tags=integration ./...` | 127 packages ok, 0 test failures from the change |
| `go test -tags=smoke ./internal/smoke/` | ok, 45.6s |

Zero occurrences of `connection refused` or `db_unreachable` in either run: not
one test so much as TRIED the ambient address. Every scenario in the tree starts
what it needs.

(The integration run reported one failure, `TestNoTurkishOutsideLedger`, on the
untracked feature list that lives in this working tree — the file was not moved
aside for the experiment. It is unrelated to the environment and is named here
because a measurement that quietly drops a red is worth nothing.)

## 4. The precedent this repeats

`internal/smoke/process_test.go` builds each server process's environment from
scratch and says why:

> The environment of whoever runs the test is NOT INHERITED, and this is
> mandatory for the correctness of the scenarios: cmd/server reads the plugin
> settings from os.Environ(), so a STRIPE_API_KEY sitting in the developer's
> shell would silently pass the "no key" scenario. The same holds for
> DATABASE_URL and PLUGINS.

The rule was already right, already written down, and applied to ONE lane —
the one whose processes it could see. The class is the familiar one: a rule kept
in prose while the population it should govern grows.

## 5. Mutations

| Mutation | What failed |
|---|---|
| the prefix removed from the integration recipe | the lane audit: that lane hands tests the ambient services |
| the value set to the configuration's own default | the lane audit: the value must DIFFER from what a forgotten test reaches |
| the setting moved into a recipe COMMENT | the lane audit: it reads the commands, not the prose around them |
| D107 put back (the test's own installation removed) under the lane's environment | `db_unreachable ... target: 127.0.0.1:1/gobit` — the runner's own failure, locally |

The last one is the decision's proof rather than the gate's. The first three say
the Makefile still carries the rule; only the fourth says the rule does the work
it was written for.

## 6. What this did not do

It does not bind a developer who runs `go test` by hand, and it cannot: the
environment belongs to whoever starts the process. It also says nothing about a
test that starts a container and then reads some OTHER setting from the shell —
a plugin key, a feature flag. Smoke's helper refuses those for the processes it
starts; the lanes do not.
