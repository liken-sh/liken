---
title: Install the operator
weight: 10
description: "Install firmware-operator and the Firmware resource definition from its kustomize base, confirm that it holds its Lease, and remove it. Use when a cluster has USB devices that need firmware from the host, when running a development build, or when removing the operator."
---

# Install the operator

At the end of this guide, `firmware-operator` runs in `liken-system`
and holds its `Lease`, and the cluster accepts `Firmware` objects. You
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

- the `Firmware` resource definition, which is cluster-scoped;
- the `firmware-operator` `ServiceAccount` and its `ClusterRole`,
  which reads every `Firmware`, every `ResourceSlice`, and every
  `Machine`, and writes each `Firmware`'s status;
- a `Role` in `liken-system`, which creates, reads, and deletes the
  upload pods and their `ResourceClaim`s;
- a `Role` in `default`, which posts the `Event`s about each
  `Firmware`;
- a `Role` in `liken-system`, which creates, reads, and renews the
  `Lease` named `firmware-operator`;
- the `firmware-operator` `Deployment`, with one replica;
- the `DeviceClass` `firmware-upload`, which selects the USB devices
  of `liken.sh` and which each upload's claim names;
- the `NetworkPolicy` `firmware-upload` in `liken-system`, which
  denies all traffic in and out of the upload pods.

The same files are on this site, so you can also apply them without
kustomize:

```sh
kubectl apply -f https://liken.sh/firmware/deploy/firmware-crd.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/rbac.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/operator.yaml
kubectl apply -n liken-system -f https://liken.sh/firmware/deploy/upload.yaml
```

Those files name the image tag `latest`. The kustomization pins a
release instead.

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

`kubectl get firmware` lists the rules, and is empty until you write
one. The [upload guide](../upload-to-a-device/) writes the first.

## Run a development build

Every push to `main` publishes a development build of the image. Pin
the base to the commit and the image to the build's tag with an
`images` entry in your kustomization:

```yaml
images:
  - name: ghcr.io/liken-sh/firmware-operator
    newTag: <tag>
```

The upload pods run the image that each `Firmware` names, not the
operator's image, so this entry changes only the operator.

## Remove the operator

Delete the `Deployment` to stop the operator. Each `Firmware` keeps
its status, and a device that loaded keeps its firmware until it
loses power. A bare chip that appears after that stays bare.

```sh
kubectl -n liken-system delete deployment/firmware-operator
```

Delete the base to remove the definition too. That deletes every
`Firmware`, and with them every upload pod and claim that is still
running.

The operator creates its `Lease` itself, so the base does not hold it,
and it stays after you delete the base. Delete it with
`kubectl -n liken-system delete lease firmware-operator`.
