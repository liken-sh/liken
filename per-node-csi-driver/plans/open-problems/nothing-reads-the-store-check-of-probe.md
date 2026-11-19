# Nothing reads the store check of `Probe`

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it. On 2026-11-19 the upstream sources were read, and
neither the kubelet's CSI client nor the registrar calls `Probe`.

`Probe` in `per-node-csi-driver/identity.go` reports ready only when
`storeIsWriteable` can create and delete a file in the store. Its
comment gives the reason: every copy is a directory under the store,
so a store that refuses writes makes every publish fail. The check has
effect only when some process calls `Probe` and acts on `Ready`.

The node plugin's pod in `deploy/node.yaml` runs two containers: the
driver and `csi-node-driver-registrar`. It runs no `livenessprobe`
sidecar, which is the upstream sidecar that calls `Probe`, and neither
container has a liveness or readiness probe. Neither the kubelet's CSI
client nor the registrar calls `Probe`, so a read-only or failing store
leaves the pod Ready and running, and the first sign of the fault is a
failed `NodePublishVolume` for a pod. `TestProbeIsReadyWhenTheStoreIsWriteable`
and `TestProbeIsNotReadyWhenTheStoreIsGone` in `identity_test.go` test
the check, but not any consumer of it.

## Evidence

- In `pkg/volume/csi/csi_client.go` and `csi_plugin.go` on the
  `master` branch of kubernetes/kubernetes, read on 2026-11-19, the
  only match for `Probe` is `ProbeVolumePlugins`, the function that
  registers the volume plugin. The kubelet's CSI client sends no
  `Probe` request.
- In `cmd/csi-node-driver-registrar/main.go` and `node_register.go` of
  `node-driver-registrar` at the tag `v2.18.0`, the matches for
  `Probe` are the `kubelet-registration-probe` mode, which checks the
  registration and not the driver. The registrar asks the driver only
  for its name, with `csirpc.GetDriverName`.
- `deploy/node.yaml` runs no `livenessprobe` sidecar.

No unit test can show that nothing calls `Probe`. A run on the lab
that makes the store read-only on the node, and checks whether the
node plugin's pod stays Ready and keeps running, would show the
effect.

## What a fix has to decide

Whether the check should act: add the `livenessprobe` sidecar with an
HTTP probe on the driver container, which restarts the driver on a
failed check, or a readiness probe, which only marks the pod not
ready. Or whether to delete the check and let `Probe` return ready,
because a failed publish already posts `PerNodeMountFailed` on the pod.
