# A dropped uevent is not counted for a hotplug hold

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

A hotplug hold keeps a connector that reads disconnected in the slice
for `hotplugSettle`, and starts the hold again each time a new drm
uevent names the connector, up to `hotplugCeiling`. The hold reads the
count from `hotplugCounts`, which `countHotplugs` in `hotplughold.go`
updates as each event passes from the uevent reader to the reconcile
loop.

The reader in `uevents.go` sends each event with `sendDRMEvent`, which
drops the event when the channel of 16 events is full. The comment
gives the reason: the events already queued wake a pass that reads
all of sysfs. But the count runs after the drop, so a dropped event is
never counted. When the dropped event named connector 403 and the
queued ones name other connectors, a hold on 403 does not start again,
and it can end while the receiver still pulses the line.

## Evidence

- `uevents.go`, `sendDRMEvent`: `select { case events <- event:
  default: }`.
- `hotplughold.go`, `countHotplugs`: `counts.add(event)` runs on each
  event that it receives from that channel.
- A report of lost datagrams from the socket counts for every
  connector (`lostEventsAction`), but a drop at the channel produces
  no such report.

## The reproduction

In a `synctest` bubble, the test made a channel with room for one
event and sent two events through `sendDRMEvent`: one that names
connector 100, which filled the channel, and one that names connector
403, which `sendDRMEvent` dropped. It then started `countHotplugs` on
the channel, read the event that passed, and asserted that the count
of connector 403 is at least 1. It failed with:

```
the count of connector 403 is 0 after its event was dropped at a full channel, want at least 1
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When a burst of uevents overflows the reader's queue, should the
reader count each event before it can drop it, or should a drop count
as a lost event that extends the hold of every connector?

The count can move in front of the channel, into the reader, so every
event is counted before a drop. Or a drop at the channel can count as
a lost event, which counts for every connector and keeps every hold
going.
