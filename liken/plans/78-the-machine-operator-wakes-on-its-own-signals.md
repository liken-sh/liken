# 78. The machine operator wakes on its own signals

Milestone 78. Proposed 2026-10-09. The third of five milestones that
remove the ten-second ticker from `machine-operator`'s reconcile loop.
[Milestone 76](completed/76-a-pass-reports-what-it-did-not-finish.md) gives the
series and the table of every job the ticker does. This milestone
wires three signals that `machine-operator` already has, or nearly
has, into the loop: a release download that ends, a watch that works
again after it failed, and a change to the node's `ResourceSlice` that
the operator did not make.

## What happens now

**The release download.** The fetcher in
[`fetch.go`](../machine-operator/fetch.go) downloads a release in its
own goroutine. When `run` ends, it records `Verified` or `Failed`, or,
for a download that a new ask replaced, records nothing, and nothing
wakes the loop. The next tick reads the result, so a verified release
reaches status and the staging step up to ten seconds late. A
replacement download starts only on the first pass after the old
goroutine returns, which is also a tick.

**A watch that works again.** Each watch in
[`watches.go`](../machine-operator/watches.go) wakes the loop once,
after its first sync. The informer calls `Reopened` each time the API
server accepts a watch after the first
(`kubernetes/informer/informer.go`), and the operator only counts it
in a metric. The API server ends every watch on its own schedule,
every five to ten minutes with client-go's timeouts, so most reopens
follow no failure. After a real outage, a pass whose writes failed
runs again only on the next tick, unless an object changed.

**The `ResourceSlice`.** Milestone 77 gave the slice watch a handler
that wakes the loop on a delete and on nothing else, because a pass
that only the ticker woke no longer walks sysfs. The kubelet deletes a
driver's slices when it starts and after the plugin unregisters, so a
restart of k3s deletes this node's slice, and the delete's pass writes
it again. An update that another writer makes wakes nothing, and the
next pass that something else wakes writes over it.

## The design

**The fetcher wakes on every exit.** `run` calls the loop's wake
function on every return, after it clears `busy` under the fetcher's
lock. A `defer` placed after the unlock covers the early return of a
replaced download too. The pass that the wake starts reads the result,
or starts the new download. A `Failed` result is safe to wake on,
because milestone 76's backoff gives the next attempt its time. Without
the backoff, this wake would restart a download that fails in
milliseconds, such as a `404`, with no delay between attempts.

**A `Recovered` callback.** The informer gains a `Recovered` option,
called when the API server accepts a watch after a watch on the same
collection failed: the moment the collection's `watching` flag goes
from false to true. `Reopened` stays as the metric, because a wake on
every routine reopen would run dozens of passes an hour on a settled
machine. `Recovered` wakes the loop.

The informer marks a copy ready the moment the API server accepts the
resumed watch, before the reflector delivers the events missed during
the outage. A pass that the recovery starts could read a copy older
than the outage. So the pass that a `Recovered` wake starts reads that
kind from the API server, the same path the reads take when a copy is
not ready.

**The slice handler.** A handler that woke on every change would turn
a write that never converges into a loop of writes. That can happen:
`WriteResourceSlice` compares the devices with `reflect.DeepEqual`,
and an API server with `DRAConsumableCapacity` off drops
`allowMultipleAllocations` on write, so the slice never compares
equal. Today that costs one write every ten seconds. With a handler
that wakes on its own echo, it costs a write as fast as the API server
answers. The upstream DRA slice controller met the same loop
(`k8s.io/dynamic-resource-allocation/resourceslice`), and it copies
the fields the server dropped back into the desired state before it
compares.

So the handler also wakes on an update whose `resourceVersion` is not
the version the operator's own last write returned.
`WriteResourceSlice` records that version from the server's answer,
and it compares the desired devices against the devices the server
returned, so a dropped field does not count as a difference.

## Tests

In a `synctest` bubble with `kubernetes/apiservertest`:

- A download that finishes against a fake release server wakes a
  pass, and that pass stages the release with no tick.
- A download that a new ask replaces wakes a pass that starts the new
  one.
- A watch that the fake API server refuses and then accepts wakes a
  pass, and that pass reads the kind from the server. A routine reopen
  wakes nothing.
- An update to the `ResourceSlice` that another writer makes is
  overwritten with no tick.
- A fake API server that drops `allowMultipleAllocations` on write
  gets one write, not a loop of writes.

## Verification needed

- On `liken-1`, confirm that the slice's `resourceVersion` stays the
  same for ten minutes on a settled node.
- Restart k3s on a node, and confirm the slice is back within seconds.
