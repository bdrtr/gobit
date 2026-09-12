# What a killed consumer leaves behind — measured 2026-09-12

D105 said the Redis bus cannot reclaim a stranded message. This is the probe
that showed what "stranded" means here, the design the measurement forced, and
the mutation that survived the first round of gates.

## 1. The defect, run rather than read

A bus consumed with the name `consumer-that-dies` and entered a handler that
never returned — the state a process killed mid-dispatch leaves. A second bus
then came up under a different name, which is what a restart produces
(`<hostname>-<pid>`).

```
PENDING       id=1789243109057-0 consumer=consumer-that-dies idle=0s      retries=1
MEASUREMENT: after 10s the fresh consumer received NOTHING; the message is stranded
STILL PENDING id=1789243109057-0 consumer=consumer-that-dies idle=10.001s retries=1
```

The entry does not expire, no other consumer is offered it, and the idle time is
the only thing that moves. The stream reports the message as delivered.

## 2. What the tree already guaranteed, and what it only claimed

The handlers were ready for this before the bus was. Twenty-five files across
the modules, the workflows and the plugins discuss a redelivered event, and the
ones that say why it is harmless give the same reason: the handler computes a
TARGET rather than a difference, so a second delivery finds the target already
met. That rule is what makes a takeover safe to build — the decision here is
about delivery, not about correctness under it.

The published sentence was the opposite kind. `core/eventbus`'s package comment
has said "delivers at least once and resumes where it left off when the process
restarts" since the backend was written, and the gate under it
(`TestRedisIntegrationResumesAfterRestart`) restarts with the SAME explicit
consumer name after a CLEAN shutdown — the one case in which nothing can be
lost. The sentence was broader than the gate, and the gap between them is
exactly the defect.

## 3. Why XPENDING before XCLAIM

XAUTOCLAIM does the same work in one round trip and does not report how many
times a message has been delivered. That count is the only bound on a message
that kills the process reading it, so the sweep pays one extra command:

| | XAUTOCLAIM | XPENDING + XCLAIM |
|---|---|---|
| round trips per sweep | 1 | 2 (1 when nothing is idle) |
| delivery count available | no | yes (`RetryCount`) |
| dangling entries cleaned | yes | yes (XCLAIM, Redis 7) |

## 4. The six gates and what each is the subject of

| Gate | Subject |
|---|---|
| the sweep asks only for messages idle beyond the threshold | the request that finds candidates |
| a message a dead consumer was holding is delivered | the takeover itself, and the ACK after it |
| this consumer's own pending messages are left alone | the process not racing itself |
| a message that emptied every consumer is dropped | the bound on the poison pill |
| the claim asks Redis to check the idle time again | the race with an owner that just finished |
| a message inside the threshold is not even looked at | the filter, through the drop it would otherwise reach |

The last one exists because of a surviving mutation, below.

## 5. Mutations

| Mutation | What failed |
|---|---|
| the own-name skip removed | the process claimed what it was already holding |
| the delivery bound raised out of reach | a message that emptied three consumers was handed to a fourth |
| the sweep never called | the end-to-end takeover, against a real Redis |
| XCLAIM's second idle check removed | the claim became non-atomic |
| XPENDING's idle filter removed | **survived the first round** |

The filter's removal survived because XCLAIM still carries the same threshold:
Redis refuses the claim, so the takeover stays correct and every gate stayed
green. The two guards are redundant BY CONSTRUCTION, which is the same shape
ADR 0161's third mutation had.

They are not redundant for the DROP. Without the filter the sweep sees entries
that are not idle at all, and one of them can meet the delivery bound — a
message a live consumer has been handed three times is then acknowledged out
from under it, and the work that consumer is doing right now is thrown away with
it. The sixth gate is written on that consequence, and the mutation bites it.

The fake client had to change for the gate to be writable: it applied no IDLE
filter, so a scenario about an entry INSIDE the threshold could not be expressed
against it. A fake that answers the same whatever it is asked cannot test the
asking.

## 6. What this did not do

The consumer ENTRY itself is not removed. A group accumulates one dead consumer
name per restart, and `XGROUP DELCONSUMER` is not called; the entries are small
and hold nothing once their messages are taken over, but they are unbounded over
a long enough life.

Nothing here reports a dropped message anywhere a machine can read. The log line
is the dead letter, and the bus still has no queue to put one in.
