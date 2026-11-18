---
title: Install the operator
weight: 10
description: "Install display-operator on a liken cluster and confirm it publishes every monitor output as a device. Use when a cluster has no display devices yet, when running a development build, or when removing the operator."
---

# Install the operator

This guide installs `display-operator` on a
[`liken`](https://liken.sh/docs/) cluster. At the end, every monitor
output on the cluster is a device a workload can claim.

You need:

* A `liken` cluster. The operator claims the graphics card through
  [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/),
  from the devices `liken`'s own driver publishes.
  [Devices](https://liken.sh/docs/reference/devices/) describes
  those.
* A machine in that cluster with a graphics card, with a monitor on
  a connector.
* `kubectl` with cluster-admin access, because the install creates
  cluster-scoped objects: the
  [`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
  you create in step 2, a `ClusterRole`, and the `Display`
  `CustomResourceDefinition`.

## 1. Check that the card publishes

The machine with the monitors must publish its graphics card as a
device. Look for a `displayNode` attribute in that node's
`liken.sh` `ResourceSlice`:

    kubectl get resourceslice <node>-liken.sh -o yaml

If no device has `displayNode`, the operator's own claim cannot be
allocated and its pod stays `Pending`.
[Claiming hardware](https://liken.sh/docs/concepts/claiming-hardware/)
explains why: `liken` publishes the whole card, and this operator
claims it and publishes each of its outputs.

## 2. The device classes

A `DeviceClass` is cluster-scoped, and you choose its name and
selectors, as you do for a `StorageClass`.

* The base provides `display-gpu`, `display-render`, and
  `display-i2c` in [`deviceclasses.yaml`](/deploy/deviceclasses.yaml).
  The operator's pod claims the graphics card's card node, its render
  node, and its i2c buses through them. The `ResourceClaimTemplate` in
  [`operator.yaml`](/deploy/operator.yaml) refers to them by name, so
  the operator can't start without them. Don't delete them. They
  select on device attributes and not on a vendor or product id, so
  they work on a fleet of different machines.
* You create the class that your workloads claim through, because its
  name and selector are a choice for your cluster. Start with
  `display-output`:

        apiVersion: resource.k8s.io/v1
        kind: DeviceClass
        metadata:
          name: display-output
        spec:
          selectors:
            - cel:
                expression: |
                  device.driver == "display.liken.sh" &&
                  has(device.attributes["display.liken.sh"].appId)

  The `appId` check limits the class to monitor outputs. The driver
  also publishes each panel's
  [control device](/docs/reference/devices/#the-control-device),
  which has no `appId`, and a class without the check could allocate
  either one.

### Generic or specific

A class can match every monitor output or only some of them.
`display-output` above is generic, so each claim chooses its screen
with its own selector in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/).
A specific class contains the selector, so a claim names the class
and writes no CEL:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: lobby-screen
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "display.liken.sh" &&
              has(device.attributes["display.liken.sh"].appId) &&
              device.attributes["display.liken.sh"].connector == "HDMI-A-1"

Start with a generic class. Create a specific class when several
workloads repeat the same selector, or when the cluster, and not each
workload manifest, should choose the screen.

The example selects by `connector`, which every output publishes. It
keeps the `appId` check because the control device has a `connector`
attribute too. A class that selects by a monitor attribute, such as
`model`, must check the attribute with `has()` first, as
[Put a window on a screen](/docs/guides/claim/) shows, because a
connector with no signal doesn't publish monitor attributes, and a
selector that reads a missing attribute makes the whole allocation
fail.

## 3. Apply the manifests

This site serves the repository's [`deploy/`](/deploy/kustomization.yaml)
directory as raw YAML, so the install needs no clone. Apply every
file except `api-authentication.yaml` into `liken-system`:

    kubectl apply -n liken-system \
      -f https://liken.sh/display/deploy/displays.yaml \
      -f https://liken.sh/display/deploy/layouts.yaml \
      -f https://liken.sh/display/deploy/deviceclasses.yaml \
      -f https://liken.sh/display/deploy/rbac.yaml \
      -f https://liken.sh/display/deploy/operator.yaml \
      -f https://liken.sh/display/deploy/api.yaml

Then apply `api-authentication.yaml` with no `-n`. It binds a `Role`
in `kube-system`, and `kubectl` refuses an object in another
namespace than the one `-n` gives:

    kubectl apply -f https://liken.sh/display/deploy/api-authentication.yaml

`api.yaml` contains the `display-api` `Deployment`, its `Service`, and
its RBAC. `display-api` runs once for the cluster and serves the
routes in the [API reference](/docs/reference/api/). Leave `api.yaml`
and `api-authentication.yaml` out when you want no capture API.

`displays.yaml` is the `Display` `CustomResourceDefinition`. The
operator creates a [`Display`](/docs/reference/displays/) for every
monitor it probes, and it cannot do that on a cluster that does not
have the kind.

`layouts.yaml` is the `Layout` `CustomResourceDefinition`. A
[`Layout`](/docs/reference/layouts/) divides a screen into regions,
as the [layout guide](/docs/guides/layout/) shows. The operator
reads the `Layout` that a `Display` names in `spec.layout`, and
watches `Layout` objects for changes.

The `-n` flag puts the `ServiceAccount` and the `DaemonSet` in
`liken-system`, which every `liken` cluster has. The subject of the
`ClusterRoleBinding` is a `ServiceAccount` in that namespace, so the
operator works only there. The `-n` flag doesn't affect the
cluster-scoped `DeviceClass` and `CustomResourceDefinition` objects.

For GitOps, point a `Kustomization` at your own classes and at the
same URLs. `kustomize` accepts a raw YAML URL as a resource:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    transformers:
      - |-
        apiVersion: builtin
        kind: NamespaceTransformer
        metadata:
          name: liken-system
          namespace: liken-system
        unsetOnly: true
    resources:
      - classes.yaml
      - https://liken.sh/display/deploy/displays.yaml
      - https://liken.sh/display/deploy/layouts.yaml
      - https://liken.sh/display/deploy/deviceclasses.yaml
      - https://liken.sh/display/deploy/rbac.yaml
      - https://liken.sh/display/deploy/operator.yaml
      - https://liken.sh/display/deploy/api.yaml
      - https://liken.sh/display/deploy/api-authentication.yaml

The transformer sets `liken-system` only on objects that have no
namespace, so the `RoleBinding` in `api-authentication.yaml` stays in
`kube-system`. A top-level `namespace:` field would move it, and
`display-api` could not read the cluster's client certificate
authority.

A clone works too: `kubectl apply -k display-operator/deploy/` from the
repository applies the same base through
[`deploy/kustomization.yaml`](/deploy/kustomization.yaml).

## 4. Watch the operator find the screens

The operator runs as a `DaemonSet`, so you don't name the machine
with the monitors. Each pod claims the graphics card on its own node.
On a node with no graphics card, the claim finds no device and the pod
stays `Pending`, which uses no resources. To start no pod there, see
[Keep the pods off nodes with no graphics card](#keep-the-pods-off-nodes-with-no-graphics-card).

    kubectl -n liken-system get pods -o wide

On the machine with the card, the pod's log lists each monitor it
found:

    kubectl -n liken-system logs ds/display-operator
    display.liken.sh: operating the monitors on node-1
    display.liken.sh: HDMI-A-1 has gsm-7716-lg-hdr-wqhd

## 5. See the devices

The operator publishes a device for each connector on the card, in
a `ResourceSlice` named `<node>-display.liken.sh`:

    kubectl get resourceslice <node>-display.liken.sh -o yaml

A connector with a monitor has the monitor's attributes. A
connector with no monitor is published too, with a `disconnected`
taint, so a claim on it is not allocated until a monitor connects.
[Devices](/docs/reference/devices/) describes every attribute.

The operator also creates a `Display` for each monitor. A `Display`
is cluster-scoped. It reports the panel's controls, and you declare
values such as its brightness, input, and mode in its `spec`:

    kubectl get displays

[Displays](/docs/reference/displays/) describes the resource.

Now [put a window on a screen](/docs/guides/claim/).

## Keep the pods off nodes with no graphics card

To start no operator pod on a node with no graphics card, label the
node `display.liken.sh/display: none`. The `DaemonSet` in the base
already has this node affinity, so you need no patch:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: display.liken.sh/display
              operator: NotIn
              values: ["none"]
```

`NotIn` also matches a node that has no such label, as the
Kubernetes page on
[set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)
describes, so every node without the label gets a pod. When a label
changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes the pod from a node that no longer matches, and
starts one on a node that matches again.

Declare the label in the node's `Machine`, with the rest of the
machine's declaration:

    spec:
      nodeLabels:
        display.liken.sh/display: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on. An older release refuses the key, so on an older
release label the node with `kubectl`, as shown below.

The `liken` machine operator applies the label to the running node,
and a machine that is demoted or installed again registers a new
`Node` that has the label from the start. To run the pod on that node
again, remove the key from `spec.nodeLabels`, and `liken` removes the
label from the node, even when you first set it with `kubectl`.

You can also label the node with `kubectl`:

    kubectl label node node-1 display.liken.sh/display=none

A label set this way stays on the node after a reboot, because
`liken` removes only labels that a `Machine` declared. But a machine
that is demoted or installed again registers a new `Node`, and you
must label it again. To run the pod on the node again, remove the
label:

    kubectl label node node-1 display.liken.sh/display-

A patch of your own that sets a node affinity on this `DaemonSet`
replaces the list of terms in the base, including the `none` term.
Copy the `none` requirement into each term of your patch.

## Running a development build

A push to `main` that changes the operator publishes a development
build of it. Its version is the most recent release tag with a suffix.
For example, `2026.09.03-007-dev-003-abcdef01` is three commits after
release `2026.09.03-007`, at commit `abcdef01`. Every image of the
operator has the same version, and `:latest` is still the most recent
release.

A development build has no git tag. Pin the manifests to the full
commit sha, and pin each image to the version:

```yaml
resources:
  - https://github.com/liken-sh/liken//display-operator/deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/display-operator
    newTag: 2026.09.03-007-dev-003-abcdef01
  - name: ghcr.io/liken-sh/display-capture
    newTag: 2026.09.03-007-dev-003-abcdef01
  - name: ghcr.io/liken-sh/display-api
    newTag: 2026.09.03-007-dev-003-abcdef01
```

The operator `DaemonSet` runs `display-operator` and `display-capture`,
and the `display-api` `Deployment` runs `display-api`. Pin each of
them to the same version, because an image that you don't pin stays on
`:latest`.

A git fetch by sha needs all 40 characters, not the 8 in the version.
The summary of the CI run for that commit shows the full sha and the
version.

The same build publishes the manifests as the OCI artifact
`oci://ghcr.io/liken-sh/display-operator-deploy:<version>`, with each
image of the operator set to that version. A Flux `OCIRepository`
can pull that artifact by version, with no sha.

A Flux `Kustomization` that applies the base must not set
`targetNamespace`, because Flux sets that namespace on every object,
as a top-level `namespace:` field does. The `RoleBinding` in
`api-authentication.yaml` must stay in `kube-system`, where the
built-in `Role` that it binds is.

## Remove the operator

Delete the manifests. Then delete the slice on each node that
published one:

    kubectl delete -n liken-system \
      -f https://liken.sh/display/deploy/api.yaml \
      -f https://liken.sh/display/deploy/rbac.yaml \
      -f https://liken.sh/display/deploy/operator.yaml
    kubectl delete -f https://liken.sh/display/deploy/api-authentication.yaml
    kubectl delete resourceslice <node>-display.liken.sh

The API's `Secret`s and `ConfigMap` stay after you delete the
`Deployment`. Delete `display-api-tls`, `display-capture-server`, and
`display-api-ca` in `liken-system` by hand.

The operator never deletes its slice, because when a device leaves
the slice while a claim still refers to it, the kubelet can't finish
the prepare call for that claim. So the operator taints a device that
is gone and keeps it in the slice, and the slice stays after every pod
is gone. Delete the device classes yourself too, once nothing claims
through them.

**Deleting the `Display` CRD deletes every `Display`**, including the
brightness that an active override captured, so a panel that the
override turned dark has no saved value to restore. Remove every
override, and check that each panel shows what you expect, before you
delete `displays.yaml`.

Deleting `layouts.yaml` deletes every `Layout` and the arrangements
in them. Delete it after the last screen that uses a `Layout` no
longer needs one.
