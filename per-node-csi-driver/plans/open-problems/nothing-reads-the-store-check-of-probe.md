# Nothing reads the store check of `Probe`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`Probe` in `per-node-csi-driver/identity.go` reports ready only when
`storeIsWriteable` can create and delete a file in the store. Its
comment gives the reason: every copy is a directory under the store,
so a store that refuses writes makes every publish fail. The check has
effect only when some process calls `Probe` and acts on `Ready`.

The node plugin's pod in `deploy/node.yaml` runs two containers: the
driver and `csi-node-driver-registrar`. It runs no `livenessprobe`
sidecar, which is the upstream sidecar that calls `Probe`, and neither
container has a liveness or readiness probe. The review did not find a
call to `Probe` in the kubelet or in the registrar, but it did not
check their source. If neither calls it, a read-only or failing store
leaves the pod Ready and running, and the first sign of the fault is a
failed `NodePublishVolume` for a pod. `TestProbeIsReadyWhenTheStoreIsWriteable`
and `TestProbeIsNotReadyWhenTheStoreIsGone` in `identity_test.go` test
the check, but not any consumer of it.

## What would confirm it

Read the source of the kubelet's CSI plugin and of
`csi-node-driver-registrar` v2.18.0 for a call to `Probe`. On the lab,
make the store read-only on the node, and check whether the node
plugin's pod stays Ready and keeps running.

## What a fix has to decide

Whether the check should act: add the `livenessprobe` sidecar with an
HTTP probe on the driver container, which restarts the driver on a
failed check, or a readiness probe, which only marks the pod not
ready. Or whether to delete the check and let `Probe` return ready,
because a failed publish already posts `PerNodeMountFailed` on the pod.
