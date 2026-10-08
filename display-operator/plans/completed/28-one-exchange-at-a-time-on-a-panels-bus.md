# One exchange at a time on a panel's bus

Plan 28. Built and tested on 2026-10-08. The drill on stick-1 is still
owed.

The `boe-1080-display` panel on stick-1 answered brightness reads
with the replies to other controls, and a claim's prepare failed on
it. The panel was not at fault. The operator ran two DDC/CI exchanges
on its bus at once.

## What the logs showed

The drills of plan 27 logged two crossed replies on 2026-10-08:

- At 17:41:41.237 a claim's prepare asked for brightness (0x10) and
  read the reply to contrast (0x12). At 17:41:41.436 the `Display`
  pass's read of contrast found no reply at all.
- At 17:46:21.306 a prepare asked for brightness and read the reply to
  sharpness (0x87). At 17:46:21.565 the `Display` pass asked for
  contrast and read the reply to brightness.

A panel holds one reply at a time. An exchange writes a request,
waits at least 40 ms, and reads the reply, so the second request of
two that overlap replaces the first one's reply.

## The fault

Every caller opened the bus through `busFor`, and nothing made two
callers wait for each other. The claim's prepare runs on the kubelet's
gRPC call, the `Display` pass and its poll run on their own loop, the
probe runs on whichever pass misses the cache, and a restore runs on
a goroutine of its own. Each paced its own exchanges by the
specification and none paced against the others.

## What changed

`busFor` hands each bus to one caller at a time, and the bus gives the
turn back when the caller closes it (`busturn.go`). The next caller
also waits out 50 ms after the last close, the gap the specification
asks for before the next message. Every caller already opened the bus
through `busFor` and closed it after one exchange, so no caller
changed.

The turn exposed a deadlock that could not happen before it.
`factsFor` held the cache's lock while the probe read the panel, and
the poll records each answer in the cache while it holds the bus. A
probe that waited for the bus while it held the cache would wait on
the poll forever. `factsFor` now releases the cache while it probes,
and a second pass that misses the cache during a probe waits for that
probe and reads its answer, so two passes still read the panel once.

The turn is a channel and not a mutex. `testing/synctest` advances its
fake clock only while every goroutine waits on a channel or a timer,
and the tests of the turn run the protocol's own delays on that clock.

## What stays open

A pod that holds a panel's control device reads and writes the bus
from its own process, outside these turns. The manual already says
that such a pod owns the panel, and that no `Display` spec and no
claim parameter writes it.

Right after a compositor restart, the stick-1 panel sometimes answers
no read at all for a moment. That is a panel that is busy after a
mode change, not a crossed reply, and the drill of this plan checks
whether it remains.
