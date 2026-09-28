# A fresh agent's first version arrives late

This open problem was found in the plan 28 drill on 2026-09-02. A `Job` whose
Corrosion agent starts on a fresh claim joins the namespace's cluster
a few seconds after its first write. On the testbed the catalog pod
received that agent's versions 2 through 12, the walk's rows, within
four seconds of the join, and its version 1, the started `runs` row,
more than two minutes later. The `Job`'s wait timed out, its second
pod wrote a new version on the same claim, and the catalog pod
reported that version at once. The second pod's agent had a peer from
its first write. The first pod's agent had no peer at its first
write.

The echo timeout is how long a `Job` waits for the catalog pod to
report its run back. The cost of this problem is one echo timeout, two
minutes by default, on the first walk of every new `Library`. After the
enrichers of plan 29 exist, the cost is also one timeout on the first
run of every new worker's claim. No rows are lost, because the next pod
runs on the same claim and carries them.

There are two possible fixes, and neither is measured. The `Job` could
delay its first write until its agent has a peer. That needs a signal
that the agent gives today only on its admin socket. Alternatively,
someone could read Corrosion's sync code in `crates/corro-agent` to
find why sync does not pull a fresh actor's first version with the
other versions. The catalog pod's log for that time is the evidence to
start from: the
agent `d98bd6b3` joined at 18:30:58, and its versions 2 to 12 were
buffered by 18:31:02 with the warning "did not apply buffered changes"
on each.

## The echo timeout on a short walk, 2026-09-15

The same timeout, "the catalog did not report the scan run within
2m0s", has a second cause, which does not need a fresh agent. On a
cluster with three libraries, it occurred on about one franchises walk
in forty over two weeks, and never on a movies or series walk. The
franchises walk takes one second. Its finished run row is therefore one
broadcast, made seconds after the agent joined. At the same time, the
movies and series walks start in the same minute and flood the
namespace's gossip.

Every walk that the catalog pod reported was reported within the same
second. Every walk that timed out was never reported at all. The failed
agent had a peer from its first write. It had no path to the catalog
pod: its broadcasts to one busy sibling agent timed out at the
five-second deadline for the whole wait, and the
catalog pod's periodic sync did not reach the new actor for two and a
half minutes, because a sync picks three of six random members sorted
by the versions it already needs, and it has no version information
about an actor that it has just met.

Three changes fix this cause. First, the Job writes its finished run
row again every ten seconds while it waits, with a later finish time.
Each write is a new version with a new broadcast and a gap that the
catalog can detect (`runsnudge.go`). Second, the three walks of a
namespace each run in a different minute. Third, the Corrosion fork
gets a setting for how long it remembers a member that left. A pod
never returns under the same identity, and by default foca announces
to remembered members for two days.

## Closed by the confirmation, 2026-09-16

The echo wait is gone. A `Job` no longer compares two copies' item and
file counts. It writes its finished `runs` row, writes it again naming the
agent that applied that write and the db version it was given, and
waits for a `confirmations` row from a `confirmer` container in a
catalog pod. That container reads `crsql_db_versions` and
`__corro_bookkeeping_gaps` on its own copy, so it confirms the run only
after its copy has every version of the writing agent up to the one
that the run names.

This fixes both causes above. A `Job` whose agent joins late is
confirmed as soon as its versions arrive. Before, it was confirmed only
when two counts happened to agree. A `Job` whose copy started empty is
now confirmed. Before, its own low counts could never match any report.
The repeated write every ten seconds stays, because it sends a lost
broadcast to a new set of peers.
