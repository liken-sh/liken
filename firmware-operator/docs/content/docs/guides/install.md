---
title: Install the operator
weight: 10
description: "Install firmware-operator, the Firmware and FirmwareCatalog resource definitions, the lvfs FirmwareCatalog, and the scan image from its kustomize base, confirm that it holds its Lease, and remove it. Use when a cluster has USB devices that need firmware from the host, when a cluster should find firmware updates, when running a development build, or when removing the operator."
---

# Install the operator

At the end of this guide, `firmware-operator` runs in `liken-system`
and holds its `Lease`, and the cluster accepts `Firmware` and
`FirmwareCatalog` objects. You
need `kubectl` with cluster-admin rights on a `liken` cluster. The
operator reads the devices that `liken`'s `machine-operator`
publishes, so it does nothing useful on another kind of cluster.

Add the base in the operator's `deploy/` directory to your own
kustomization, and pin `<ref>` to a release tag. A pinned ref installs
the same definition and the same operator image every time you apply
it.

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization

resources:
  - https://github.com/liken-sh/liken//firmware-operator/deploy?ref=<ref>
```

The base holds these parts:

- the `Firmware` and `FirmwareCatalog` resource definitions, which are
  cluster-scoped;
- the `firmware-operator` `ServiceAccount` and its `ClusterRole`,
  which reads every `Firmware`, every `FirmwareCatalog`, every
  `ResourceSlice`, and every `Machine`, writes each `Firmware`'s and
  each `FirmwareCatalog`'s status, and creates and deletes the discovered
  `Firmware` objects;
- a `Role` in `liken-system`, which creates, reads, and deletes the
  upload pods and their `ResourceClaim`s and the scan pods, and reads
  the logs of the scan pods;
- a `Role` in `default`, which posts the `Event`s about each
  `Firmware` and each `FirmwareCatalog`;
- a `Role` in `liken-system`, which creates, reads, and renews the
  `Lease` named `firmware-operator`;
- the `firmware-operator` `Deployment`, with one replica;
- the `DeviceClass` `firmware-upload`, which selects the USB devices
  of `liken.sh` and which each upload's claim names;
- the `NetworkPolicy` `firmware-upload` in `liken-system`, which
  denies all traffic in and out of the upload pods;
- the `NetworkPolicy` `firmware-scan` in `liken-system`, which allows
  the scan pods to send traffic only to DNS (port 53) and to TCP port
  443, and allows no traffic in;
- the `FirmwareCatalog` `lvfs`, which names the LVFS catalog at
  `https://cdn.fwupd.org/downloads/firmware.xml.zst`.

The operator starts a scan pod on each machine from the image
`ghcr.io/liken-sh/firmware-operator-scan`. It reads the tag from its
own image, so one pin moves both images. The `--scan-image` flag names
another image, for an operator image that you name by digest. The
`--catalog-interval` flag sets the time between two reads of a
catalog's jcat, an hour by default.

Each scan pod is privileged, because fwupd reads SMBIOS tables,
`efivarfs`, `sysfs`, and device nodes. The pods run in `liken-system`,
which carries no Pod Security label. If your cluster labels
`liken-system` with a level below `privileged`, the API server refuses
the scan pods. The pods need to reach the catalog's host over HTTPS,
and the operator needs the same route to read the jcat. A cluster with
no route to the internet deletes the `lvfs` object and declares a
`FirmwareCatalog` for its own mirror, which serves the jcat, the versioned
file, and the unversioned file under its own URL. A mirror on a port
other than 443 needs a `NetworkPolicy` of your own beside
`firmware-scan`.

The same files are on this site, so you can also apply them without
kustomize:

```sh
kubectl apply -f https://liken.sh/firmware/deploy/firmware-crd.yaml
kubectl apply -f https://liken.sh/firmware/deploy/firmwarecatalog-crd.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/rbac.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/operator.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/upload.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/scan.yaml
kubectl apply -f https://liken.sh/firmware/deploy/lvfs.yaml
```

Those files name the image tag `latest`. The kustomization pins a
release instead.

The `lvfs` object is a `FirmwareCatalog`, a kind that the base defines. On the
first apply of the whole base, `kubectl` sends the object before the
API server serves the new kind, and refuses it with `ensure CRDs are
installed first`. Apply the base a second time, and the object is
created. The commands above avoid this by applying `lvfs.yaml` last.
A Flux `Kustomization` applies the definitions first and waits for
them.

Watch the operator start:

```sh
kubectl -n liken-system rollout status deployment/firmware-operator
```

## Confirm that it leads

Only one copy of the operator acts at a time. Each copy competes for
the `Lease` named `firmware-operator` in `liken-system`, and only the
copy that holds it watches, writes statuses, and starts upload pods.
Two copies that acted at once would both follow the same upload pod,
and one of them would mark an upload that loaded as failed.

```sh
kubectl -n liken-system get lease firmware-operator
```

The `HOLDER` column names the pod that leads. A rollout starts the
new pod beside the old one. The old pod finishes the `Firmware` it is
answering, writes the `Event`s it posted, and releases the `Lease`
when it stops, and the new pod takes it within about 11 seconds. A
leader that can't renew the `Lease`, such as one on a node the
control plane can't reach, stops writing 10 seconds after its last
renewal and then exits. A waiting copy takes a `Lease` that nobody
renews 30 to 41 seconds after its last renewal.

`kubectl get firmware` lists the rules. It is empty until you write
an upload rule or the first scan finds a device. The
[upload guide](../upload-to-a-device/) writes an upload rule, and the
[find-updates guide](../find-updates/) reads the scans.

## Run a development build

Every push to `main` publishes a development build of the image. Pin
the base to the commit and the image to the build's tag with an
`images` entry in your kustomization:

```yaml
images:
  - name: ghcr.io/liken-sh/firmware-operator
    newTag: <tag>
```

The scan pods run the scan image at the same tag, so this entry moves
the operator and the scan image together. The upload pods run the image
that each `Firmware` names, so this entry does not change them.

## Remove the operator

Delete the `Deployment` to stop the operator. Each `Firmware` and each
`FirmwareCatalog` keeps its status, and a device that loaded keeps its firmware
until it loses power. A bare chip that appears after that stays bare,
and no machine is scanned.

```sh
kubectl -n liken-system delete deployment/firmware-operator
```

Delete the base to remove the definitions too. That deletes every
`Firmware` and every `FirmwareCatalog`, and with them every upload pod, claim,
and scan pod that is still running.

The operator creates its `Lease` itself, so the base does not hold it,
and it stays after you delete the base. Delete it with
`kubectl -n liken-system delete lease firmware-operator`.
