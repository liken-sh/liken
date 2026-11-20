# 94, Metrics for the operators that serve none

Proposed on 2026-11-20, as a stub. Nothing is built.

[Root plan 65](completed/65-prometheus-metrics.md) is the contract
for the Prometheus metrics of every long-running process: the layers
that each process serves, the names, the labels, and the port.
`firmware-operator` and `people-operator` each run a `Deployment`,
and neither one serves metrics. Neither has a `metrics.go`, a
`reference/metrics.md` page in its manual, or a `deploy/monitoring/`
directory.

Plan 65 gives each component one prefix for its metric names, and it
has no prefix for either operator. `firmware-operator` was added on
2026-10-19, after the plan was built. The plan says that
`people-operator` ships a CRD and no program, and it runs a
`Deployment` today.

So a session that is asked for one metric in either operator has no
prefix to use, and no place in the component to add the metric. It
has to choose the prefix and build the layers that every process
serves before it can add the metric it was asked for.

## What the design has to settle

- The prefix of each operator.
- Which metrics of its own each operator serves. `firmware-operator`
  scans machines, rehearses updates, and applies them, and each of
  those can fail. `people-operator` reads each person's picture and
  writes a thumbnail.
- Where the list of prefixes is kept, so that the next new component
  finds it. Plan 65 is completed, and a completed plan does not
  change.

The detail of each operator's metrics will go in a plan of that
operator.
