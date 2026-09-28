# 69, An agent's exit inside its grace period

Built on 2026-09-27. It closes the open problem "Slow agent shutdown".
Every pod that runs a catalog or progress agent now has a termination
grace period of 90 s, up from 60 s. The number comes from three
measurements: a delete on `liken-1`, eight days of agent exits on a home
cluster, and a drill that reproduced the slow exit on a workstation.
The slow exit is in Corrosion, not in this operator's code, and a fix
for it belongs in the fork.

## The problem

The proof of concept saw a busy agent take more than 30 s to exit on
`SIGTERM`. Plans 03 and 06 asked for a longer grace period on every pod
that runs an agent, and `pod.go` set it to 60 s. The open problem asked
what the agent does in that time, whether a kill during a sync leaves a
file the next start recovers from, and whether the delay grows with
the cluster.

## What the agent does after SIGTERM

Corrosion stops its tasks on `SIGTERM` and then waits for the ones it
counts, for at most 60 s: 600 checks, 100 ms apart, in
`wait_for_all_pending_handles` in its `spawn` crate. It logs `Waiting on
N spawned futures` once a second while it waits, and `All spawned
futures done!` when the wait ends early. Then the process exits.

At rest, the wait is about 5 s. Most of it is the gossip task telling
the agent's peers that it leaves.

The slow exits come from one task, `apply_fully_buffered_changes_loop`,
which applies versions whose changes arrived in several parts. The loop
checks for `SIGTERM` only before it reads its next trigger. Two cases
keep it busy after `SIGTERM`:

* **A list after a schema change.** When an agent applies a changed
  schema at start, it sends `ApplyTrigger::SchemaChanged`, and the loop
  reads every fully buffered version from `__corro_seq_bookkeeping` and
  applies the whole list without a check between versions. An agent
  that restarts in the middle of a first sync holds many buffered
  versions, so on a new image it applies for as long as the list takes,
  and the agent exits at the end of its 60 s wait with the work
  unfinished.
* **One apply behind a slow commit.** The loop's current version waits
  for the write connection. On a node with a slow disk, one version of
  244 rows took 33 s behind a commit of 12.7 s, and the agent exited
  when that version was done.

The fix for the first case is a check for `SIGTERM` between versions in
the fork's `util.rs`, which this repository does not change.

The delay does not grow with the cluster. It grows with the backlog of
buffered versions on the agent's own claim and with the speed of its
disk, and the 60 s wait caps it.

A kill during a sync loses no committed row. On the home cluster the
kernel killed the agent of a first sync several times, and each restart
continued from the `state.db` on the claim until the sync completed.
SQLite rolls back an unfinished transaction on the next open.

## The design

`scannerGracePeriod` in `pod.go` is 90. The catalog pod, the progress
pod, every library `Job`, and the cleanup `Job` use it. The agent is a
native sidecar, so it receives `SIGTERM` only after the pod's other
containers exit, and both parts come out of the same period. 90 s
covers the agent's own wait of 60 s, which includes the 5 s it takes to
leave its peers, the other containers' exit, and a margin of about
25 s. The slowest exit the drill below measured was 61.3 s.

The 60 s are not a hard bound. The version the loop is applying when
the wait ends runs in `block_in_place`, and the process exits after it
returns: 1.3 s in the drill. One version can take as long as
`sql_tx_timeout`, 60 s, and on the home cluster one version waited 33 s
behind a slow commit. So an agent whose wait ends during such a version
can pass 90 s, and the kubelet kills it. The kill costs that one
transaction, which the next start applies again.

The Jellyfin pods run no agent and use the same constant. A grace
period is a ceiling, and a pod whose containers exit on `SIGTERM` ends
before it.

`agentExitWait` in `pod.go` records Corrosion's 60 s. A test reads the
grace period of each pod that runs an agent and fails if the period is
not longer than that wait.

The screen pods keep their 15 s. The browser does not exit on
`SIGTERM`, so it uses the whole period, and the kubelet then gives the
native sidecars its minimum of 2 s. In the eight days of logs, 103 of
118 screen agents stopped logging about 2 s into their wait, which fits
that kill. A screen's agent holds only
rows its peers replicate to it, and its claim keeps what it synced, so
the kill costs a short re-sync.

## What was set aside

* **A longer period, such as 150 s.** It would also cover one slow
  version at the end of the wait. No exit in the eight days of logs or
  in the drills needed more than 61.3 s, and a longer period delays
  every forced stop of a wedged agent by the same amount. The fix in
  the fork removes the case.
* **A fix in this operator's code.** Nothing the operator runs delays
  the exit. The delay is Corrosion's loop over buffered versions.

## How the work is proved

**A delete at rest on `liken-1`**, on 2026-09-27, in release
2026.09.27-004. `kubectl delete pod` of the catalog pod was sent at
23:25:31.13. The `confirmer` and `reporter` containers exited within
the same second. The agent logged `Waiting on 1 spawned futures` from
23:25:32.69 to 23:25:36.74, then `foca runtime loop is done, leaving
cluster` and `All spawned futures done!` at 23:25:36.84, and exited
with code 0. The pod was gone at 23:25:37.34, 6.2 s after the delete.

**Eight days of exits on a home cluster**, read from the agents' logs
from 2026-09-20 to 2026-09-27. Each exit is one run of `Waiting on`
lines in one container. The last column counts the exits whose log
ends without `All spawned futures done!`.

| Pods | Exits | Median | 95th percentile | Longest | No end line |
|---|---|---|---|---|---|
| library `Job`s | 799 | 5.1 s | 10.1 s | 59.2 s | 1 |
| catalog and progress pods | 82 | 5.1 s | 7.1 s | 28.5 s | 0 |
| screens | 118 | 2.0 s | 5.2 s | 9.1 s | 103 |

The one `Job` agent with no end line applied buffered versions, up to
10,000 rows each, for the whole 60 s after its `SIGTERM`. At that point
both its own wait and the pod's grace period of 60 s ended, and the log
does not show which one stopped it. The next longest, 36 s, was the
agent of the gaps `Job` in plan 68, after its restart in the middle of a
first sync: one buffered version waited 33 s for a slow commit. The
design section explains the screens.

**The slow exit, reproduced on a workstation** with two agents from the
image of release 2026.09.27-004 in `docker`, and a synthetic catalog of
600,000 rows. `docker stop` sends `SIGTERM` and waits up to 120 s.

| Case | Exit after `SIGTERM` |
|---|---|
| first sync, 70 s in, 6,920 changesets queued | 5.7 s |
| first sync, 150 s in, applying buffered versions | 5.4 s |
| first sync, 250 s in, applying buffered versions | 5.4 s |
| first sync with the settings of plan 68, 70 s in | 5.3 s |
| first sync with the settings of plan 68, 250 s in | 5.4 s |
| killed 100 s into a first sync, restarted on the same schema, stopped 20 s later | 5.4 s |
| killed 100 s into a first sync, restarted with one more table in the schema, stopped 20 s later | 61.3 s |

The last case is the slow exit. The agent logged `Processing buffered
changes` until it exited, with 60 lines of `Waiting on 1 spawned
futures`, and it exited with code 0 at the end of its own wait. A busy
sync alone does not slow the exit. A changed schema over a backlog of
buffered versions does.

Still owed: the fix in the fork, and a drill after it that restarts an
agent on a changed schema over a backlog and times its exit.
