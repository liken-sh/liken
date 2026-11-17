# A report from a catalog pod that just started counts too little

The operator decides what a `Library` needs from the Library's report,
which the reporter beside a catalog pod builds from that pod's copy of
the catalog and publishes retained. Twice on 2026-11-17, on a home
cluster, the operator started `Job`s that had nothing to do, in the
first minute after a rollout that restarted the operator and all three
catalog pods at once:

- After one rollout, four `Job`s that fill gaps for one `Library` in 45
  seconds, each "because gaps are open".
- After the next, a placing `Job` for each of two Libraries, "because
  the catalog holds 0 placements, and the last placing wrote 1494" and
  "wrote 8032". The catalog held every placement: each `Job` placed
  every reading, and wrote 0 rows and deleted 0.

So the report that the operator read counted no placement an hour after
the placing had written them all. Each placing `Job` ended in under two
minutes, and no further one started, so the cost is a `Job` for each
`Library` at each such restart.

The cause is not confirmed. The reporter builds a report as soon as its
agent answers, and nothing in the report says how far the agent's copy
is behind the other members. The catalog claim is a volume of
`per-node-csi-driver`, which gives each node a directory of its own. A
catalog pod that starts on another node than before would read that
node's older copy until it has synced with its peers, and nobody has
checked on which nodes the pods ran before and after.

The placing trigger holds the rule "the catalog holds fewer placements
than the last placing wrote" back while the placing `Job` is still
listed, for a report that was built before the `Job`'s rows arrived
(`titlesPlaceCause` in `titlesplace.go`). That guard does not cover a
report from a copy that is behind for another reason. The rule is how a
catalog that is built again gets its placements back, so it cannot wait
for the report to name the placing's own run: a catalog that is built
again never names it.

A fix belongs with the report and not with each trigger, because the
gap `Job`s show the same fault: a reporter that publishes no report, or
marks the report, until its copy holds the last write that a catalog
pod confirmed.
