# `DaemonSet` tolerations cover more than the controller adds

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it. The upstream
behavior below is from the reviewer's memory, not from the Kubernetes
source.

`skip` in `crates/transect-ground/src/placement/mod.rs` predicts
whether the DaemonSet controller makes a pod for a node. It passes the
keys in `TOLERATED_BY_DAEMON_SETS` to `check`, and `check` ignores a
taint with one of those keys at any effect.

Upstream, `AddOrUpdateDaemonPodTolerations` in the DaemonSet controller
adds each toleration with one effect: `NoExecute` for `not-ready` and
`unreachable`, and `NoSchedule` for the others. It adds the
`network-unavailable` toleration only to a pod with `hostNetwork:
true`. If that is correct, `skip` returns `None` for two cases where
the pod does not run:

- a node with the taint `node.kubernetes.io/not-ready:NoSchedule`,
  which the node lifecycle controller sets, for any `DaemonSet`.
- a node with `node.kubernetes.io/network-unavailable`, for a
  `DaemonSet` whose pods do not use the host network.

## What would confirm it

Read `AddOrUpdateDaemonPodTolerations` in
`kubernetes/pkg/controller/daemon/util/daemonset_util.go` at the
release that the project tracks. Then a unit test of `skip` with a node
that has `not-ready:NoSchedule` returns `None`, where the controller's
tolerations do not cover the taint.

## What a fix has to decide

Whether `TOLERATED_BY_DAEMON_SETS` holds pairs of key and effect, and
whether `skip` reads `hostNetwork` from the template for the
`network-unavailable` key.
