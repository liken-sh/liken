# The machine of Prometheus is read on a timer from its host name

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

The loss view says when a lost machine runs Prometheus. `poll` in
`crates/transect-ground/src/loss/blind/poll.rs` reads Prometheus's host
name from `/api/v1/status/runtimeinfo` every `PROMETHEUS_POD_INTERVAL`
(60 s, in `crates/transect/src/main.rs`). `blinded` in
`crates/transect-ground/src/loss/blind/mod.rs` then finds the running
pod whose name equals that host name, and reads its node. This has
these faults:

- The root `AGENTS.md` says that a timer that reads state again to find
  a change is a defect, unless a comment at the timer gives the failure
  it covers. The comment at `PROMETHEUS_POD_INTERVAL` says that the
  name changes only when the pod is replaced, which is a change that
  the pod watch that `transect` already runs reports.
- A Prometheus pod with `hostNetwork: true` reports its node's host
  name, not its pod name, so no pod matches and the loss view does not
  say that Prometheus is lost.
- `blinded` compares only the pod name, not the namespace, so a pod of
  the same name in another namespace can match.

## The reproduction

A test gave `blinded` one running pod, `metrics/prometheus-0` on the
machine `beta` with `hostNetwork: true`, the host name `beta` that such
a pod reports, and a loss of `beta`. It wanted Prometheus reported on
`beta`, and failed with:

```
the sources on lost machines are on [], want Prometheus on beta, where its pod runs
```

The timer is in the code: `PROMETHEUS_POD_INTERVAL` in
`crates/transect/src/main.rs` is 60 seconds, and `main` passes it to
`loss::blind::poll::poll`. No test was written for the pod of the same
name in another namespace, because `Sources` holds no namespace, so
the right answer depends on the decision below.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

How should `transect` find the Prometheus pod from the state of the
cluster with no timer, for example from the `Service` that the
`MetricsSource` address names, and what should the loss view say when
Prometheus runs outside the cluster?

How to find the Prometheus pod from the cluster's state with no timer,
for example from the endpoints of the `Service` that the
`MetricsSource` address names, and what to do when the address is
outside the cluster.
