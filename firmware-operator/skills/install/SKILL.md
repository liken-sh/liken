---
name: install
description: "Install firmware-operator, the Firmware and FirmwareCatalog resource definitions, the lvfs FirmwareCatalog, and the scan image from its kustomize base, confirm that one copy is the leader, and remove it. Use when a cluster has USB devices that need firmware from the host, when a cluster should find firmware updates, when running a development build, or when removing the operator."
---

This skill is the guide at https://liken.sh/firmware/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Install the operator

`firmware-operator` scans each machine for firmware that can be updated,
and loads firmware into USB devices that need it from the host. At the
end of this guide it runs in `liken-system`, it holds its `Lease`, and
the cluster accepts `Firmware` and `FirmwareCatalog` objects. You need
`kubectl` with cluster-admin rights on a `liken` cluster. The operator
reads the devices that `machine-operator` publishes, so it does nothing
useful on another kind of cluster.

Add the base in the operator's `deploy/` directory to your own
kustomization, and pin `<ref>` to a release tag. A pinned ref installs
the same resource definitions and the same operator image every time
you apply it.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/liken-sh/liken//firmware-operator/deploy?ref=<ref>
```

The base contains:

- The `Firmware` and `FirmwareCatalog` resource definitions. Both are
  cluster-scoped.
- The `firmware-operator` `ServiceAccount` and its `ClusterRole`. The
  role reads every `Firmware`, `FirmwareCatalog`, `ResourceSlice`, and
  `Machine`. It writes the status of each `Firmware` and
  `FirmwareCatalog`, and creates and deletes the `Firmware` objects that
  the operator discovers. It creates, patches, and deletes the
  `Disruption`s that request a machine's reboot turn. It posts `Event`s
  about each `Firmware` and `FirmwareCatalog` in the `default`
  namespace.
- A `Role` in `liken-system` that creates, reads, and deletes the upload
  pods and their `ResourceClaim`s, and the scan, rehearsal, and apply
  pods. It also patches the annotation that tells an apply pod to
  write, and reads the logs of the scan, rehearsal, and apply pods.
- A `Role` in `liken-system` that creates, reads, and renews the `Lease`
  named `firmware-operator`.
- The `firmware-operator` `Deployment`, with one replica. Its pod
  tolerates the `node.kubernetes.io/unschedulable` taint, so it keeps
  running on a machine that it has drained.
- The `DeviceClass` `firmware-upload`, which selects the USB devices
  that `liken.sh` publishes. The claim of each upload refers to it.
- The `NetworkPolicy` `firmware-upload` in `liken-system`, which denies
  all traffic to and from the upload pods.
- The `NetworkPolicy` objects `firmware-scan`, `firmware-rehearsal`, and
  `firmware-apply` in `liken-system`. They allow the scan, rehearsal,
  and apply pods to send traffic only to DNS (port 53) and to TCP port
  443, and allow no incoming traffic.
- The `FirmwareCatalog` `lvfs`, which points to the LVFS catalog at
  `https://cdn.fwupd.org/downloads/firmware.xml.zst`.

The operator runs a scan pod on each machine from the image
`ghcr.io/liken-sh/firmware-operator-scan`, at the same tag as the
operator's own image, so the tag you pin applies to both. If you pin the
operator image by digest, it has no tag to copy, so set the
`--scan-image` flag to the scan image. The `--catalog-interval` flag
sets how often the operator reads a catalog's jcat file. The default is
one hour.

Each scan pod is privileged, because fwupd reads the SMBIOS tables,
`efivarfs`, `sysfs`, and device nodes. The pods run in `liken-system`,
which has no Pod Security label. If your cluster labels `liken-system`
with a level below `privileged`, the API server refuses the scan pods.

The scan pods and the operator read the catalog's host over HTTPS. On
a cluster with no route to the internet, delete the `lvfs` object and
declare a `FirmwareCatalog` for your own mirror. The mirror must serve
the catalog at its URL, the jcat at the same URL with `.jcat` added, and
the versioned catalog file that the jcat lists, in the same directory.
If the mirror listens on a port other than 443, add your own
`NetworkPolicy` beside `firmware-scan`.

The same files are on this site, so you can also apply them without
kustomize:

```sh
kubectl apply -f https://liken.sh/firmware/deploy/firmware-crd.yaml
kubectl apply -f https://liken.sh/firmware/deploy/firmwarecatalog-crd.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/rbac.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/operator.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/upload.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/scan.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/rehearsal.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/apply.yaml
kubectl apply -f https://liken.sh/firmware/deploy/lvfs.yaml
```

Those files use the image tag `latest`. The kustomization pins a release
instead.

The `lvfs` object is a `FirmwareCatalog`, a kind that the base defines.
On the first apply of the whole base, `kubectl` sends the object before
the API server serves the new kind, and fails with `ensure CRDs are
installed first`. Apply the base again and `kubectl` creates the
object. The commands above avoid the error by applying `lvfs.yaml`
last. A Flux `Kustomization` applies the definitions first and waits for
them.

Watch the operator start:

```sh
kubectl -n liken-system rollout status deployment/firmware-operator
```

## Confirm that it leads

Only the copy of the operator that holds the `Lease` named
`firmware-operator` in `liken-system` watches, writes statuses, and
starts pods. If two copies acted at once, both would follow the same
upload pod, and one of them could mark an upload that loaded as
failed.

```sh
kubectl -n liken-system get lease firmware-operator
```

The `HOLDER` column shows the pod that leads. During a rollout the new
pod starts beside the old one and waits. The old pod finishes its
current pass, sends the `Event`s it still has queued, and releases the
`Lease` when it stops. The new pod then takes the `Lease` within about
11 seconds. A leader that can't renew the `Lease`, for example on a node
that can't reach the control plane, stops writing 10 seconds after its
last renewal and then exits. A waiting copy takes a `Lease` that nobody
renews 30 to 41 seconds after its last renewal.

`kubectl get firmware` lists the `Firmware` objects. The list is empty until you write an
upload rule or the first scan finds a device. The
[upload guide](https://liken.sh/firmware/docs/guides/upload-to-a-device/) shows how to write an upload
rule, and the [find-updates guide](https://liken.sh/firmware/docs/guides/find-updates/) shows how to read
the scans.

## Run a development build

Every push to `main` publishes a development build of the image. To use
one, pin the base to the commit and the image to the build's tag with an
`images` entry in your kustomization:

```yaml
images:
  - name: ghcr.io/liken-sh/firmware-operator
    newTag: <tag>
```

The scan pods run the scan image at the same tag, so this entry changes
the operator and the scan image together. The upload pods run the image
that each `Firmware` names, so this entry does not change them.

## Remove the operator

Delete the `Deployment` to stop the operator:

```sh
kubectl -n liken-system delete deployment/firmware-operator
```

Each `Firmware` and `FirmwareCatalog` keeps its status, and a device
that loaded firmware keeps it until it loses power. With the operator
stopped, a device that appears without its firmware gets none, and no
machine is scanned.

Only the operator ends the hold of a reboot turn that it requested, and
the hold has no deadline. So a machine in such a turn stays drained
after the operator stops. Before you delete the `Deployment`, check that
no `Firmware` shows `Unknown` in its `APPLIED` column. If one does,
release the machine by hand
([A machine stays drained](https://liken.sh/firmware/docs/guides/troubleshoot/#a-machine-stays-drained)).

Delete the base to remove the definitions too. That deletes every
`Firmware` and every `FirmwareCatalog`. It also deletes every upload
pod, claim, scan pod, rehearsal pod, and apply pod that is still
running, and every `Disruption` that a `Firmware` created. A machine
that one of those `Disruption`s held in its turn leaves the turn without
a reboot.

The operator creates its `Lease` itself, so deleting the base leaves
it. Delete it with
`kubectl -n liken-system delete lease firmware-operator`.
