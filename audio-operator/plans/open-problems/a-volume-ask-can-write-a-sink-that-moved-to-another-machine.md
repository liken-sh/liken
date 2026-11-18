# A volume ask can write a `Sink` that moved to another machine

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

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

## What would confirm it

A unit test of `reportAsk` with a fake API server that answers the
first patch with 409, and returns a `Sink` whose `status.node` names
another machine on the read that follows. The test reads whether a
second patch is sent.

## What a fix has to decide

Whether every `compose` checks `status.node`, or `settleSinkStatus`
checks it once for every caller. A check in `settleSinkStatus` keeps
the contract in one place, but the reconcile pass sets `status.node`,
so it must still be able to write a `Sink` that names no machine or
another one.
