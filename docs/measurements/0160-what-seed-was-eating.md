# What seed was eating — measured 2026-09-12

A command that opens the application joins the event bus. On Redis that is not a
declaration; it is a share of the traffic.

## 1. How it was found

Not by reading the bus. By measuring where an MCP server could be mounted
(A8.16): one reader answered "in-process, in the SERVING process, because an MCP
that assembles its own installation is not read-only — `Register` subscribes, and
a second member of the consumer group takes the server's messages."

That sentence is about MCP and it is true of every verb the binary already ships.

## 2. The chain, link by link

Each link was read rather than assumed:

1. Seven call sites open the application. Five are verbs — `seed`, `recover`,
   `jobs`, `refold-invoices`, `mfa-reset`; the other two are the server and the
   facade's in-process harness.
2. `openApplication` builds the real bus: it calls `setupEventBus`, which returns
   a Redis Streams bus when `EVENT_BUS=redis` and an in-memory one otherwise.
3. It then calls `registerModules`, which reaches each module's `Register`.
4. `Register` subscribes. The order module's subscription sits inside its
   `Register`, and so does the notification module's.
5. The Redis bus's `Subscribe` is not a registration. It calls `ensureGroup` and
   then starts a goroutine: `go b.consume(eventName)`.
6. The group is the point. The bus's own godoc: "Consumers joined to the same
   group receive a message only ONCE; that is how scaling is done."

So a verb is a consumer in the server's group, for as long as it runs.

## 3. What that costs, in the three ways it costs

**The server does not get the message.** Not delayed — gone. The outbox row that
produced it was marked published when the relay handed it over, so nothing
re-delivers.

**The command RUNS the handler.** The subscriber is registered in the same
`Register` that subscribed. `gobit seed` therefore sends order confirmations:
the notification module's `OrderPlaced` is bound to the topic in the process
that was asked to rebuild a catalog.

**What it did not acknowledge is stranded.** The consumer name defaults to
`<hostname>-<pid>`, so every run is a new consumer that never returns. A message
delivered and not acknowledged stays in that consumer's pending list, and
nothing takes it back:

```
$ grep -rn "XAutoClaim\|XClaim\|XPending\|reclaim" --include='*.go' core/eventbus/ \
    | grep -v _test
(nothing)
```

The bus has no reclaim, no pending sweep and no dead-consumer detection.

## 4. How reachable it is

Two conditions, both ordinary: `EVENT_BUS=redis`, and an operator running a
verb. The default is the in-memory bus, so most installations were never
exposed — but Redis is the scaling path this repository added deliberately
(ADR 0009's second carrier), and `seed` on the measured rig runs for minutes.

## 5. The fix, and what it deliberately leaves alone

The assembly takes an event role. The two paths that answer requests consume;
the five verbs get a wrapper that publishes to the real bus and subscribes to
nothing.

Publishing is untouched ON PURPOSE. A command that writes through a service
writes an outbox row in the same transaction and publishes directly after the
commit; handing it an in-memory bus would have dropped that direct half
silently. The outbox would still have carried the event — which is a repair, not
a reason.

`Subscribe` succeeding and doing nothing is a shape this repository refuses
everywhere else. Refusing would fail `Register`, and a module that cannot be
registered is a verb that cannot run. What makes it honest is that the wrapper
says so once, in the log, with the effect spelled out.

## 6. The gates, and which one is the population

Three prove the wrapper: a command reaches no `Subscribe`, a command's `Publish`
still arrives, and the server's `Subscribe` still reaches the bus. The third is
what stops a wrapper that refuses everything from passing the first two.

The fourth is the one that matters, and it audits the CALL SITES: it reads the
composition root and requires every `openApplication` call to name a role,
refusing any consuming site that is not one of the two that serve requests. A
verb added next year is written by copying a neighbour, and every neighbour is a
command.

## 7. Mutations

| Mutation | What failed |
|---|---|
| `seed` given the consuming role | the call-site population check, on both halves: a consuming site that does not serve, and one fewer command than there are verbs |
| the wrapper's `Subscribe` delegating to the inner bus | "a command process reached the real bus's Subscribe" |
| the role ignored, so the SERVER gets the wrapper too | "the server did not reach the real bus's Subscribe; every subscriber in the installation would be silent" |
| the wrapper's `Publish` swallowing the event | "the direct half of the house pattern was dropped" |

## 8. What this did not close

The pending list. A message stranded by a command that ran before this change is
still in Redis under a consumer that will not come back, and nothing in the tree
reclaims it. That is the bus's gap rather than the composition root's, and it is
recorded rather than fixed here.

And the in-process harness still consumes. Its bus is the in-memory one by
default and a test wants its subscribers to fire; pointed at a Redis
installation it joins the group like any other member, which is now written down
in the known limits.
