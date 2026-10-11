# 84. The clock offset is a bound in status

Milestone 84. Proposed and built 2026-10-11, released in
2026.10.11-002, corrected twice after testbed drills, and drilled on
the testbed in 2026.10.11-004
([What the testbed showed](#what-the-testbed-showed)). It follows
milestone 81, which
stopped `init` from republishing the time facts on every poll, and it
closes the finding that milestone 81 left open: on wifi, single SNTP
samples move the offset past 25 ms several times an hour
([milestone 81](81-the-machine-operator-stays-quiet.md#what-the-testbed-showed)).

## What happens now

`init` measures the clock every 64 seconds with one SNTP exchange,
slews the clock by the measured offset, and writes the time facts to
five files under `/run/liken/facts/time/`. It rewrites the files only
when the state, the source, or the stratum changes, or when the offset
moves 25 ms or more from the value it last published.
`machine-operator` copies the facts into `status.time`, and the
`Offset` printer column shows `status.time.offset`.

Three things are wrong with that.

**The column looks live, and it is not.** On a wired machine the
offset stays near 3 ms, so the 25 ms threshold never trips, and
`kubectl get machine` shows the offset from the boot for the life of
the boot. On `liken-1` it read 3.21 ms for 17 hours. The value is
correct to within 25 ms, but nothing on the screen says so.

**No path carries the reading anywhere else.** `machine-operator`'s
metrics read the status that it is about to write
(`machine-operator/metrics.go`), so a metric of the offset would
freeze in the same way. A person who wants to graph the clock's error,
or alert on it, has nothing to scrape.

**One sample is not a measurement.** `stick-1` joins its network over
wifi. Its readings one poll apart were −30 ms, +37 ms, and −8 ms from
the same source. That is noise from the asymmetric delay of a wifi
link, not error in the clock. `init` slews by each raw sample, so it
pushes the clock 30 ms one way and 37 ms the other, and each sample
past 25 ms is a status write.

The five files also break rule 5 of the facts tree
(`machine/factstree.go`): a group of facts that change together while
the machine runs is one record file. `init` rewrites the five files
one at a time, so each publish wakes two reconcile passes.

## The design

The time facts split into two kinds. **The standing** is the state,
the source, the stratum, and a bound on the offset. It changes
rarely, and each change is news, so it stays in `status.time`. **The
reading** is the exact offset, measured on every poll. It means
nothing without its history, so it goes to Prometheus and never
reaches the API server.

### A clock filter

`init` keeps the last eight samples from its current source, each
with its offset and its round-trip delay. This is NTP's clock filter
(RFC 5905, section 10). A short round trip leaves little room for
the two directions of the trip to differ, and that difference is what
the offset calculation cannot cancel. An old sample also carries the
drift of the clock since it was measured. RFC 5905 grows that part of
a sample's error at 15 ppm, because ntpd also corrects the clock's
frequency. `init` corrects only the offset, so the filter grows it at
100 ppm, twice the drift that `liken-1` showed, and selects the sample
whose error bound, half its round trip plus its drift since, is
smallest. The filter drops a sample older than eight polls, and it
starts empty when the source changes.

A sample measured the clock as it was when the sample was taken, and
each slew since then has removed part of that error. So the filter
records, with each sample, the total correction that the kernel had
applied at that moment, and it subtracts the correction applied since
then from the sample's offset. The kernel reports the part of a slew
that it has not applied yet (`ADJ_OFFSET_SS_READ`), and the applied
total is the requested total less that part. chrony corrects its
stored samples for each clock adjustment in the same way. A new slew
replaces the pending part of the last one, so `init` slews by the
corrected offset on every poll.

The filter feeds all three outputs: the slew, the bound in status, and
the reading for Prometheus.

### The bound in status

`status.time.offset` holds the smallest bound that the filtered offset
fits inside:

| Bound | What it means |
| --- | --- |
| `±1ms` | A follower on a quiet LAN, syncing from a leader. |
| `±5ms` | A leader on a good internet upstream. |
| `±25ms` | A noisy path, such as a wifi link. |
| `±128ms` | Past this, drift outruns the slew. It is `stepThreshold`, the line ntpd draws between slewing and stepping. |
| `±1s` | The clock is badly wrong, but TLS and leases still work. |
| `>1s` | Anything worse. |

A worse bound publishes at once, because that is the news a person
needs. A better bound publishes only after three polls in a row inside
it, which is the window that `syncStaleAfter` already uses. A clock
that stays near a boundary does not write status at each crossing.

The standing is one record file, `time/standing`, so one rename
replaces it and one reconcile pass reads it. A machine that lost its
sources reports `Unsynchronized` with no bound, because a bound is a
claim about a measurement it no longer has. After a boot step, the
bound is absent until the first poll, because the step measured the
clock before the step corrected it.

`init` prints a console line each time the published bound changes,
so the console and the status agree.

### The `ClockSynchronized` condition

`machine-operator` sets `ClockSynchronized` from `status.time`:

| Status | Reason | When |
| --- | --- | --- |
| `True` | `Synchronized` | The clock follows a source, within `±128ms`. |
| `True` | `FreeRunning` | The cluster declares no upstreams, on purpose. |
| `False` | `Unsynchronized` | The sources stopped answering. |
| `False` | `OffsetTooLarge` | The bound is `±1s` or `>1s`. |
| `Unknown` | `NoClockFacts` | `init` published no time facts. |

Each transition posts an `Event`, through the path every condition
already takes (`postStatusEvents`). The condition does not feed
`Ready` or the phase, the same as `SerioAttached`. An upstream outage
makes every leader `Unsynchronized` at once, and a fleet that turns
`Degraded` all together stalls every reboot turn and hides the
machine with a real fault.

### The reading for Prometheus

`init` writes the reading to `/run/liken/clock/reading` on every poll
that gets an answer: the filtered offset, the delay of the selected
sample, the source, and the time of the poll. The file is outside the
facts tree on purpose. `machine-operator` watches the whole facts tree
with inotify, so a file written every 64 seconds inside it would wake
a pass every 64 seconds. `machine-operator` already mounts all of
`/run/liken`, so it reads the file with no manifest change.

`machine-operator` reads the file only when Prometheus scrapes it, and
publishes two series:

- `liken_time_offset_seconds`, the filtered offset. The series is
  absent before the first answer, and absent when the reading is older
  than three polls, so a stale reading never looks current.
- `liken_time_last_sync_timestamp_seconds`, the time of the last poll
  that got an answer. It stays after the offset goes stale, so a graph
  shows when the clock lost its source.

When nobody scrapes, nothing reads the file, and the reading costs one
tmpfs write a minute.

### The exact value on demand

A person who wants the exact offset without Grafana reads it from the
same endpoint:

```sh
kubectl get --raw \
  "/api/v1/namespaces/liken-system/pods/<machine-operator-pod>:9200/proxy/metrics" \
  | grep liken_time_
```

The manual gives this recipe beside the description of
`status.time`. There is no plugin or subresource for it, because a
new API path into PID 1 for a number that Prometheus already holds
costs more than it gives.

### What goes away

`lastSync` leaves the CRD schema. The 25 ms republish threshold,
`offsetPublishThreshold`, leaves `init`. The five files under
`time/` become the one record.

## Choices against the obvious alternatives

- **Damp the exact offset harder** instead of publishing a bound, for
  example with a 100 ms threshold. The column would still show a
  frozen number that looks live. A bound states what the number
  promises.
- **Ship a `PrometheusRule`** for the alert. No component in the
  repository ships one yet, and it fires only where the Prometheus
  Operator runs. The condition alerts on any cluster. A rule for every
  component is its own decision.
- **Publish the reading through the facts tree.** The operator
  watches the whole tree, so every poll would wake a pass. That is the
  cost milestone 81 removed.

## Tests

- The filter selects the sample with the smallest error bound, drops
  samples older than eight polls, and starts empty when the source
  changes.
- The filter subtracts from each sample the correction applied since
  it was measured, and keeps the part of a slew still pending.
- A worse bound publishes at the poll that measures it. A better bound
  publishes after three polls in a row inside it, and not after two.
- The loop writes the reading on every answered poll.
- The facts round-trip `time/standing` as one record.
- The collector publishes no offset before the first reading or after
  three polls of silence, and still publishes the last-sync time.
- `ClockSynchronized` takes each row of its table, and a False
  `ClockSynchronized` leaves `Ready` `True`.

## What the testbed showed

Release 2026.10.11-002 reached `liken-1` at 15:55 UTC and `stick-1` at
15:58 UTC on 2026-10-11.

`liken-1` stepped its clock by 133 ms at boot, published no bound, and
published `±5ms` at its first poll 65 seconds later.
`liken_time_offset_seconds` read 3.20 ms and 3.49 ms on two scrapes 70
seconds apart, and `ClockSynchronized` was `True`.

`stick-1` measured 102 ms at boot, under the step threshold, so it
slewed and published `±128ms`. Its reading then stayed at 37.6 ms from
16:02 to 16:10, and the bound stayed at `±128ms` until 16:13, when the
reading was −0.65 ms and the bound went to `±1ms`. The 37.6 ms sample
had the shortest round trip, so the filter selected it for eight polls
after the clock had been slewed by it. The filter as first built
slewed once by each selected sample and reported the sample's offset
as measured. The correction for applied slews above replaced that
rule.

Release 2026.10.11-003 carried that correction, and it reached both
machines at 16:37 UTC. The readings then read exactly 0 on most polls,
and `liken-1` went to `±25ms` for six minutes. After a slew applied,
the sample that had driven it read 0 and kept the shortest round trip,
so the loop slewed by 0, and the clock's drift went uncorrected until
a newer sample won. `liken-1`'s steady 3.2 ms before this milestone was
that drift, about 50 ppm over each 64-second poll, and the first
filter had corrected it at every poll. Selecting by the error bound
above, with age weighed in, replaced selection by round trip alone.

Release 2026.10.11-004 carried the selection by error bound, and both
machines were Ready on it at 17:11 UTC. Over the next 20 minutes, one
sample a minute:

| | `liken-1`, wired | `stick-1`, wifi |
| --- | --- | --- |
| `liken_time_offset_seconds` | 2.80 ms to 3.67 ms | 0.28 ms to 5.29 ms |
| Bound in status | `±5ms` throughout | `±5ms`, `±25ms` for 3 polls after the 5.29 ms reading, then `±5ms` |
| `facts` passes | 1 | 3 |
| `ClockSynchronized` | `True`, `Synchronized` | `True`, `Synchronized` |

Before this milestone, `stick-1`'s readings swung from −30 ms to
+37 ms on consecutive polls, and it ran 69 `facts` passes in the 18
hours before the rollout. `liken-1`'s steady 3 ms is its drift over
one poll, which a frequency correction would remove; `init` corrects
only the offset.

The console line for a new bound reached the log relay as
`\xc2\xb15ms`, because the kernel escapes every byte outside
printable ASCII when a reader reads `/dev/kmsg`. The console line now
spells the bound `+/-5ms`, and status keeps `±5ms`.

## The drill

Roll the release onto the testbed. On `liken-1` and `stick-1`, check
that the `Offset` column shows a bound, that `ClockSynchronized` is
`True`, and that `liken_time_offset_seconds` moves between scrapes.
Count the `facts` passes in an idle hour on `stick-1` against the 14
of milestone 81. The checks ran on 2026.10.11-004, above. The idle
hour has not run; the 20 minutes above held 3 `facts` passes on
`stick-1`, one of them the boot's.
