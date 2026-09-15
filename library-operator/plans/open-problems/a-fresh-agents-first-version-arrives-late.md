# A fresh agent's first version arrives late

Open problem, found in the plan 28 drill on 2026-09-02. A `Job` whose
Corrosion agent starts on a fresh claim joins the namespace's cluster
a few seconds after its first write. On the testbed the catalog pod
received that agent's versions 2 through 12, the walk's rows, within
four seconds of the join, and its version 1, the started `runs` row,
more than two minutes later. The `Job`'s wait timed out, its second
pod wrote a new version on the same claim, and the catalog pod
reported that one at once. The second pod's agent had a peer from its
first write. The first pod's did not.

The cost is one echo timeout, two minutes by default, on the first
walk of every new `Library`, and on the first run of every new
worker's claim once the enrichers of plan 29 exist. The rows are never
lost. The next pod carries them.

Two answers are possible and neither is measured. The `Job` could hold
its first write until its agent has a peer, which needs a signal the
agent gives only on its admin socket today. Or Corrosion's sync could
be read for why a fresh actor's first version is not pulled with the
rest, which is a question for its source, in `crates/corro-agent`. The
catalog pod's log for the window is the evidence to start from: the
agent `d98bd6b3` joined at 18:30:58, and its versions 2 to 12 were
buffered by 18:31:02 with the warning "did not apply buffered changes"
on each.

## The echo timeout on a short walk, 2026-09-15

The same timeout, "the catalog did not report the scan run within
2m0s", has a second cause that is not a fresh agent. On a cluster
with three libraries it hit about one franchises walk in forty over
two weeks, and never a movies or series walk. The franchises walk
takes one second, so its finished run row is one broadcast made
seconds after the agent joined, while the movies and series walks
start on the same minute and flood the namespace's gossip.

The echo is binary. Every walk that echoed did so within the same
second, and every walk that timed out never echoed at all. The failed
agent had a peer from its first write. What it did not have was a
path to the catalog pod: its broadcasts to one busy sibling agent
timed out at the five-second deadline for the whole wait, and the
catalog pod's periodic sync did not reach the new actor for two and a
half minutes, because a sync picks three of six random members sorted
by what it already knows it needs, and it knows nothing about an actor
it has just met.

Three changes answer it. The Job writes its finished run row again
every ten seconds while it waits, with a later finish time, so each
write is a new version with a fresh broadcast and a gap the catalog
can name (`runsnudge.go`). The three walks of a namespace run on
minutes of their own. And the Corrosion fork takes a knob for how long
a member that left is remembered, because a pod never returns under
the same identity and foca announces to remembered members for two
days by default.
