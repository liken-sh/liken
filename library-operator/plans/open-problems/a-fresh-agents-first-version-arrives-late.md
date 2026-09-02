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
