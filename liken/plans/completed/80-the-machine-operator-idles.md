# 80. The machine operator idles

Milestone 80. Proposed and built 2026-10-09. The week of the backstop
counter on the `lab` fleet has not run, and neither has a pass that
hangs on purpose.
The QEMU drills are in [What the lab measured](#what-the-lab-measured).

The last of five milestones that
remove the ten-second ticker from `machine-operator`'s reconcile loop.
[Milestone 76](76-a-pass-reports-what-it-did-not-finish.md) gives the
series and the table of every job the ticker does. Milestones 76 to 79
move every job with an event off the ticker. Three jobs remain: the
heartbeat lease, which needs a clock; the sysctls, which have no
event that the operator can see; and a wake that the code forgot to send, which is a bug that no
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
changes is written back within ten seconds. Milestone 77 measured why
no watch can replace the check. A write through `/proc/sys` does send
`IN_MODIFY` and `IN_CLOSE_WRITE`, but only to a watch on the same mount
of procfs, because each mount has inodes of its own. On `node-1`, a
write through a debug pod's `/proc` reached a watch on that `/proc`
and not a watch on `/host/proc`, and a write through `/host/proc` the
other way round. Each container mounts its own `/proc`, so a watch in
the operator's pod sees only the operator's writes. A value that the
kernel changes as a side effect of another write sends no event on
any mount: a write to `net.ipv4.ip_forward` sets
`net.ipv4.conf.all.forwarding`, and that file got none in a test on a
workstation's kernel. A check keeps
the ticker's pace, but it reads the sysctls in place of running a
pass. Every ten seconds, an arm of the loop's
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
closes its channel, and after milestone 77, a closed channel ends the
operator, so the kubelet starts it again and the new process reads
everything. So a lost event comes from the operator's own code: a path that changes state and sends no wake, or a failure that
the outcome does not record. The reviews of this series found several
of those in its first draft.

So a backstop pass runs every 5 minutes, with a jitter of up to 10
percent. It is an ordinary pass, and milestone 76's recorder notes
every write it makes. It must read everything a pass reads. Milestone
77 lets a pass that only the ticker woke skip the walk of sysfs and
the read of `/etc/hosts` (`localReads` in `machineevents.go`), so the
backstop's wake must not count as the ticker's, or it would skip the
very reads that find a missed uevent or inotify wake. With the ticker
gone, `tickOnly` and the reuse it allows go with it, unless the check
of the sysctls needs a pass of its own. A backstop pass on a correct machine writes
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

The ticker and its arm in the `select` are deleted. A facts watch that
dies no longer opens again on a tick: milestone 77 made it end the
operator, the same as the uevent listener and the hosts watch, so that
job needs no new home. One thing the ticker does moves to the retry
timer first:

- A lasting failure that a person fixes outside the `Machine`, such
  as a `403` that an RBAC grant fixes, waits up to five minutes for
  its next try once no tick comes sooner. Decide whether a `403` and
  a `422` deserve a lower ceiling than a missing kernel parameter. The comments that describe the ticker or its
cadence change with it:

- `main.go`, `loop.go`, and `machineevents.go`, where milestone 77
  describes the tick-only pass;
- `kubernetes/heartbeat.go`, `kubernetes/apiclient.go`, and
  `kubernetes/resourceslices.go`;
- `fetch.go`, `release.go`, `watches.go`, `reconcile.go`,
  `ownstatus.go`, and `drain.go`;
- `cluster-operator/main.go`, where it describes the machine
  operators' cadence.

The sysctls reference page (`docs/content/docs/reference/sysctls.md`)
says the operator applies the values on every pass, about every ten
seconds. The page changes to describe the check every ten seconds. The
check can compare against `status.sysctls` because milestone 77 made a
pass apply an overridden name once, with the spec's value. Before
that, a pass wrote the OS default and then the spec's value, and the
kernel held the default for a moment on every pass.

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

## What was built

The design above was built, with these departures. Several came from
an adversarial review of the first build, in three parts: liveness and
clocks, the sysctls and the backstop, and every decision that depends
on time or on state with no wake.

- **No liveness probe.** The renewal timer ends the process itself
  once the loop has been busy for 60 seconds (`liveness.go`), and the
  `DaemonSet` has no `livenessProbe`. A kubelet probe also fails while
  the listener is not open: during the setup before the loop, when
  port 9200 is taken or moved by `--metrics-address`, and for a binary
  older than its pod template, which answers `404` on `/healthz`. Each
  of those would restart an operator that is not stuck. `/healthz`
  still answers the same check, for a person.
- **The pass deadline keeps the client's answer to a `429`.**
  `apiclient`'s `WithContext` also ends the wait after a `429` with the
  context, so each request of a pass would wait out a `429` for up to
  ten seconds. `kubernetes.Within` binds the pass's context and keeps
  the wait that has already ended.
- **The first renewal runs before the busy mark**, because its requests
  run under no pass's deadline. The gauge of the longest pass times the
  whole busy window, from the wake to the end of the pass.
- **The Node watch ignores the kubelet's heartbeat.** The kubelet
  rewrites the Node's status every five minutes with only the
  `lastHeartbeatTime` of each condition changed, which a review measured
  on `liken-1` at 5 minutes 5 seconds. Each write woke a pass and moved
  the backstop back, so the backstop fired only when its jitter was
  under 5 seconds, and most repairs it exists to report were made by
  an ordinary pass and never counted. `WakeOnContent` takes functions
  that remove fields before the comparison, and the Node's handler
  removes the heartbeat times. It also ignores `managedFields`.
- **The sysctl apply remembers its writes** (`sysctlMemory` in
  `conditions.go`). The kernel stores some values in another form: `0x10`
  reads back as `16`, a write of one value to `kernel.printk` reads
  back as four. Compared with the spec's value, such a parameter was
  written on every pass, and each backstop pass would have reported
  the write as a repair. A parameter that still reads what the kernel
  reported after the last write of the same value is current. A
  write-only parameter, such as `vm.drop_caches`, is written once for
  each value.
- **The sysctls are read back after both sets.** Two names can write
  one kernel variable, such as `net.ipv4.ip_forward` and
  `net.ipv4.conf.all.forwarding`. A read taken right after each write
  held a value that a later write changed, so the check found it
  drifted on every check. A default is also dropped when the spec names
  the same file in the other spelling, dots or slashes.
- **The check reads the parameters that did not exist.** A parameter
  under an interface in `/devices/virtual`, which sends no uevent the
  listener keeps, now applies within ten seconds of the interface
  appearing, not at the lasting retry.
- **A backstop pass is a backstop pass only when nothing else
  explains it.** The backstop is not armed while a retry is due,
  because the retry's pass does the same work, and a write it makes
  after somebody fixes the cause is the retry's. A backstop that fires
  while a wake is waiting, or while a sysctl has drifted, gives the
  pass to that cause.
- **Each pass counts its cause**, in `liken_machine_passes_total`, so
  the passes of a settled machine can be explained.
- **A failed `Sync` of the facts watch** goes into the outcome, and the
  retry runs it again within a second or two.
- **The inotify readers end when their directory leaves its path.**
  The reader dropped `IN_IGNORED` and `IN_UNMOUNT`, which carry no
  name, so a removed, renamed, or unmounted `/host/etc` left a watch
  that never woke and never closed. A record of `IN_IGNORED`,
  `IN_UNMOUNT`, `IN_DELETE_SELF`, or `IN_MOVE_SELF` for the watched
  directory, or the facts tree's root, now closes the channel, and the
  operator ends. A hosts watch that fails to open ends the operator
  unless its directory does not exist. `Sync` and the reader's close
  of the descriptor are exclusive, so `Sync` cannot add a watch to a
  descriptor number the process gave to another file.
- **`cluster-operator` holds the rollout for its first 40 seconds.** A
  new process records every Lease as seen at its start, so for 40
  seconds a machine that is already down reads its last status, usually
  `Ready`. The rollout grants and reclaims no turn until the record is
  40 seconds old, so a dead leader cannot count as up while a second
  leader takes its turn.
- **A `403` and a `422` keep the one five-minute ceiling.** Controller
  backoffs commonly run longer, and a person who grants RBAC can see
  the retry's line in the log.
- **`Heartbeat.Renew` keeps the time of its last write**, from
  `time.Now`, and a new process renews once whatever the lease's
  `renewTime` says.

The review also found these, which stay as they are:

- **A hang in uninterruptible sleep is not recovered.** A `syncfs` or a
  sysfs read in the D state survives `SIGKILL`, so ending the process
  does nothing. The machine reads `Lost`, as it did before.
- **An old binary under new RBAC.** During an upgrade, a follower that
  runs the new binary under the old RBAC cannot open the watches
  milestone 79 added. Its reads go to the API server, and a change to
  those kinds waits for the backstop until the new RBAC lands.
- **A modules intent that `init` refuses** is asked again on every pass,
  and each backstop pass reports it. It happens only when the operator
  and `init` disagree about whether a change can load live, which is a
  bug, so the report is the right signal.

## What the lab measured

`node-1` of the `lab` fleet, on 2026-10-09, under UEFI. `make
smoke-uefi` installed the first build on blank disks and reported
`Ready` after 15 seconds.

On the first build, before the review's fixes:

- 10 idle minutes ran 8 passes. The longest pass took 36 ms, and the
  backstop counter stayed at zero.
- The lease renewed every 8 or 12 seconds: a timer that fires a moment
  early skips one firing, as designed.
- `vm.max_map_count`, written to `65530` from a debug pod, read
  `524288` again after 2.9 and 4.2 seconds. `/etc/hosts`, overwritten,
  was written back after 1.0 seconds.

An upgrade to the build with the fixes, with a one-replica `Deployment`
held by a `PodDisruptionBudget` of `maxUnavailable: 0`, drained on the
first build:

- The operator asked for the guarded pod 12 times in the first 2
  seconds, while the cordon moved the other pods, and once in the next
  67 seconds. The API server stated no `Retry-After` for the budget's
  refusal, so nothing asked again on a timer.
- The pod's `deletionTimestamp` was set 0.12 seconds after the patch
  that relaxed the budget returned.

On the build with the fixes, after the upgrade:

- 10 idle minutes ran 4 passes, half the first build's 8. The longest
  pass took 68 ms, and the backstop counter stayed at zero.
- Over the whole drill, `liken_machine_passes_total` counted: 1 at
  start, 6 woken by a watch, 4 by a machine event (each overwrite of
  `/etc/hosts` and the operator's own write back), 2 by a sysctl that
  drifted, 1 by the facts tree, and 1 backstop pass.
- `vm.max_map_count` read `524288` again after 0.7 and 9.7 seconds, both
  inside one check of ten seconds. `/etc/hosts` was written back after
  1.0 seconds.

On the testbed, `liken-1` and `stick-1`, after the rollout of release
2026.10.09-001 on 2026-10-09:

- The rollout took 6 minutes, one machine at a time. `stick-1` took its
  turn 54 seconds after `liken-1` reported `Ready`, which includes the
  40-second hold of the `cluster-operator` that started again when
  `liken-1` rebooted.
- In 30 idle minutes, `liken-1` ran 20 passes and `stick-1` 15. On the
  release before, each ran about 420 passes an hour.
- The lease renewed every 8 or 12 seconds on both.
- A synthetic `add` uevent on the GPU ran a pass after 1.0 and 0.9
  seconds, the settle. A `change` uevent runs none, by design.
- `vm.max_map_count` came back after 6.7 and 8.9 seconds, and
  `/etc/hosts` after 0.95 and 0.90 seconds.
- The backstop reported a repair on every one of its passes, and both
  were real bugs that predate this milestone. A claim on an input
  device rewrote its CDI spec on every pass, because the comparison
  read the evdev node's file mode as a pointer. `stick-1`, which joins
  its network over wifi, wrote its status on every pass, because the
  Machine schema did not declare `status.boot.network`'s `wireless` and
  `hostEntries`, and the API server pruned them from each write. Both
  are fixed, and a test now requires a schema property for every field
  `MachineSpec` and `MachineStatus` can hold.

## Verification needed

- After the next release, confirm the backstop counter stays at zero on
  the testbed.
- Watch `liken_machine_backstop_repairs_total` on the `lab` fleet for a
  week. Each increase is a missed wake to fix.
- Hang a pass on purpose, and confirm that the operator ends itself
  after 60 seconds, the kubelet starts it again, and a loop that stays
  stuck reaches `Lost` as the kubelet's backoff grows.
