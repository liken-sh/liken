---
title: Install the operator
weight: 10
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
* `kubectl` with cluster-admin access, because the install touches
  cluster scope: the
  [`DeviceClasses`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
  you create in step 2 and a `ClusterRole`.

## 1. Check that the card publishes

The machine with the monitors must publish its graphics card as a
device. Look for a `displayNode` attribute in that node's
`liken.sh` `ResourceSlice`:

    kubectl get resourceslice <node>-liken.sh -o yaml

If no device carries `displayNode`, the operator's own claim will
park and its pod will stay `Pending`. The
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/)
page describes this layering: `liken` publishes the card, and this
operator refines it into outputs.

## 2. Create the device classes

A `DeviceClass` is cluster-scoped policy: you name and curate the
classes, the same convention as a `StorageClass`, so the base ships
none. Save this as `deviceclasses.yaml`. Then apply it:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: display-gpu
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "liken.sh" &&
              has(device.attributes["liken.sh"].displayNode)
    ---
    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: display-render
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "liken.sh" &&
              has(device.attributes["liken.sh"].renderNode)
    ---
    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: display-output
    spec:
      selectors:
        - cel:
            expression: device.driver == "display.liken.sh"

    kubectl apply -f deviceclasses.yaml

The three classes do two different jobs:

* `display-gpu` and `display-render` are bootstrap. The operator's
  own pod claims the graphics card's card node and render node
  through them, from the devices `liken` publishes, so without them
  the operator cannot start. The classes select on the `displayNode`
  and `renderNode` attributes rather than on a vendor and a product
  id, so they stay correct across a fleet of different machines.
* `display-output` is what workloads claim: this operator's monitor
  outputs.

The names are yours to choose, with one consequence: the operator's
`ResourceClaimTemplate` in [`operator.yaml`](/deploy/operator.yaml)
names `display-gpu` and `display-render` literally, so different
names there mean patching the template. A different name for
`display-output` costs nothing; your claims name it in
`deviceClassName`.

### Generic or specific

A class is the cluster's vocabulary for a kind of device, and you
choose its grain. `display-output` above is generic: it matches
every monitor output, keeps the class list short, and leaves the
choice of screen to each claim's selector, written in
[Common Expression Language (CEL)](https://kubernetes.io/docs/reference/using-api/cel/).
A specific class holds the selector itself. A claim then names the
class and writes no CEL, and you make the choice once, in cluster
policy you control:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: lobby-screen
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "display.liken.sh" &&
              device.attributes["display.liken.sh"].connector == "HDMI-A-1"

Start generic. When several workloads repeat the same selector, or
when you want the choice of screen in cluster policy rather than in
each workload's manifest, create a specific class.

The example selects by `connector`, the one attribute every output
always publishes. A specific class that selects by a monitor
attribute, such as `model`, must guard the read with `has()`, the
way [Put a window on a screen](/docs/guides/claim/) shows. Those
attributes are absent on a dark connector, and a selector that
reads a missing attribute fails the whole allocation.

## 3. Apply the manifests

This site serves the repository's [`deploy/`](/deploy/kustomization.yaml)
directory as raw YAML, so the install needs no clone. Two files are
the rest of the install:

    kubectl apply -n liken-system \
      -f https://display.liken.sh/deploy/rbac.yaml \
      -f https://display.liken.sh/deploy/operator.yaml

The `-n` flag places the `ServiceAccount` and the `DaemonSet` in
`liken-system`, the namespace every `liken` cluster has. The
`ClusterRoleBinding`'s subject names that namespace, so the binding
only works there.

For GitOps, point a `Kustomization` at your classes and the same
URLs. `kustomize` takes a raw YAML URL as a resource:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - deviceclasses.yaml
      - https://display.liken.sh/deploy/rbac.yaml
      - https://display.liken.sh/deploy/operator.yaml

A clone works too: after step 2, `kubectl apply -k deploy/` from the
repository applies the same base through
[`deploy/kustomization.yaml`](/deploy/kustomization.yaml).

## 4. Watch the operator find the screens

The operator runs as a `DaemonSet`, so a pod lands on every node and
no manifest names the machine with the monitors. Each pod claims the
card on its own node. On a node with no graphics card, the claim
finds no device and the pod parks `Pending`, which costs nothing.

    kubectl -n liken-system get pods -o wide

On the machine with the card, the pod's log names each monitor it
found:

    kubectl -n liken-system logs ds/display-operator
    display.liken.sh: operating the monitors on kitchen
    display.liken.sh: HDMI-A-1 carries gsm-7716-lg-hdr-wqhd, app-id hdmi-a-1

## 5. See the devices

The operator publishes one device for each connector on the card,
into a `ResourceSlice` named `<node>-display.liken.sh`:

    kubectl get resourceslice <node>-display.liken.sh -o yaml

A connector with a monitor carries the monitor's attributes. An
empty connector publishes too, with a `disconnected` taint, so a
claim on it parks until a monitor arrives.
[Devices](/docs/reference/devices/) describes every attribute.

Now [put a window on a screen](/docs/guides/claim/).

## Remove the operator

Delete the manifests. Then delete the slice on each node that
published one:

    kubectl delete -n liken-system \
      -f https://display.liken.sh/deploy/rbac.yaml \
      -f https://display.liken.sh/deploy/operator.yaml
    kubectl delete resourceslice <node>-display.liken.sh

The slice step is yours because the operator never deletes its
slice. A device that leaves the inventory while a claim still names
it strands the kubelet's prepare call. So the operator taints
devices instead of removing them, and the slice outlives every pod.
The device classes are yours too. When nothing else claims through
them, delete them.
