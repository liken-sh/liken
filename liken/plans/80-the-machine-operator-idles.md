# 80. The machine operator idles

Milestone 80. Proposed 2026-10-09. The last of five milestones that
remove the ten-second ticker from `machine-operator`'s reconcile loop.
[Milestone 76](76-a-pass-reports-what-it-did-not-finish.md) gives the
series and the table of every job the ticker does. Milestones 76 to 79
move every job with an event off the ticker. Three jobs remain: the
heartbeat lease, which needs a clock; the sysctls, which have no
event; and a wake that the code forgot to send, which is a bug that no
event reports. This milestone gives each one its own mechanism and
deletes the ticker, so a settled machine runs no pass until something
changes. It also makes each judgment of the heartbeat read one clock,
not the clocks of two machines. It depends on milestones 76 to 79.

## The heartbeat

`cluster-operator` reads each machine's heartbeat lease, and
`effectivePhase` marks a machine `Lost` when its lease is older than
`HeartbeatStaleAfter`, 40 seconds (`cluster-operator/phase.go`). The
sweep and the conductor both judge machines through that function, and
a false `Lost` takes one of the rollout's slots. A machine that holds a
rollout grant reads `Updating`, not `Lost`, for up to
`rolloutStallAfter`, 10 minutes.

Today the pass renews the lease, before it publishes status, so that a
machine that boots into a fleet that already declared it `Lost`
announces itself before its status write. `Heartbeat.Renew` writes
only when the last renewal is older than `HeartbeatRenewAfter`, 8
seconds. The comment in `main.go` explains why a goroutine does not
renew: a heartbeat must prove that the operator does its job, and a
goroutine would keep renewing while the reconcile loop is stuck. So
today only a pass renews the lease, and the ticker starts a pass every
ten seconds to do it.

The failure the comment guards against is a loop that does not come
back to its `select`: a hung call or a lock that never releases. A
loop that waits in its `select` for an event is healthy, because it
acts the moment an event arrives. So the heartbeat changes in three
ways.

**The loop records when it is busy.** The loop marks itself busy, with
the time, the moment its `select` returns, and clears the mark just
before it enters the `select` again. The mark covers the whole body:
the facts watch's `Sync`, the `Machine` read, the pass, the sysctl
check, and the timer handling.

**A renewal timer.** A timer fires every 4 seconds, half of
`HeartbeatRenewAfter`, so a timer that fires a moment early does not
halve the renewal rate against `Renew`'s skip. It renews the lease
when the loop is not busy, or when the loop has been busy for less
than the limit. Past the limit it does not renew, the lease ages past
40 seconds, and `cluster-operator` marks the machine `Lost`. The timer
renews once at startup, before the first pass, with the owner UID from
the `Machine` that `main` read, so the boot ordering of today holds.
Only the timer calls `Renew`, because `Heartbeat` has no lock.

The timer and the loop share three values: the busy mark, the
`Machine`'s UID for the owner reference, and whether the `Machine` is
gone. A gone `Machine` gets no renewal, because a renewal would create
the lease again for the garbage collector to delete, and a `Machine`
that comes back clears the mark. Each value is an atomic, and the tests
run with `-race`.

**A bound on the pass.** The limit must be longer than any pass that is
not stuck, and the reconcile metric cannot measure that: its top
bucket is 16.4 seconds, so a 20-second pass and a 90-second pass both
count as `+Inf`. Long passes exist that are not stuck, such as a pass
of several requests, each with a 15-second timeout, the `syncfs`
before the image proof's promote, or one eviction per pod. So the pass
runs under a context deadline, and the limit is the deadline plus a
margin. A gauge records the longest pass since the operator started.
The starting proposal is a 45-second deadline and a 60-second limit.

A machine that is gone reaches `Lost` as fast as it does today. When
the machine loses power, its kernel panics, its network fails, or the
operator exits, the renewals stop at once. The lease ages past 40
seconds, and the sweep, which runs every 10 seconds, marks the machine
`Lost` within about 50 seconds.

Only a loop that is stuck while the process and the network work
reaches `Lost` later. Today a pass that hangs stops the renewals at
once, so it takes about 50 seconds too. With a 60-second limit it
takes up to 60 + 40 + 10 seconds, 110 seconds. In exchange, a slow
pass that is not stuck no longer ages the lease out: today a pass
longer than 40 seconds makes a working machine read `Lost`. The
liveness probe below restarts a stuck operator after the limit, so in
most cases the machine recovers before it reaches `Lost`.

## One clock for each judgment

Two comparisons of the heartbeat read a time that a different clock
wrote, and this milestone changes both while it changes the renewal.

**In `cluster-operator`.** `effectivePhase` subtracts the lease's
`renewTime`, which the machine wrote from its own clock, from the
time on `cluster-operator`'s clock. A machine whose clock runs 45
seconds behind writes renewals that already look 45 seconds old, so a
working machine reads `Lost`. A machine whose clock runs ahead writes
renewals that look fresh for longer, so a machine that is gone reads
`Lost` late. `liken` machines synchronize their clocks, so the skew is
small on a settled fleet. It is large at boot before the first
synchronization, and on a machine with a wrong hardware clock and no
time server.

The node lifecycle controller in Kubernetes does not compare the
kubelet's time with its own. It records, from its own clock, the
moment it sees a lease's `renewTime` change, and it measures the
staleness from that moment
(`pkg/controller/nodelifecycle/node_lifecycle_controller.go`).
`cluster-operator` already watches the leases in `liken-system`, with
no handler. The lease copy gets a handler that records, from
`cluster-operator`'s clock, when each lease's `renewTime` changed, and
`effectivePhase` measures the 40 seconds from that time. The record is
a cache of what the watch delivered. A `cluster-operator` that starts,
or that takes the lead, records each lease as seen at its start, so a
machine that is already gone reads `Lost` 40 seconds after that, as
the node lifecycle controller does.

**In `machine-operator`.** `Renew` skips a renewal when the
`renewTime` it parses back from its own copy is less than 8 seconds
old by the wall clock. When the clock steps back, for example when the
first time synchronization corrects it at boot, the last renewal looks
like it is in the future, and `Renew` skips every renewal until the
wall clock catches up. A step of one minute stops the renewals for one
minute, and the machine reads `Lost`. `Renew` keeps the time of its
last write in memory, from `time.Now`, whose monotonic reading does
not step, and compares against that.

## The liveness probe

The lease reports a stuck loop to the cluster, but nothing recovers
it. client-go's leader election has the same split: a goroutine renews
the lease, and `LeaderElector.Check`, served through a health adaptor,
fails a liveness probe when renewals stop, so the kubelet restarts the
process. The operator serves `/healthz` on its metrics listener,
`:9200`. It answers `500` when the loop has been busy past the limit,
and the `DaemonSet` adds a liveness probe on it. A stuck operator is
then restarted, and the restart's first pass runs from the state as it
is. A pass that is long but not stuck stays under the deadline, so the
probe does not restart it. When a person turns the metrics listener
off with `--metrics-address=`, the probe has no endpoint, so the
manual tells them to remove the probe too.

## The sysctls

`applySysctls` writes the OS defaults and `spec.sysctls` into
`/proc/sys` on each pass, and writes a value only when the value it
reads differs (`conditions.go`), so a value that another process
changes is written back within ten seconds. `/proc/sys` sends no event
for a change. A check keeps that pace, but it reads the sysctls in
place of running a pass. Every ten seconds, an arm of the loop's
`select` reads each name that the last pass applied, and compares the
value with the value the pass read back after its write, which is
what `status.sysctls` holds. It wakes a pass only when one differs.

The comparison is against what the kernel reported, not against the
value the pass wrote, because the kernel reports some values in
another form. An integer written as `0x10` reads back as `16`, and a
partial write to a multi-value sysctl such as `kernel.printk` reads
back as four values. A comparison against the written value would
wake a pass every ten seconds forever. A name that failed to apply is
not in the map, so the check does not read it. A parameter that
appears later, such as one under a network interface, appears with a
uevent, and milestone 77's listener wakes a pass for it, unless its
device is under `/devices/virtual/`, which the listener filters.

On a settled machine the check is a few small reads every ten seconds,
with no pass and no API request. The comment at the check names the
failure it covers, as the rule for events asks.

## The backstop

Every event source the operator uses either delivers each change or
reports that it lost some. A Kubernetes watch resumes from its last
version and lists again on `410 Gone`. The uevent socket reports an
overflow with `ENOBUFS`, and inotify with `IN_Q_OVERFLOW`, and both
readers wake a pass on it. After milestone 76, a reader that stops
closes its channel. So a lost event comes from the operator's own
code: a path that changes state and sends no wake, or a failure that
the outcome does not record. The reviews of this series found several
of those in its first draft.

So a backstop pass runs every 5 minutes, with a jitter of up to 10
percent. It is an ordinary pass, and milestone 76's recorder notes
every write it makes. A backstop pass on a correct machine writes
nothing. A backstop pass that writes anything found a bug, and the
operator reports each write loudly:

- a log line that names the step and what it changed;
- a counter, labeled by step, so an alert can fire on any increase;
- a `Warning` `Event` on the `Machine`, with the reason
  `BackstopRepaired`, naming the step, so `kubectl describe` shows it.

A count above zero is a bug report that names the missed wake. The fix
is to add the wake, not to shorten the backstop. The comment at the
timer names the failures it covers. controller-runtime keeps a backstop
for the same reason: its `SyncPeriod` defaults to 10 hours, as
insurance against a bug that drops a requeue.

## The ticker

The ticker and its arm in the `select` are deleted. The comments that
describe the ticker or its cadence change with it:

- `main.go` and the head of `dra.go`;
- `kubernetes/heartbeat.go`, `kubernetes/apiclient.go`, and
  `kubernetes/resourceslices.go`;
- `fetch.go`, `release.go`, `watches.go`, `reconcile.go`, `hosts.go`,
  and `drain.go`;
- `cluster-operator/main.go`, where it describes the machine
  operators' cadence.

The sysctls reference page (`docs/content/docs/reference/sysctls.md`)
says the operator applies the values on every pass, about every ten
seconds. The page changes to describe the check every ten seconds.

## Tests

The loop function from milestone 76 takes the clock and its channels.
In a `synctest` bubble with `kubernetes/apiservertest`:

- A settled machine runs one backstop pass every 5 minutes, with
  jitter, for an hour of fake time, and its lease stays fresh for the
  whole hour.
- A loop that blocks past the limit stops the renewals, the lease ages
  past 40 seconds, and `/healthz` answers `500`.
- A `Machine` that is deleted gets no more renewals, and renewals start
  again when it comes back.
- A machine whose fake clock runs 45 seconds behind
  `cluster-operator`'s keeps a fresh lease and does not read `Lost`.
- A wall clock that steps back one minute does not stop the renewals.
- A sysctl that the test changes in a fake `/proc/sys` wakes a pass
  within ten seconds, and the pass writes it back. A fake `/proc/sys`
  that reports a value in another form than the one written wakes no
  pass.
- A backstop pass that finds a missing hosts entry posts a
  `BackstopRepaired` `Event` and counts it.

## Verification needed

- Count the passes per hour on an idle machine in the `lab` fleet and
  on `liken-1`. Each pass has a cause in the log or the metrics.
- Watch the backstop counter on the `lab` fleet for a week. Each
  increase is a missed wake to fix.
- Hang a pass on purpose, and confirm that `cluster-operator` marks
  the machine `Lost` within the limit plus 40 seconds plus one sweep
  tick, and that the kubelet restarts the operator.
