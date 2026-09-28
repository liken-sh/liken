---
name: install
description: "Install display-operator on a liken cluster and confirm it publishes every monitor output as a device. Use when a cluster has no display devices yet, when running a development build, or when removing the operator."
---

This skill is the guide at https://liken.sh/display/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

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
  you create in step 2, a `ClusterRole`, and the `Display`
  `CustomResourceDefinition`.

## 1. Check that the card publishes

The machine with the monitors must publish its graphics card as a
device. Look for a `displayNode` attribute in that node's
`liken.sh` `ResourceSlice`:

    kubectl get resourceslice <node>-liken.sh -o yaml

If no device has `displayNode`, the operator's own claim will
park and its pod will stay `Pending`. The
[hardware operators](https://liken.sh/docs/concepts/hardware-operators/)
page describes this layering: `liken` publishes the card, and this
operator refines it into outputs.

## 2. The device classes

A `DeviceClass` is cluster-scoped policy. You name and curate the
classes, using the same convention as a `StorageClass`. The classes
split by owner:

* `display-gpu`, `display-render`, and `display-i2c` are wiring,
  and the base ships them, served at
  [`deviceclasses.yaml`](https://liken.sh/display/deploy/deviceclasses.yaml). The
  operator's own pod claims the graphics card's card node, its
  render node, and its monitor-control wires through them, from the
  devices `liken` publishes. The `ResourceClaimTemplate` in
  [`operator.yaml`](https://liken.sh/display/deploy/operator.yaml) names them literally, so
  the operator cannot start without them. Do not delete them. The
  classes select on the `displayNode` and `renderNode` attributes
  and on the i2c companion's `subsystem`. They do not select on a
  vendor and a product id, so they stay correct across a fleet of
  different machines.
* The class your workloads claim through is yours to create,
  because it is your cluster's vocabulary, and the base ships no
  policy. `display-output` is the one to start with:

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

  The `appId` guard keeps the class on outputs. The driver also
  publishes each panel's
  [control device](https://liken.sh/display/docs/reference/devices/#the-control-device),
  which has no `appId`, and a class that matched the whole driver
  would allocate either one.

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
              has(device.attributes["display.liken.sh"].appId) &&
              device.attributes["display.liken.sh"].connector == "HDMI-A-1"

Start with a generic class. When several workloads repeat the same
selector, or when cluster policy should choose the screen instead of
each workload manifest, create a specific class.

The example selects by `connector`, an attribute every output
always publishes. The `appId` guard is there because the panel's
control device publishes `connector` too. A specific class that
selects by a monitor attribute, such as `model`, must guard the read
with `has()`, the way [Put a window on a screen](https://liken.sh/display/docs/guides/claim/)
shows. Those attributes are absent on a dark connector, and a
selector that reads a missing attribute fails the whole allocation.

## 3. Apply the manifests

This site serves the repository's [`deploy/`](https://liken.sh/display/deploy/kustomization.yaml)
directory as raw YAML, so the install needs no clone. Five files
are the rest of the install, and `api.yaml` is the one that runs
once per cluster rather than once per node:

    kubectl apply -n liken-system \
      -f https://liken.sh/display/deploy/displays.yaml \
      -f https://liken.sh/display/deploy/deviceclasses.yaml \
      -f https://liken.sh/display/deploy/rbac.yaml \
      -f https://liken.sh/display/deploy/operator.yaml \
      -f https://liken.sh/display/deploy/api.yaml

`api.yaml` holds the `display-api` `Deployment`, its `Service`, and
its RBAC. It answers the routes the [API reference](https://liken.sh/display/docs/reference/api/)
describes, and an owner who wants no capture API leaves it out.

`displays.yaml` is the `Display` `CustomResourceDefinition`. The
operator creates a [`Display`](https://liken.sh/display/docs/reference/displays/) for every
monitor it probes, and it cannot do that on a cluster where the kind
is missing.

The `-n` flag places the `ServiceAccount` and the `DaemonSet` in
`liken-system`, the namespace every `liken` cluster has. The
`ClusterRoleBinding`'s subject names that namespace, so the binding
only works there. `DeviceClass` and the `CustomResourceDefinition`
are cluster-scoped, so the flag leaves them alone.

For GitOps, point a `Kustomization` at your specific classes and the
same URLs. `kustomize` takes a raw YAML URL as a resource:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - classes.yaml
      - https://liken.sh/display/deploy/displays.yaml
      - https://liken.sh/display/deploy/deviceclasses.yaml
      - https://liken.sh/display/deploy/rbac.yaml
      - https://liken.sh/display/deploy/operator.yaml
      - https://liken.sh/display/deploy/api.yaml

A clone works too: `kubectl apply -k deploy/` from the repository
applies the same base through
[`deploy/kustomization.yaml`](https://liken.sh/display/deploy/kustomization.yaml).

## 4. Watch the operator find the screens

The operator runs as a `DaemonSet`, so a pod lands on every node and
no manifest names the machine with the monitors. Each pod claims the
card on its own node. On a node with no graphics card, the claim
finds no device and the pod parks `Pending`, which costs nothing. A
node labeled `display.liken.sh/display: none` gets no pod, as
[Keep the pods off nodes with no graphics card](#keep-the-pods-off-nodes-with-no-graphics-card)
describes.

    kubectl -n liken-system get pods -o wide

On the machine with the card, the pod's log names each monitor it
found:

    kubectl -n liken-system logs ds/display-operator
    display.liken.sh: operating the monitors on kitchen
    display.liken.sh: HDMI-A-1 has gsm-7716-lg-hdr-wqhd

## 5. See the devices

The operator publishes one device for each connector on the card,
into a `ResourceSlice` named `<node>-display.liken.sh`:

    kubectl get resourceslice <node>-display.liken.sh -o yaml

A connector with a monitor has the monitor's attributes. An
empty connector publishes too, with a `disconnected` taint, so a
claim on it parks until a monitor arrives.
[Devices](https://liken.sh/display/docs/reference/devices/) describes every attribute.

The operator also creates one `Display` per monitor, the
cluster-scoped resource that reports the panel's controls and takes
declarations:

    kubectl get displays

[Displays](https://liken.sh/display/docs/reference/displays/) describes the resource.

Now [put a window on a screen](https://liken.sh/display/docs/guides/claim/).

## Keep the pods off nodes with no graphics card

The `DaemonSet` makes a pod on every node. On a node with no
graphics card, the claim matches no device, and the pod stays `Pending`. To make no
pod on such a node, label the node `display.liken.sh/display: none`.

The `DaemonSet` in the base carries this node affinity, so no patch is
needed:

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

`NotIn` matches a node whose label has a different value, and also a
node that has no such label. The Kubernetes page on
[set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)
gives this rule. So with no label the `DaemonSet` makes a pod on every
node, and a node labeled `none` gets no pod. When a label changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes the pod from a node that no longer matches, and
adds one to a node that matches again.

Declare the label in the node's `Machine`, so the label is part of
the record of the machine:

    spec:
      nodeLabels:
        display.liken.sh/display: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on, and an older release refuses it, so on an older
release use the `kubectl label` way below.

The `liken` machine operator applies the label to the running node. A
machine that is demoted or installed again registers a new Node, and
the Node has the label from registration. To run the pod on that
node again, remove the key from `spec.nodeLabels`, and `liken`
removes the label from the node. `liken` also removes a label that
you set with `kubectl` before the `Machine` declared it, when the key
leaves the spec.

You can also label the node with `kubectl`:

    kubectl label node node-1 display.liken.sh/display=none

The label stays on the node across reboots. `liken` leaves it in
place, because `liken` removes only the labels that a `Machine`
declared. The label goes with the Node object: a machine that is
demoted or installed again registers a new Node, and you label it
again. To run the pod on that node again, remove the label:

    kubectl label node node-1 display.liken.sh/display-

A patch of your own that sets a node affinity on this `DaemonSet`
replaces the list of terms in the base, and the `none` term with it.
Copy the `none` requirement into each term of your patch.

## Running a development build

Every push to the operator's main branch publishes a development
build. Its version is the most recent release plus a suffix:
`2026.09.03-007-dev-003-abcdef01` is three commits past release
`2026.09.03-007`, at commit `abcdef01`. Every image the repository
builds has the same version, and `:latest` still names the
most recent release.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version:

```yaml
resources:
  - https://github.com/liken-sh/display-operator//deploy?ref=<full 40-character sha>
images:
  - name: ghcr.io/liken-sh/display-operator
    newTag: 2026.09.03-007-dev-003-abcdef01
```

A git fetch by sha needs all forty characters; the eight in the
version are not enough. The CI run for that commit prints both
lines in its summary.

## Remove the operator

Delete the manifests. Then delete the slice on each node that
published one:

    kubectl delete -n liken-system \
      -f https://liken.sh/display/deploy/api.yaml \
      -f https://liken.sh/display/deploy/rbac.yaml \
      -f https://liken.sh/display/deploy/operator.yaml
    kubectl delete resourceslice <node>-display.liken.sh

The API's own `Secret`s and `ConfigMap` outlive the `Deployment`.
Delete `display-api-tls`, `display-capture-server`, and
`display-api-ca` in `liken-system` by hand.

The slice step is yours because the operator never deletes its
slice. A device that leaves the inventory while a claim still names
it strands the kubelet's prepare call. So the operator taints
devices instead of removing them, and the slice outlives every pod.
The device classes are yours too. When nothing else claims through
them, delete them.

**Deleting the `Display` CRD deletes every `Display` with it**,
including the brightness a standing override captured. A panel an
override darkened then has nothing left to restore it. Lift every
override, and confirm every panel shows what you expect, before you
delete `displays.yaml`.
