# A volume ask can write a `Sink` that moved to another machine

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

`settleSinkStatus` in `audio-operator/endpointreads.go` takes a
`compose` function, whose contract is to return the status to write,
and false when another machine owns the resource. After a 409 or a
404, `patchSinkStatus` in `statuspatch.go` reads the `Sink` again and
calls `compose` on the new copy, so a `Sink` that another machine took
in the meantime is not written. The sweep in `sweep.go` follows the
contract: its `compose` returns `published.Node == e.machine`.

`reportAsk` in `asks.go` writes the level of a volume ask into
`status.observed`, and its `compose` always returns true. When a
Bluetooth speaker connects to another machine between the read of the
`Sink` in `applyAsks` and the write, the write fails with a 409, the
new copy names the other machine in `status.node`, and `reportAsk`
writes this machine's level into the other machine's status. The
compose of `reconcileSink` in `sinkcontrol.go` also always returns
true. That pass read the endpoint on this machine, so the case may
not apply to it, but it does not follow the contract either.

## The reproduction

The test ran a first pass of `liken-1` over the fake API server, then
changed the speaker's `Sink` in the fake so that `status.node` names
`liken-2`, and gave the `Sink` a new `resourceVersion`. It then called
`reportAsk` with the copy that `liken-1` read before the change, so
the first patch got a 409 and `settleSinkStatus` read the `Sink`
again. It asserted that the stored `Sink` reports no volume from
`liken-1`, and it failed with:

    the Sink that liken-2 owns reports the volume 40 that liken-1 wrote, want no write from liken-1

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When a speaker moves to another machine during a volume request,
which machine reports the volume in its `Sink`? Today the old machine
can write its level into the new machine's status. One option checks
`status.node` in each `compose` function: in `reportAsk` that is one
line, `published.Node == e.machine`, as the sweep has. Another checks
it once in `settleSinkStatus` for every caller. A check in `settleSinkStatus` keeps
the contract in one place, but the reconcile pass sets `status.node`,
so it must still be able to write a `Sink` that names no machine or
another one.
