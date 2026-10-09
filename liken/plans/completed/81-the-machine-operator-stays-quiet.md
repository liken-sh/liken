# 81. The machine operator stays quiet

Milestone 81. Proposed and built 2026-10-09, and released in
2026.10.09-003. One finding stays open: on wifi, single SNTP samples
move the offset past 25 ms several times an hour
([What the testbed showed](#what-the-testbed-showed)). It follows milestones 76 to 80,
which removed the ten-second ticker from `machine-operator`'s reconcile
loop. On the testbed, an idle machine went from about 420 passes an
hour to between 34 and 50
([milestone 80](80-the-machine-operator-idles.md#what-the-lab-measured)).
Most of the passes that are left come from two sources: facts that
`init` republishes, and the echo of the operator's own status write.
This milestone removes the passes that change nothing.

## What happens now

The testbed's second pass, on release 2026.10.09-002, counted the
causes of each pass in 30 idle minutes with
`liken_machine_passes_total`:

| Cause | `liken-1` | `stick-1` |
| --- | --- | --- |
| `facts` | 10 | 8 |
| `watch` | 5 | 4 |
| `machine event` | 7 | 0 |
| `backstop` | 3 | 5 |

**The facts.** `init` measures the clock every 64 seconds and
republishes the time facts when the state, the source, or the stratum
changes, when the offset moved 25 ms or more since the last publish,
or 10 minutes after the last publish (`worthRepublishing` in
`init/time.go`). Each republish wakes a pass, and the pass writes the
Machine's status, because `status.time` holds `lastSync` and `offset`.
`stick-1` joins its network over wifi, and its offset moved from 11 ms
to 72 ms between two polls, so the 25 ms threshold does not damp it.
`liken-1` is wired, and its offset stays near 3 ms, so its 10 `facts`
passes may come from other facts. Nobody has measured which ones.

**The echo.** The Machine watch wakes the loop on every change
(`WakeOnChange` in `watches.go`), because the conductor's grant and
the sweep's `Lost` verdict are status writes the pass must act on. The
operator's own status write comes back through the same watch, and
wakes one more pass, which finds nothing to do. So each status write
costs two passes. Most of the `watch` passes above are that echo.

**The machine events.** `liken-1` ran 7 passes for uevents in 30 idle
minutes, and `stick-1` none. Nobody has looked at which devices sent
them.

## The design

**Measure first.** Log which fact paths each facts wake carried, and
which uevents woke a pass, at the debug level or in a counter by fact
directory and by subsystem. One idle hour on each testbed machine
names the sources. The design below assumes the time facts and the
echo dominate, and the measurement can change that.

**The time facts.** Two choices, and the measurement picks one:

- Keep `status.time` and damp the offset harder: publish on a state,
  source, or stratum change, on an offset beyond a fixed bound that
  matters to the cluster (such as 100 ms, well inside etcd's and TLS's
  tolerance), and at the 10-minute floor. The status then reports a
  clock that is good enough, not each measurement.
- Move `lastSync` and `offset` out of status into metrics, and keep
  only the state, the source, and the stratum in status. No condition
  and not the phase reads the time status, and a person reads
  `status.time.state` to see `Synchronized`. The offset is a reading,
  and the rule for conditions and Events says a reading does not belong
  in status. `kubectl get machines` shows the offset today, so the
  printer column changes too.

**The echo.** The operator remembers the `resourceVersion` of its last
status write, the way `kubernetes.SliceWriter` remembers its last slice
write, and the Machine watch ignores an update at that version. Every
other update still wakes the loop, so a grant or a verdict that lands
after the write is a new version and wakes a pass.

**The machine events.** Decided by the measurement. An event that
changes nothing the inventory reads, such as a power-supply property,
can be filtered by subsystem in `hardware.InventoryEvent`.

## What the testbed measured

On 2026-10-09, one debug pod on each testbed machine ran for 30 idle
minutes on release 2026.10.09-002. It logged each facts file whose
modification time changed, and each kernel uevent from the netlink
socket, with its action, subsystem, and path.

| Source | `liken-1` | `stick-1` |
| --- | --- | --- |
| Time facts republished | 3 | 3 |
| Other facts rewritten | 0 | 0 |
| uevents under `/kernel/sunrpc/` | 34 | 0 |
| uevents of the `nfs` subsystem | 2 | 0 |
| uevents under `/devices/virtual/` | 24 | 0 |
| uevents of real devices | 0 | 9 |

Every time publish was the 10-minute floor. The offset never moved
25 ms, on the wired machine or on the wifi one, so damping the offset
harder would change nothing. `liken-1`'s uevents came in two bursts
at 20:40, when a pod with NFS volumes started: the NFS client adds and
removes RPC clients under `/kernel/sunrpc/`, and the pod's veth pair
and queues announce themselves under `/devices/virtual/net/`.
`hardware.InventoryEvent` already dropped `/devices/virtual/` except
its `misc` class, but it kept every path outside `/devices/`, so the
`sunrpc` events woke the loop. `stick-1`'s nine events were a
Bluetooth game controller that disconnected, a real change.

## What was built

* **The echo.** `kubernetes.OwnWrite` remembers the `resourceVersion`
  of the operator's last status write, and
  `watch.WakeOnAnotherWritersChange` ignores an update at that
  version. The write holds the memory until the API server's answer
  arrives, because the watch can deliver the echo before the answer.
  The slice writer had the same race: its watch test failed 2 runs in
  100. Both now share `OwnWrite`.
* **The time facts.** The measurement picked neither design above.
  The floor was the source, and the floor exists only for `lastSync`.
  So the facts drop `lastSync`, and `init` publishes on a change of
  state, source, or stratum, or an offset 25 ms from the published
  one. `Synchronized` already means a good measurement within three
  polls, because `init` reports `Unsynchronized` three polls after the
  sources stop answering. `status.time.offset` stays, with the
  printer column, and its description says it is within 25 ms of the
  measured offset. The CRD keeps `lastSync`, because a machine that
  still runs an older release writes it, and a schema that dropped it
  would prune each of that machine's writes and make it write again on
  every pass.
* **The machine events.** `hardware.InventoryEvent` keeps only paths
  under `/devices/`.

## What the dev-cluster drill showed

On 2026-10-09, four guests (`node-1` to `node-3` and `node-5`) ran
commit af8e94aa.

* **The echo.** In the 20 minutes after boot, `node-5` ran 26 `facts`
  passes, each of which wrote its status, and no `watch` pass.
* **The time facts.** `node-5` stepped its clock 857 ms at boot and
  then measured −528 ms against another leader. The slew closes about
  30 ms in each 64-second poll, so each poll moved the offset 25 ms or
  more, and `init` published each one for about 15 minutes. These are
  real changes, and they stopped: in the next 15 idle minutes no guest
  ran a `facts` pass.
* **Ten idle minutes**, after the clocks settled: no `facts` pass on
  any guest, one `watch` pass on each leader and none on `node-5`, and
  about two `backstop` passes on each, with no repair. That is about
  18 passes an hour on a leader and 12 on the follower. The leaders'
  `watch` passes, about 6 an hour, have no known source yet: the
  follower has none, so a leader's own objects change on a cycle.

## What the testbed showed

Release 2026.10.09-003 reached both testbed machines at 22:24 UTC on
2026-10-09. A drill then wrote `three` into `stick-1`'s `hardware/cpus`
fact for 15 seconds. `FactsPublished` went `False` with the new
message, and the slice did not change, because no device on `stick-1`
carries a disk in its subtree: its system stick is a storage role and
was never offered. No CDI spec named the withheld node.

The idle hour from 22:25 to 23:25, beside the hour before the rollout:

| | `liken-1` on -002 | `liken-1` on -003 | `stick-1` on -002 | `stick-1` on -003 |
| --- | --- | --- | --- | --- |
| `backstop` | 5 | 10 | 8 | 7 |
| `facts` | 12 | 0 | 12 | 14 |
| `machine event` | 8 | 0 | 0 | 0 |
| `watch` | 6 | 0 | 6 | 0 |
| All passes | 31 | 10 | 26 | 21 |
| CPU seconds | 4.7 | 2.8 | 9.4 | 8.0 |

No backstop pass repaired anything. `liken-1` ran its backstop and
nothing else. The hour before the rollout includes the 30 minutes of
measurement above.

`stick-1`'s `facts` passes are its clock. Samples of its status one
poll apart read −30 ms, +37 ms, and −8 ms from the same source, so a
single SNTP exchange over wifi moves the offset past 25 ms several
times an hour. This is noise in the measurement, not error in the
clock. Two answers fit: filter the samples, as NTP does when it keeps
the exchange with the shortest round trip of the last eight, or raise
the threshold to a bound that matters to the cluster, such as 100 ms.
Each publish also wakes two passes, because init writes the five
`time/` files one at a time. The dev-cluster's leaders ran about six
`watch` passes an hour whose source nobody has found; `liken-1`, the
testbed's leader, ran none.

## Tests

In a `synctest` bubble with `kubernetes/apiservertest`:

- A status write's echo wakes no pass, and a grant written after it
  does.
- A time measurement whose offset moves within the bound publishes no
  fact, and one past it publishes.
- A settled loop with time facts that republish every 64 seconds runs
  no pass for each republish that changes nothing in status.

## Verification needed

- Count the passes by cause for one idle hour on `liken-1` and
  `stick-1`, before and after. The `facts` and `watch` passes fall to a
  few an hour.
