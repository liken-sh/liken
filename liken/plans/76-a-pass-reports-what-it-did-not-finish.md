# 76. A pass reports what it did not finish

Milestone 76. Proposed 2026-10-09. The first of five milestones,
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
drain's deadline, a `429`'s `Retry-After`, and the download's next
attempt each use it. The recorder keeps the earliest T.

**One wake-at timer.** When a pass ends, the loop sets one timer for
the earliest moment the outcome asks for, and the timer wakes a pass.
Each pass computes the moment again from the state it reads, so a
restart of the operator, a withdrawn grant, or a wall clock that steps
back needs no special case.

**The retry delay.** A pass that ends with failures asks for a retry.
The delay sorts each failure by kind:

- A transient failure (`5xx`, `429` with no `Retry-After`, a timeout, a
  network error, `EAGAIN`, `EBUSY`) starts at one second and doubles
  on each pass that still has a transient failure, up to ten seconds,
  the pace the ticker gives today. So the change can only make such a
  retry sooner.
- A failure that will not change by itself (`400`, `403`, `422`,
  `ENOENT`, `EINVAL`) starts at ten seconds and doubles up to five
  minutes, so a misconfigured machine does not retry every ten seconds
  forever. An edit by a person sends a watch event, and that pass
  tries again at once.

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
closed channel tells the caller the watch died. The loop opens the
watch again and runs a pass, following the three steps of the rule
for events: subscribe, read the whole state, and on failure do both
again. init's callers of the same readers handle the close in the same
way.

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

In a `synctest` bubble with `kubernetes/apiservertest`:

- A status publish that the fake API server refuses with a `500`
  retries after one second of fake time, then two, then four, and stops
  at ten.
- A `422` retries after ten seconds and backs off to five minutes.
- A pass that succeeds stops the timer and resets the delay.
- A `429` with `Retry-After: 3` retries after three seconds.
- A transient download failure retries after ten seconds and backs off
  to two minutes. A new ask starts at once.
- A uevent channel and an inotify channel that close make the loop open
  them again and run a pass.
