# 102. A restarted cluster operator trusts each machine it hears

Milestone 102. Proposed and built on 2026-11-21. The drills ran in
the lab on 2026-11-21, and
[What the lab measured](#what-the-lab-measured) records them. They
found one limit, the last item in [What still waits](#what-still-waits).

## The problem

A new `cluster-operator` process has no record of the heartbeat
Leases. The first `renewTime` it reads for a Lease shows only that the
machine renewed at some time in the past, so a machine that lost power
a minute earlier still has a Lease and a status that reads `Ready`.
Milestone 80 answered this with a wait: for `HeartbeatStaleAfter`
after its first read of the Leases, the process granted no turn and
reclaimed no grant ([milestone 80](80-the-machine-operator-idles.md),
"cluster-operator holds the rollout for its first 40 seconds").
[Milestone 82](82-the-cluster-records-each-reboot-turn.md) held the
confirm of a recorded turn for the same time, because a grant on a
machine that is down makes it read `Updating` and hides that it is
`Lost`. [Milestone 88](88-disruptions.md) measured the cost: a turn
ended 40 to 55 seconds after its machine was back whenever the drain
had evicted `cluster-operator`.

The wait starts again with each process. A `cluster-operator` that
restarts more often than `HeartbeatStaleAfter` never ends it, and so
never reclaims a finished turn and never grants another. On the `lab`
fleet, a separate fault replaced the `cluster-operator` pod every 10
to 15 seconds during a rollout. The first machine finished its turn,
was `Ready` on the target release, and kept the turn for more than 30
minutes, while the other machines waited. A crash loop, an eviction
loop, or a node under memory pressure has the same effect.

## The design

The wait distrusts one kind of evidence: the first `renewTime` that a
process reads for a Lease. A change of `renewTime` after that is a
write that the machine made while this process was reading, which is
the same evidence that judges every heartbeat after the wait. So the
wait applies to each machine separately instead of to the fleet.

**A machine is heard** when this process has seen its Lease's
`renewTime` change from a value that this process read before. The
heartbeat record keeps a flag with each sighting. A Lease that is
deleted and created again is not heard until it renews again.

**While the record is younger than `HeartbeatStaleAfter`,** a pass
treats each machine that it has not heard as possibly down, whatever
its status says:

- It counts against every budget that selects it, when the pass
  decides whether a budget has room. The budget's status does not
  count it, because the status reports the machines known to be out
  of service.
- If it is a leader, no other leader gets a turn.
- It gets no turn, no confirm, and no second grant of a recorded turn.
- Its grant is not reclaimed. The grant counts against the budget, as
  before.

A machine that the pass has heard is judged as a process past the
wait would judge it: by its status, while its last sighting is fresh.
Its finished turn is reclaimed, a recorded turn is confirmed, and it
gets a turn when the budgets allow. After `HeartbeatStaleAfter`, the
rule ends, because a machine that has not renewed by then reads
`Lost`.

**The first renewal of each Lease starts a pass.** The Lease watch
starts no pass for later renewals, because the ticker measures a
heartbeat's age. The first renewal can let the pass grant or end a
turn, and without a wake the pass would wait for the next tick. Each
Lease wakes the loop at most once in the life of a process.

**The messages.** The `Granted` condition of a waiting Disruption
says that `cluster-operator` grants the machine no turn until it sees
a renewal of the machine's heartbeat Lease, and gives the time of the
first read. A budget refusal names the machines it counts as out until
`cluster-operator` sees a renewal. The Cluster's `Progressing` message
names every machine not yet heard, and the time when the wait ends.

## The guarantees it keeps

- **A machine that is down never counts as up.** A machine that is
  down writes no Lease, so no process hears it, however often the
  program restarts. Every decision that would count it as up, a grant
  to another machine or a reclaim of its own turn, counts it as out
  instead.
- **The leader limit holds.** A leader that the pass has not heard
  blocks every other leader's turn.
- **No confirm hides a `Lost` machine.** A confirm writes a grant
  only to a machine that the pass has heard.
- **The budget holds against a former leader.** The rules of
  `status.rebootTurns` from milestone 82 do not change. An entry still
  leaves on the API server's answer alone.

## What still waits

- A process that leads for less time than one renewal interval of
  every machine grants nothing, because it hears no machine. A
  process whose life is shorter than the wait but longer than that
  interval moves the rollout, when the process before it released the
  election Lease.
- When the elected process exits without releasing its election
  Lease, no process in another pod can lead until it has run for the
  Lease's duration, `election.Duration`, which is 30 seconds
  (`kubernetes/election/election.go`). Each process competes as a new
  candidate, with the pod's name and a random suffix as its identity,
  and client-go counts the duration from a candidate's first read of
  the Lease. A container restarted in the same pod takes the Lease at
  once when its last renewal is at least 30 seconds old
  (`abandonedByThisPod` in `kubernetes/election/lock.go`), and not
  sooner. A pod that is deleted or evicted gracefully releases the
  Lease on `SIGTERM`. A process that stops while the API server is
  unreachable cannot write the release. In the lab this happened when
  a leader rebooted while `cluster-operator` ran on the other of two
  leaders, which leaves the API server unreachable for about 20
  seconds. With a pod deletion every 5 seconds, the rollout then
  stopped for 12 minutes. The Cluster's status showed nothing new in
  that time, and the only sign was the election Lease, which named a
  pod that no longer existed. After the deletions stopped, a new pod
  ended the turn 36 seconds after it started. A sequence of new pods
  that each live less than 30 seconds never leads.
- A pass that cannot read the Machines, the Leases, or the requests
  stops before it decides anything (`reconcileFleet` in `fleet.go`),
  because a decision without them could exceed a budget. A reclaim
  does not depend on the `Cluster` write: `carryOutRollout` sends it
  when that write failed or an older schema dropped the turns.

## Alternatives set aside

- **Reclaim a finished turn without hearing the machine.** The reclaim
  would be safe for the budget, because the unheard machine still
  counts as out after it. It would also remove the grant that keeps a
  machine that went down on its turn reading `Updating`, and so it
  would replace a stalled rollout with a `Lost` machine. Hearing the
  machine costs at most one renewal interval.
- **Carry the start of the wait across restarts.** The `Progressing`
  message already gives the time of the first read. A new process has
  new sightings, though, and a first sighting looks fresh whenever it
  was made, so an older start would trust the evidence that the wait
  distrusts.
- **Carry the sightings across restarts in the Cluster's status.** A
  sighting time is on the clock of the process that saw it, and the
  next process may run on another node with another clock. Heartbeats
  are timed on one clock so that no age mixes two clocks
  ([milestone 80](80-the-machine-operator-idles.md)).
- **A shorter wait.** It would only lower the restart rate that stops
  a rollout, and a machine that is down for less than the wait would
  count as up.

## How it is proved

The tests in `cluster-operator/`:

- `restartturns_test.go` restarts `cluster-operator` every 15 seconds
  of the fake clock with real elections, in a fleet where the first
  machine has finished its turn and the second waits. When every
  machine renews its Lease, the finished turn is reclaimed and the
  second machine gets a turn. When the third machine is down and its
  status reads `Ready`, the finished turn is reclaimed and the second
  machine gets no turn. Written first, both rows failed on the code
  before this milestone with the first machine still holding its turn.
- `rolloutturns_test.go` has rows for a young record: a heard machine
  is reclaimed, confirmed, and granted; a machine not heard keeps its
  grant and fills the budget; a leader not heard keeps the other
  leader from a turn.
- `rollouthold_test.go` checks that a process grants a turn one
  renewal after it starts when every machine renews, and none in the
  wait when the leader does not renew.
- `heartbeats_test.go` checks which writes make a machine heard, and
  that only the first renewal of each Lease starts a pass.

The drill, on the `lab` fleet: start a rollout to a new release with
the default budget, and once the first machine has its turn, delete
the `cluster-operator` pod every 20 seconds until the rollout ends. A
machine that finishes its turn loses it within one renewal interval
of the next pod's start, and the next machine gets a turn. Repeat with
a deletion every 12 seconds to find how short a run can be and still
move the rollout. Then repeat with one worker powered off before the
first deletion: the finished turn still ends, no other machine gets a
turn, and the `Progressing` message names the powered-off worker.

## What the lab measured

The drills ran on 2026-11-21 on lab guests. Each rollout moved
between two lab releases built from one tree. The Helm controller was
on, so that an unrelated manifest loop did not disturb the drills. The
run without this milestone used commit ca100afa7, the tree that this
milestone changes. Every rollout used the default budget.

| Build | Machines | Pod deleted | Result |
| --- | --- | --- | --- |
| ca100afa7 | 2 leaders, 1 follower | every 20 s, 28 times over 10 min 46 s | The first machine finished at about 52 s and held its turn through every deletion: 62 samples showed the same turn. No other machine got a turn. The next turn came 42 s after the last deletion. |
| This milestone | 3 | every 20 s | The rollout finished in 2 min 08 s. Turns were held 14 s, 32 s, and 47 s. A finished turn ended 5 to 11 s after a new pod started. |
| This milestone | 3 | every 12 s | The rollout finished in 2 min 18 s. |
| This milestone | 3 | every 8 s | The rollout finished in 2 min 48 s. |
| This milestone | 3 | every 5 s | One turn moved, and then the rollout stopped for 12 minutes, for the reason in the last item of [What still waits](#what-still-waits). |
| This milestone | 4 | never | The rollout finished in 1 min 46 s. Turns were held 14 s, 33 s, 20 s, and 23 s. Samples 2 s apart never showed more than one leader cordoned. |

In the run with no deletions, the last leader's reboot replaced the
`cluster-operator` pod, and the new pod ended that leader's turn 6
seconds after it started. In the same situation without this
milestone, the turn was held 80 seconds, because the new pod waited
the full 40 seconds of `HeartbeatStaleAfter`.

With a follower powered off while its status still read `Ready`, the
pod was deleted every 20 seconds for 9.5 minutes. A live follower's
finished turn still ended, after it had been held 19 seconds. No
machine got a new turn in that time, because the default budget
allows one machine out of service and the powered-off machine, which
no process heard, took that place. The `Progressing` message named the
powered-off machine in every sample. The machine never read `Lost`
while the deletions ran, because no process lived for
`HeartbeatStaleAfter`, and no pass counted it as up. 51 seconds after
the deletions stopped, `cluster-operator` posted `MachineLost` and
`BudgetFull`.

The `MachineLost` Event's message gave the time that this process
first read the machine's Lease, not the machine's last renewal. This
milestone did not change that message.
