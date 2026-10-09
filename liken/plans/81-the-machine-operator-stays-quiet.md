# 81. The machine operator stays quiet

Milestone 81. Proposed 2026-10-09. It follows milestones 76 to 80,
which removed the ten-second ticker from `machine-operator`'s reconcile
loop. On the testbed, an idle machine went from about 420 passes an
hour to between 34 and 50
([milestone 80](completed/80-the-machine-operator-idles.md#what-the-lab-measured)).
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
