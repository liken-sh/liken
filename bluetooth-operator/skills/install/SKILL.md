---
name: install
description: "Install bluetooth-operator on a liken cluster and verify that it claims the radio. Use when a cluster has no Bluetooth devices yet, when running a development build, or when removing the operator."
---

This skill is the guide at https://liken.sh/bluetooth/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

<a id="install-the-operator"></a>

# Install and verify the operator

This guide installs `bluetooth-operator` on a
[`liken`](https://liken.sh/docs/) cluster and verifies that its pod
claims the radio. The operator is an ordinary workload. Everything it
needs is in one `kustomize` base, and nothing here touches a machine
over SSH.

## What you need

* A `liken` cluster. The operator claims the Bluetooth adapter from
  `liken`'s own [Dynamic Resource Allocation
  (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
  driver, so the cluster's operating system publishes the raw
  hardware. [Devices](https://liken.sh/docs/reference/devices/)
  describes that inventory.
* A machine with a USB Bluetooth adapter. The operator selects on the
  `btusb` kernel driver, which covers the plug-in dongles and the
  radios built into a board. You do not have to say which machine has
  the radio: the claim places the pod where the radio is.

<a id="the-device-classes"></a>

## Create the device classes

A [`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
is cluster-scoped policy, the same convention a `StorageClass`
follows: the cluster owner names and curates the classes workloads
may ask for. The classes split by owner. If the DRA objects are new
to you, read
[How the pieces fit](https://liken.sh/bluetooth/docs/guides/#how-the-pieces-fit) first.

* `bluetooth-adapter` is wiring, and the base ships it, served at
  [`deviceclasses.yaml`](https://liken.sh/bluetooth/deploy/deviceclasses.yaml). The
  operator's own pod claims the raw radio through it, and its
  selector picks the `btusb` adapter that `liken` publishes. The
  claim template in [`operator.yaml`](https://liken.sh/bluetooth/deploy/operator.yaml) names
  it literally, so the operator cannot start without it. Do not
  delete it.
* The class your workloads claim through is yours to create,
  because it is your cluster's vocabulary, and the base ships no
  policy. `bluetooth-input` is the one to start with. Its selector
  covers the paired input devices and only them, because the driver
  also publishes devices no workload should hold, such as a paired
  speaker's bond record:

        apiVersion: resource.k8s.io/v1
        kind: DeviceClass
        metadata:
          name: bluetooth-input
        spec:
          selectors:
            - cel:
                expression: |
                  device.driver == "bluetooth.liken.sh" &&
                  has(device.attributes["bluetooth.liken.sh"].input) &&
                  device.attributes["bluetooth.liken.sh"].input

The guard on the `input` attribute also keeps the adapter's [media
bus](https://liken.sh/bluetooth/docs/reference/devices/#the-media-bus) out of this class. The
bus is the audio operator's to claim, through a class of its own that
names the shared `sound.liken.sh/supportsSound` attribute.

### Generic or specific

A class is the cluster's vocabulary for a kind of device, and you
choose its grain. A generic class such as `bluetooth-input`
matches every paired input device: the class list stays short, and
each claim picks its controller with a CEL selector. A specific
class holds the selector itself. This one matches exactly one
controller. A claim then names the class and writes no CEL, and you
make the choice once, in cluster policy you control:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: player-one-dualsense
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "bluetooth.liken.sh" &&
              device.attributes["bluetooth.liken.sh"].address == "A0:AB:51:33:B7:12"

Start generic. When several workloads repeat the same selector, or
when you want the choice in cluster policy rather than in each
workload's manifest, create a specific class.

## Apply the manifests

This site serves the repository's manifests as raw YAML under
[/deploy/](https://liken.sh/bluetooth/deploy/kustomization.yaml), so you can install from here
without a clone. Apply the three files into `liken-system`, the
namespace a `liken` cluster already has:

    kubectl apply -n liken-system \
      -f https://liken.sh/bluetooth/deploy/crds.yaml \
      -f https://liken.sh/bluetooth/deploy/rbac.yaml \
      -f https://liken.sh/bluetooth/deploy/operator.yaml

Or point your own GitOps at the same files with a `Kustomization`:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - https://liken.sh/bluetooth/deploy/crds.yaml
      - https://liken.sh/bluetooth/deploy/rbac.yaml
      - https://liken.sh/bluetooth/deploy/operator.yaml

The site serves the manifests of the current `main`, and the images
in `operator.yaml` name `:latest`. To pin a release instead,
reference the operator's `kustomize` base at the operator's version,
which is the release tag that last published it. The
[GitHub release](https://github.com/liken-sh/liken/releases) for each
tag lists every component and its version. One tag versions the
manifests and the three images together, so pin all four to the same
version:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    resources:
      - https://github.com/liken-sh/liken//bluetooth-operator/deploy?ref=2026.09.29-002
    images:
      - name: ghcr.io/liken-sh/bluetooth-operator
        newTag: 2026.09.29-002
      - name: ghcr.io/liken-sh/bluetoothd
        newTag: 2026.09.29-002
      - name: ghcr.io/liken-sh/bluetooth-bondfetch
        newTag: 2026.09.29-002

Whichever path you take, the manifests contain:

* The `bluetooth-adapter` `DeviceClass`, the wiring the operator's
  own claim names. Your consumer class, such as `bluetooth-input`
  above, is not in the manifests: you create it.
* The three `CustomResourceDefinitions` of the pairing API:
  `Adapter`, `Peripheral`, and `PairingRequest`. The operator records
  every bond as a `Peripheral` and stores its keys in a `Secret` that
  the `Peripheral` owns, so install the CRDs with the workload.
* The operator's `ServiceAccount` and its RBAC.
* A `DaemonSet` and the `ResourceClaimTemplate` its pods claim the
  adapter through.

## Keep the pods off nodes with no Bluetooth adapter

The `DaemonSet` makes a pod on every node. On a node with no
Bluetooth adapter, the claim matches no device, and the pod stays `Pending`. To make no
pod on such a node, label the node `bluetooth.liken.sh/bluetooth: none`.

The `DaemonSet` in the base carries this node affinity, so no patch is
needed:

```yaml
affinity:
  nodeAffinity:
    requiredDuringSchedulingIgnoredDuringExecution:
      nodeSelectorTerms:
        - matchExpressions:
            - key: bluetooth.liken.sh/bluetooth
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
        bluetooth.liken.sh/bluetooth: none

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

    kubectl label node node-1 bluetooth.liken.sh/bluetooth=none

The label stays on the node across reboots. `liken` leaves it in
place, because `liken` removes only the labels that a `Machine`
declared. The label goes with the Node object: a machine that is
demoted or installed again registers a new Node, and you label it
again. To run the pod on that node again, remove the label:

    kubectl label node node-1 bluetooth.liken.sh/bluetooth-

A patch of your own that sets a node affinity on this `DaemonSet`
replaces the list of terms in the base, and the `none` term with it.
Copy the `none` requirement into each term of your patch.

## Running a development build

A push to `main` that changes the operator publishes a development
build of it. Its version is the most recent release tag plus a suffix:
`2026.09.03-007-dev-003-abcdef01` is three commits past release
`2026.09.03-007`, at commit `abcdef01`. Every image of the operator
has the same version, and `:latest` still names the most recent
release.

A development build has no git tag, so the manifests pin to the
commit's full sha, and the image pins to the version:

    resources:
      - https://github.com/liken-sh/liken//bluetooth-operator/deploy?ref=<full 40-character sha>
    images:
      - name: ghcr.io/liken-sh/bluetooth-operator
        newTag: 2026.09.03-007-dev-003-abcdef01
      - name: ghcr.io/liken-sh/bluetoothd
        newTag: 2026.09.03-007-dev-003-abcdef01
      - name: ghcr.io/liken-sh/bluetooth-bondfetch
        newTag: 2026.09.03-007-dev-003-abcdef01

A git fetch by sha needs all forty characters; the eight in the
version are not enough. The summary of the CI run for that commit
gives the version.

The same build publishes the manifests as the OCI artifact
`oci://ghcr.io/liken-sh/bluetooth-operator-deploy:<version>`, with each
image of the operator set to that version. A Flux `OCIRepository`
can pull that artifact by the version, with no sha.

## How the pod finds the radio

The `DaemonSet` puts a pod on every node, and each pod claims one
`bluetooth-adapter` device. On a node with an adapter the claim
matches and the pod runs. On a node with no adapter the claim matches
nothing, so the pod parks `Pending` and costs nothing. A node labeled
`bluetooth.liken.sh/bluetooth: none` gets no pod, as
[Keep the pods off nodes with no Bluetooth adapter](#keep-the-pods-off-nodes-with-no-bluetooth-adapter)
describes. Nobody writes
down which machine has the radio, and a dongle moved to another
machine works there on the next pod start.

The claim also makes the pod the only Bluetooth stack on that radio,
because `liken` publishes the adapter as a device that allocates
once. The kernel arbitrates nothing between two stacks on one
adapter.

## Verify

    kubectl get pods -n liken-system -l app=bluetooth-operator

A healthy install has one `Running` pod on each machine with an
adapter. Each machine without one has a `Pending` pod, or no pod when
its node is labeled `bluetooth.liken.sh/bluetooth: none`. Then read
the radio the operator holds:

    $ kubectl get adapters
    NAME                ALIAS   ADDRESS             NODE      POWERED   AGE
    04-4a-69-66-92-27           04:4A:69:66:92:27   liken-1   true      1m

The operator creates an `Adapter` object for the radio its pod
claimed, named for the radio's address. The `ResourceSlice` of paired
controllers appears when the first controller is paired:
[Pair a controller and give it to a pod](https://liken.sh/bluetooth/docs/guides/pair-a-controller/) is
the next step.

## Read the Events

The operator posts a Kubernetes `Event` when a window opens or
closes, a device pairs, or a bond, a relay, or the radio fails. Read
them with `kubectl describe` on the object. A `Peripheral` and a
`Node` are cluster-scoped, so their `Event`s are in `default`, and
`kubectl events --for` finds them only with `-n default` or `-A`:

    kubectl describe pairingrequest new-gamepad -n liken-system
    kubectl events -n default --for peripheral/a0-ab-51-33-b7-12

| Reason | Type | Object | What happened |
|---|---|---|---|
| `PairingWindowOpened` | Normal | `PairingRequest` | The radio is discoverable and pairable until the window closes. |
| `PairingWindowExpired` | Normal | `PairingRequest` | The window closed with no device paired. |
| `PairingRefused` | Warning | `PairingRequest` | `bluetoothd` refused to pair the approved device. The window tries again, and the `Event` repeats only when the refusal changes. |
| `Paired` | Normal | `PairingRequest`, `Peripheral` | The device holds a bond, and the operator created its `Peripheral`. |
| `BondLost` | Warning | `Peripheral` | `bluetoothd` holds no bond with the device any more. The `Peripheral` stays until you delete it. |
| `InputRelayFailed` | Warning | `Peripheral` | The operator could not make the virtual input device that a claim on the controller receives. |
| `RadioClaimed` | Normal | `Node` | `bluetoothd` in the pod reports the radio. |
| `RadioLost` | Warning | `Node` | The radio is gone from `bluetoothd`: the adapter was unplugged or reset. |

A controller that connects or disconnects posts no `Event`. A Low
Energy remote drops its link between presses, so the `Connected`
condition and the `bluetooth_disconnects_total` metric hold those
changes. The API server deletes an `Event` an hour after its last
write. The status and the pod's log keep each fact longer.

<a id="look-inside-the-stack"></a>

## Inspect the Bluetooth stack

The `bluetoothd` image holds four tools for a person. Each runs as
a direct `kubectl exec`, with no shell between, and every one of
them needs the `-i` flag. BlueZ's shells attach to their standard
input. With stdin closed the attach fails, and the command never
runs and prints nothing.

`btmgmt info` prints the adapter's management settings, and its
`current settings` line is where `Connectable`, `Discoverable`, and
`Bondable` read. `btmon` traces the HCI link live, the layer under
D-Bus and under `bluetoothd`. It shows a disconnect reason or a
retransmission that no higher layer reports. `dbus-send` calls any
method on `org.bluez`. `bluetoothctl list` names what the daemon
holds.

    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- btmgmt info
    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- btmon
    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- bluetoothctl list

One limit: the image has no shell, and BlueZ's argument parser
runs one, so `bluetoothctl` and `btmgmt` refuse every command that
takes an argument ("Unable to parse mandatory command arguments").
Only their no-argument commands work: `bluetoothctl list`, and
`btmgmt info`, `extinfo`, `con`, `keys`, and `ltks`. `dbus-send`
has no such limit, so use it to reach anything else. To connect one
device by hand:

    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- \
      dbus-send --system --print-reply --dest=org.bluez \
      /org/bluez/hci0/dev_A0_AB_51_33_B7_12 org.bluez.Device1.Connect

## The privilege it takes

The pod is three containers, and the privilege is confined to one of
them. `NET_RAW` is the one capability `bluetoothd` itself does not
use. It is there for `btmon`, which binds the kernel's HCI monitor
channel, and that bind tests `CAP_NET_RAW`. The `bluetoothd`
container takes `hostNetwork` and five capabilities (`NET_ADMIN`,
`NET_RAW`, `NET_BIND_SERVICE`, `SETUID`, `SETGID`), because it is the
Bluetooth stack. The `operator` and `bondfetch` containers drop every
capability. The comments in
[`deploy/operator.yaml`](https://liken.sh/bluetooth/deploy/operator.yaml) state the kernel or
daemon check behind each grant.

The pod mounts four host paths: the two kubelet plugin directories
every DRA driver takes, `/var/run/cdi`, and
`/var/run/bluetooth.liken.sh/dbus`, which holds the D-Bus socket a
claim on the [media bus](https://liken.sh/bluetooth/docs/reference/devices/#the-media-bus)
delivers. The bus directory is a host path so that a prepared claim
names the same socket across a restart of this pod.

## Uninstall

Delete the workload. The published `ResourceSlice` stays, because the
operator does not retract it on shutdown. Its pod restarts for
ordinary reasons while consumers hold prepared claims. The `Node` owns
the slice, so a node that leaves the cluster takes it along. To
remove it now:

    kubectl delete resourceslice <node>-bluetooth.liken.sh
