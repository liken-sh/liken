# 76. A pass reports what it did not finish

Milestone 76. Proposed and built 2026-10-09. One drill has not run:
an event reader that stops on a machine. The tests in a `synctest`
bubble cover it. The QEMU drills are in
[What the lab measured](#what-the-lab-measured).

The first of five milestones,
76 through 80, that remove the ten-second ticker from
`machine-operator`'s reconcile loop, so that a settled machine runs no
pass until something changes. Many steps of a pass leave a failure to
the next tick, and the event readers stop in silence when they fail.
This milestone makes a pass report what it did not finish and what it
changed, gives the loop one timer for the earliest moment a pass asks
for, and makes every event reader signal when it stops. The later
milestones build on all three.

## The series

The loop in `machine-operator/main.go` runs a pass on three signals:
the Kubernetes watches, an inotify watch on init's facts tree, and a
ticker that fires every ten seconds. The comment on the ticker gives
it two jobs. It is the heartbeat's clock, and it is the backstop for
the state that no event announces. The repository's rule for events
says a timer that reads state again to find a change is a defect, and
most of what the backstop catches does have an event. An audit of
`reconcile` on 2026-10-09, and three reviews of a first draft of this
series against the code, sorted every job the ticker does:

| Job | Kind | Milestone |
| --- | --- | --- |
| A write that failed: status, slice, `Node`, promotes, store, CDI, hosts, sysctls | a retry after a failure | 76 |
| A transient download failure | a retry after a failure | 76 |
| An event reader that stopped after it started | the reader signals its exit | 76 |
| Driven devices in the `ResourceSlice`, the CDI refresh, the device metric | sysfs, which has uevents | 77 |
| An `/etc/hosts` entry that another process changes | a file, which has inotify | 77 |
| A facts watch that could not start | a crash, so the kubelet restarts the operator | 77 |
| A download that finished, or a replaced download that exited | a goroutine, which can signal | 78 |
| A watch that works again after it failed | a new `Recovered` callback in the informer | 78 |
| A `ResourceSlice` that the kubelet or a person deletes | the slice watch, which has no handler | 78 |
| OS containers on the node that become Ready, for the image proof | a watch on the node's pods while a proof runs | 79 |
| Pods that leave during a drain, and the 5 min drain deadline | a watch while the drain runs, and a deadline | 79 |
| An eviction that a `PodDisruptionBudget` refused | a watch on the budgets while the drain runs | 79 |
| A feature removal that waits for `HelmChart`s or `LoadBalancer` `Service`s | a watch on them while the removal waits | 79 |
| The heartbeat lease | a clock | 80 |
| A sysctl that another process changes | `/proc/sys`, which has no events, so a check | 80 |
| A wake that the code forgot to send | a backstop pass that reports each repair | 80 |

The ticker keeps running through milestones 76 to 79. Each of them
moves jobs off it, and each ships on its own, because the ticker still
catches whatever a milestone misses. Milestone 80 removes the ticker
and depends on the four before it.

## What happens now

`reconcile` returns one `error`, and the comment at its head says the
error means the status write failed (`reconcile.go`). The loop records
it in `ObserveReconcile` and waits for the next wake. Every other
failure in the pass is logged, or turned into a condition, and left to
the next tick:

- the status publish, after its one inline retry on a conflict;
- the `ResourceSlice` write (`dra.go`) and the CDI rewrite (`cdi.go`);
- the taints, the labels, the cordon, and the uncordon on the `Node`.
  A `409` on the taints is repaired by the `Node` change that caused
  it, which the `Node` watch delivers, but any other failure waits for
  a tick;
- the promotes and `WriteProven` in `settleClusterLifecycle` and
  `settleSystemReleaseLifecycle`, and the `syncfs` and the promote of
  the image proof;
- the imports store reads in `settleImportsLifecycle`;
- the staged manifest, `WithdrawStaged`, `ClearRejection`,
  `withdrawOtherStage`, the demotion intent, the reboot-request
  intent, and the registry credentials in the machine store;
- the hosts write (`hosts.go`) and the sysctl writes
  (`conditions.go`);
- every `429` from the API server, which the client answers at once
  with the error, and whose comment says the loop is its retry
  (`kubernetes/apiclient.go`).

A transient download failure waits for the next pass too, and the
fetcher's comment says it "retries every pass, forever" (`fetch.go`).
"Transient" covers every answer that is not `200`, so a `404` for a
mistyped version counts as transient.

The event readers stop in silence. `readUevents` in
`hardware/uevent.go` returns on a poll error and never closes its
channel. The inotify reader in `machine/inotify.go` does the same, and
its comment says a caller that must not miss a change needs its own
backstop. Today that backstop is the ticker.

init leaves two writes to "the operator's next pass". After a live
module load, init writes the facts again, so the facts watch wakes a
pass. A refused live load is only a console line, and today the
operator writes the modules intent on every pass, so init tries again
every ten seconds. A restart intent that init loses cannot occur
without a crash of init, and init is PID 1, so its crash panics the
kernel and the machine reboots (`init/crash.go`).

## The design

**A pass records its outcome.** A recorder, scoped to one pass,
travels with the pass's `reader`. The API helpers report into it for
each `5xx`, `429`, timeout, and network error, so the list of failures
is complete by construction, not by a list that a person keeps. Local
writes add an explicit mark. Each mark names its step, so a log line
and a metric can name it too. The recorder also notes each write the
pass made: a status publish, a slice write, a `Node` patch, a store
write, a hosts write, a sysctl write. Milestone 80 uses those notes to
report what its backstop repaired. `ObserveReconcile` keeps its
meaning: its error label still counts a failed status write.

A step can also ask for a later wake: "wake me no later than T". The
download's next attempt uses it, and milestone 79 gives the drain's
deadline to it. The recorder keeps the earliest T. A `429` sets the opposite bound: the
retry comes no sooner than its `Retry-After`, because `liken`'s client
answers a `429` at once and leaves the wait to the loop.

**One wake-at timer.** When a pass ends, the loop sets one timer for
the earliest moment the outcome asks for, and the timer wakes a pass.
Each pass computes the moment again from the state it reads, so a
restart of the operator, a withdrawn grant, or a wall clock that steps
back needs no special case.

**The retry delay.** A pass that ends with failures asks for a retry.
The delay sorts each failure by kind:

- A transient failure (`5xx`, `429`, `409`, `401`, a timeout, a
  network error, a `2xx` whose body did not decode, `EIO`, `EBUSY`, and
  any error with no errno) starts at one second and doubles on each
  pass that still has a transient failure, up to ten seconds plus the
  jitter, about the pace the ticker gives today.
- A failure that will not change by itself (`400`, `403`, `422`,
  `ENOENT`, `EINVAL`, `ENOTDIR`, `EISDIR`, `EACCES`, `EPERM`, `EROFS`,
  and a sysctl name that escapes `/proc/sys`) starts at ten seconds
  and doubles up to five minutes, so a misconfigured machine does not
  retry every ten seconds forever. An edit by a person sends a watch
  event, and that pass tries again at once.

A request that succeeds withdraws an earlier `409` on the same method
and path, and no other failure, because the status write and the
heartbeat each answer a `409` by reading the object again and writing
once more. Several writes share one path, such as the taints, the
labels, and the cordon on the `Node`, so a write that lands says
nothing about another that failed. A read of init's facts that fails
is transient whatever its errno, because init writes the facts. A CDI
specification that does not decode is lasting. A fix that the
operator does not watch, such as an RBAC grant, waits for the lasting
retry, up to five minutes.

Each delay carries a jitter of up to 10 percent, so a fleet that fails
together during an API server outage does not retry together. A pass
with no failures stops the timer and resets the delay.

**The download's backoff.** A transient download failure retries on
its own backoff, from ten seconds up to two minutes, with the same
jitter. A release server that fails usually fails for minutes, and the
failure keeps its reason in the condition in the meantime. Two minutes
keeps a fleet that waits on a release close behind a server that comes
back. A new ask resets the backoff. The fetcher records the time of the
next attempt, and `convergeSystemRelease` asks for a wake at that time.
A download that waits out its backoff does not count as a failure for
the retry delay above. The comments in `fetch.go` and `release.go`
that say a `Failed` state exists only between passes change with it.

**Readers signal their exit.** `hardware.ListenForUevents` and the
inotify reader in `machine/inotify.go` close their channel when the
reader returns for any reason other than a cancelled context. A
closed channel tells the caller the watch died. The loop runs a pass,
which reads the whole facts tree, and opens the watch again on the
next tick, so a watch that dies the moment it opens costs one pass
every ten seconds and not a pass after every death. init's components
return an error, and init's machine plane starts each one again after
its backoff: the component opens a new listener and walks the whole
state before it waits. The logs relay exits, and the kubelet starts it
again. A reader that stops on its own also ends the goroutine that
waits for its cancel, so it leaves no goroutine or descriptor behind.

**The modules intent.** After milestone 80, the operator writes the
modules intent once for each pass that an event starts, so init tries
a refused live load once for each event, not every ten seconds. That
is the intended behavior, because a refused load is refused for a
reason that a retry does not change, such as a module in use.

## The loop moves into a function

The loop in `main()` moves into a function that takes its wake
channels and its clock as arguments. A test then drives the loop in a
`synctest` bubble with fake channels, as the `testing` skill requires.
Each later milestone adds its channel to that function.

## Tests

The loop's tests run in a `synctest` bubble with
`kubernetes/apiservertest`. The retry schedule and the outcome's
sorting are unit tests of `retrySchedule` and `passOutcome`.

- A status publish that the fake API server refuses with a `503`
  retries after one second of fake time, then two, four, and eight, and
  stops at ten, with no ticker.
- A failure that will not change by itself retries after ten seconds
  and backs off to five minutes.
- A pass that succeeds stops the timer and resets the delay, and a
  settled loop runs no pass in an hour with no wake.
- A `429` with `Retry-After: 3` retries after three seconds, and a
  step's wake waits for a `429`'s `Retry-After` too.
- A `409` followed by a write that lands records no failure.
- A transient download failure retries after ten seconds and backs off
  to two minutes. A new ask starts at once, and an ask that changes
  back while its download stops starts again with no backoff.
- A facts watch whose channel closes runs a pass and opens again on
  the next tick. An inotify reader and a uevent reader that fail close
  their channels, and a cancel does not. `settle` returns at once on a
  closed channel, and the logs relay exits. Each of init's uevent
  components walks the whole state before it waits and ends with
  `errUeventsStopped` when its listener stops, and the intent watch
  ends with an error, not with the `nil` that ends it for the boot.

## What the lab measured

`node-1` of the `lab` fleet, on 2026-10-09, under UEFI with the
virtio hardware shape:

- `make smoke-uefi` installed `node-1` from blank disks with this
  build, booted the installed disk, and reported Ready after 21
  seconds.
- A settled `node-1` logged no unfinished pass in its first minute.
- A `spec.sysctls` entry for `net.ipv4.conf.drill0.forwarding`, a
  parameter the kernel does not have, failed with `ENOENT` and sorted
  as lasting. The log line of each pass named the parameter and a
  retry due in 10.65 s, 20.81 s, 40.76 s, 1m20.89 s, 2m45.44 s, and
  then about 5 minutes, with the jitter on each. The ticker still ran
  a pass every ten seconds, so each pass doubled the delay. When the
  entry was removed, the log went quiet and `SysctlsApplied` went back
  to `True`.
- A Cluster that named a release the release server did not hold got
  a `404` on each attempt. The server's log recorded the attempts 11,
  21, 41, 85, and 131 seconds apart: the backoff of 10 seconds doubling
  to its 2-minute limit, with the jitter. The ticker alone would have
  sent about 30 requests in those five minutes, and the backoff sent
  6. `VersionConverged` read `Downloading` with the time of the next
  retry, and the operator's log stayed quiet, because a download that
  waits out its backoff is not an unfinished step.

A second drill ran on the same day with the final build, after the
review fixes:

- `make smoke-uefi` reported `node-1` Ready after 16 seconds.
- A settled `node-1` wrote no log line in its first 75 seconds.
- The `drill0` sysctl again failed with `ENOENT` as lasting, with
  retries due in 10.95 s, 20.66 s, 42.52 s, 1m23.3 s, 2m49.03 s, and
  then about 5 minutes. When the entry was removed, `SysctlsApplied`
  went back to `True` and the log went quiet.
- A `ValidatingWebhookConfiguration` whose `Service` did not exist,
  with `failurePolicy: Fail`, made the API server answer `500` to each
  write of `machines/status`. A change to `spec.sysctls` gave the pass
  a status to write. Each pass logged the `500` as transient, and the
  passes ran 1.06, 1.01, 4.10, and 5.93 seconds apart, and then every
  ten seconds. Ticker passes ran inside the 2-second and 8-second waits,
  and each of those passes doubled the delay again. The machine stayed
  `Ready` the whole time, because the heartbeat lease is not a status
  write. When the webhook was deleted, the next pass wrote the status
  and the log went quiet.
- The missing release drew attempts 10, 20, 40, and 86 seconds apart,
  and `VersionConverged` named a retry 2 minutes and 3 seconds after
  the last one. The operator logged no unfinished pass.

The tests that raised `init`'s coverage put its mount, netlink, DHCP,
and clock calls behind package variables. A third boot ran on that
tree: `make smoke-uefi` reported `node-1` Ready after 15 seconds. On
the next boot, every condition was `True`, every storage role was on
its partition, and the clock synchronized. The `drill0` sysctl again
logged retries due in 10.55 s, 21.97 s, 40.47 s, and 1m25.61 s.
