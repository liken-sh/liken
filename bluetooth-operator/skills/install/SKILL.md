---
name: install
description: "Install bluetooth-operator on a liken cluster and verify that it claims the radio. Use when a cluster has no Bluetooth devices yet, when running a development build, or when removing the operator."
---

This skill is the guide at https://liken.sh/bluetooth/docs/guides/install/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

<a id="install-the-operator"></a>

# Install and verify the operator

At the end of this guide, `bluetooth-operator` runs on your
[`liken`](https://liken.sh/docs/) cluster and its pod has a claim on
the machine's Bluetooth radio. The operator is an ordinary workload in one
`kustomize` base, so you need no SSH access to any machine.

## What you need

* A `liken` cluster. The operator claims the Bluetooth adapter from
  `liken`'s own [Dynamic Resource Allocation
  (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
  driver, which publishes the machine's hardware as devices.
  [Devices](https://liken.sh/docs/reference/devices/) lists them.
* A machine with a Bluetooth adapter that uses the `btusb` kernel
  driver, as USB dongles and most radios built into a board do. You
  do not say which machine has the radio, because the scheduler puts
  the pod on a machine that has one.

<a id="the-device-classes"></a>

## Create the device classes

A [`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
is a cluster-scoped object, like a `StorageClass`. It selects a kind
of device, and the cluster owner decides which classes exist. If DRA
is new to you, read
[How a claim reaches your pod](https://liken.sh/bluetooth/docs/concepts/how-the-pieces-fit/) first.

* `bluetooth-adapter` selects the radio itself. The base ships it in
  [`deviceclasses.yaml`](https://liken.sh/bluetooth/deploy/deviceclasses.yaml). Its selector
  matches the `btusb` adapter that `liken` publishes, and the
  operator's own pod claims the radio through it. The claim template
  in [`operator.yaml`](https://liken.sh/bluetooth/deploy/operator.yaml) names this class, so
  the operator cannot start without it. Do not delete it.
* The class that your workloads claim through is the one you
  create. The base does not ship it, because which controllers a
  workload may claim is a decision for the cluster owner.
  Start with `bluetooth-input`. Its selector requires the `input`
  attribute, which only a paired controller has, so the class does
  not match the
  [media bus](https://liken.sh/bluetooth/docs/reference/devices/#the-media-bus) that the driver
  also publishes:

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

`audio-operator` claims the media bus through a class of its own that
selects on the `sound.liken.sh/supportsSound` attribute.

### Generic or specific

A class can match every paired controller, or only one. With a
general class such as `bluetooth-input`, each claim picks its
controller with a CEL selector of its own. A specific class puts that
selector in the class, so a claim names the class and needs no CEL.
This class matches one controller:

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

Create a specific class when several workloads repeat the same
selector, or when the cluster owner, and not each workload's
manifest, should choose the controller.

## Apply the manifests

This site serves the repository's manifests as raw YAML under
[/deploy/](https://liken.sh/bluetooth/deploy/kustomization.yaml), so you can install from here
without a clone. Apply the files into `liken-system`, the namespace
that every `liken` cluster has:

    kubectl apply -n liken-system \
      -f https://liken.sh/bluetooth/deploy/deviceclasses.yaml \
      -f https://liken.sh/bluetooth/deploy/crds.yaml \
      -f https://liken.sh/bluetooth/deploy/rbac.yaml \
      -f https://liken.sh/bluetooth/deploy/operator.yaml

Or point your own GitOps at the same files with a `Kustomization`:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - https://liken.sh/bluetooth/deploy/deviceclasses.yaml
      - https://liken.sh/bluetooth/deploy/crds.yaml
      - https://liken.sh/bluetooth/deploy/rbac.yaml
      - https://liken.sh/bluetooth/deploy/operator.yaml

The site serves the manifests of the current `main`, and the images
in `operator.yaml` use the tag `:latest`. To pin a release, reference
the `kustomize` base at the release tag that last published the
operator. The
[GitHub release](https://github.com/liken-sh/liken/releases) of each
tag lists every component and its version. The manifests and the
images have the same version, so pin each of them to it:

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

The manifests contain:

* The `bluetooth-adapter` `DeviceClass`.
* The `CustomResourceDefinitions` of `Adapter`, `Peripheral`, and
  `PairingRequest`. The operator creates a `Peripheral` for every
  bond and stores the bond's keys in a `Secret` that the `Peripheral`
  owns.
* The operator's `ServiceAccount` and its RBAC.
* A `DaemonSet`, and the `ResourceClaimTemplate` through which its
  pods claim the adapter.

## Keep the pods off nodes with no Bluetooth adapter

The `DaemonSet` creates a pod on every node. On a node with no
Bluetooth adapter, the pod's claim matches no device and the pod stays
`Pending`. To keep the pod off such a node, label the node
`bluetooth.liken.sh/bluetooth: none`. The `DaemonSet` in the base has
this node affinity, so you need no patch:

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

`NotIn` also matches a node that has no such label, as the Kubernetes
page on
[set-based requirements](https://kubernetes.io/docs/concepts/overview/working-with-objects/labels/#set-based-requirement)
describes, so a node with no label gets a pod. When a label changes, the
[`DaemonSet`](https://kubernetes.io/docs/concepts/workloads/controllers/daemonset/)
controller deletes the pod from a node that no longer matches and
creates one on a node that matches again.

Declare the label in the node's `Machine`, so that it stays with the
machine's declaration:

    spec:
      nodeLabels:
        bluetooth.liken.sh/bluetooth: none

A `Machine` accepts a key in a `liken.sh` subdomain from `liken`
2026.09.28-002 on. On an older release, label the node with
`kubectl` as shown below.

The `liken` machine operator applies the label to the node, and
applies it to the new `Node` when the machine is demoted or installed
again.
To run the pod on the node again, remove the key from
`spec.nodeLabels`. `liken` then removes the label from the node, also
when you first set it with `kubectl`.

To label the node with `kubectl` instead:

    kubectl label node node-1 bluetooth.liken.sh/bluetooth=none

The label stays on the node across reboots, because `liken` removes
only the labels that a `Machine` declared. A machine that is demoted
or installed again registers a new `Node`, which you must label again.
To run the pod on the node again, remove the label:

    kubectl label node node-1 bluetooth.liken.sh/bluetooth-

A patch that sets a node affinity on this `DaemonSet` replaces the
base's list of terms, with the `none` requirement in it. Copy that
requirement into each term of your patch.

## Running a development build

A push to `main` that changes the operator publishes a development
build of it. Its version is the most recent release tag plus a suffix.
For example, `2026.09.03-007-dev-003-abcdef01` is the build of commit
`abcdef01`, which is three commits after release `2026.09.03-007`.
Every image of the operator has the same version, and `:latest` still
points to the most recent release.

A development build has no git tag, so reference the manifests by the
commit's full SHA and the images by the version:

    resources:
      - https://github.com/liken-sh/liken//bluetooth-operator/deploy?ref=<full 40-character sha>
    images:
      - name: ghcr.io/liken-sh/bluetooth-operator
        newTag: 2026.09.03-007-dev-003-abcdef01
      - name: ghcr.io/liken-sh/bluetoothd
        newTag: 2026.09.03-007-dev-003-abcdef01
      - name: ghcr.io/liken-sh/bluetooth-bondfetch
        newTag: 2026.09.03-007-dev-003-abcdef01

A git fetch by SHA needs all forty characters, so the eight in the
version are not enough. The summary of the commit's CI run shows the
version.

The same build publishes the manifests as the OCI artifact
`oci://ghcr.io/liken-sh/bluetooth-operator-deploy:<version>`, with each
image of the operator set to that version. A Flux `OCIRepository`
can pull that artifact by version, with no SHA.

## How the pod finds the radio

Each pod claims one `bluetooth-adapter` device, so the pod runs on a
node that has an adapter, and a dongle that you move to another
machine works there when the pod on that machine starts. The claim
also makes the pod the only Bluetooth stack on the radio, because
only one claim can hold the adapter. The kernel does not stop two
Bluetooth stacks from using one adapter.

When the `bluetoothd` container starts, it powers every adapter off
before `bluetoothd` runs, and `bluetoothd` then powers each adapter
on. This ends the links that a killed or crashed `bluetoothd` left
up. A new `bluetoothd` that inherits such a link marks the device
connected but never opens its input, so the controller sends nothing.
Each controller connects again when you next press its button. The
`bluetoothd` container logs a line for each adapter:

    kubectl -n liken-system logs ds/bluetooth-operator -c bluetoothd

The `bluetoothd` image builds BlueZ from a release, with patches for
bugs that stop a controller from reconnecting. The `rescan` container
works around kernel bugs that have the same effect.
[When a sleeping remote does not reconnect](https://liken.sh/bluetooth/docs/guides/pair-a-controller/#when-a-sleeping-remote-does-not-reconnect)
describes each one.

## Verify

    kubectl get pods -n liken-system -l app=bluetooth-operator

A healthy install has one `Running` pod on each machine with an
adapter. A machine without one has a `Pending` pod, or no pod when
its node is labeled `bluetooth.liken.sh/bluetooth: none`. Then list
the radios that the operator found:

    $ kubectl get adapters
    NAME                ALIAS   ADDRESS             NODE      POWERED   PRIVACY   BTMON   AGE
    04-4a-69-66-92-27           04:4A:69:66:92:27   liken-1   true      off       false   1m

The operator creates an `Adapter` for the radio that its pod
claimed, and names it for the radio's address.
[Pair a controller and give it to a pod](https://liken.sh/bluetooth/docs/guides/pair-a-controller/)
is the next step.

## Read the Events

The operator posts a Kubernetes `Event` for each change in pairing,
in a controller's link, and in the radio, and for each action it
takes to recover a controller. The table below lists each reason.
Read the `Event`s with `kubectl describe` on the object. A `Peripheral`, an
`Adapter`, and a `Node` are cluster-scoped, so their `Event`s are in
the `default` namespace, and `kubectl events --for` finds them only
with `-n default` or `-A`:

    kubectl describe pairingrequest new-gamepad -n liken-system
    kubectl events -n default --for peripheral/a0-ab-51-33-b7-12

| Reason | Type | Object | What happened |
|---|---|---|---|
| `PairingWindowOpened` | Normal | `PairingRequest` | The radio is discoverable and pairable until the window closes. |
| `PairingWindowExpired` | Normal | `PairingRequest` | The window closed with no device paired. |
| `PairingRefused` | Warning | `PairingRequest` | `bluetoothd` refused to pair the approved device. The window tries again, and the `Event` repeats only when the refusal changes. |
| `Paired` | Normal | `PairingRequest`, `Peripheral` | The device paired, and the operator created its `Peripheral`. |
| `BondLost` | Warning | `Peripheral` | `bluetoothd` has no bond with the device any more. The `Peripheral` stays until you delete it. |
| `LinkUp` | Normal | `Peripheral` | The device connected. |
| `Asleep` | Normal | `Peripheral` | The link timed out on a device that sleeps between sessions, such as a T6 remote after an idle hour. |
| `LinkLost` | Normal | `Peripheral` | The link timed out on a device that does not sleep: it went out of range, or its battery ran out. |
| `ClosedByDevice` | Normal | `Peripheral` | The device ended the link: it was switched off, or it disconnected, as an X6 air-mouse remote does after an idle time. |
| `ClosedByRadio` | Normal | `Peripheral` | This radio ended the link, at an unpair or when the operator reconnected a controller that sent no input. |
| `AuthenticationFailed` | Warning | `Peripheral` | The link ended because the keys did not match. Delete the `Peripheral` and pair the device again. |
| `NotConnected` | Normal | `Peripheral` | The link ended, and the operator received no reason from `bluetoothd`. |
| `NotBonded` | Normal | `Peripheral` | `bluetoothd` has no object for the device. `BondLost` follows when it had a bond before. |
| `InputRelayFailed` | Warning | `Peripheral` | The operator could not make the virtual input device that a claim on the controller receives. |
| `AdapterPowerCycled` | Normal | `Peripheral` | `bluetoothd` did not disconnect a controller that has a link and no input, so the operator powered the adapter off and on. Every link on the adapter dropped. |
| `AdapterPowerOnFailed` | Warning | `Peripheral` | The operator powered the adapter off to recover the controller, and `bluetoothd` did not power it on again. Delete the operator's pod on that node to power the adapter on. |
| `RadioClaimed` | Normal | `Node` | `bluetoothd` in the pod reports the radio. |
| `RadioLost` | Warning | `Node` | The radio is gone from `bluetoothd`: the adapter was unplugged or reset. |
| `PrivacyChanged` | Normal | `Adapter` | `spec.privacy` differs from the value `bluetoothd` started with, and the operator deletes its own pod so that `bluetoothd` starts with the new value. |
| `BtmonChanged` | Normal | `Adapter` | `spec.btmon` differs from the value the `btmon` container reads, and the operator wrote the new value, so the container starts or stops its trace. |

The reasons from `LinkUp` to `NotBonded` are the reasons of the
`Peripheral`'s `Connected` condition, and each change of the condition
posts one `Event`. A Low Energy remote ends its link after an idle
time, so each session of a remote posts one `Event` when it connects
and one when it disconnects. The `bluetooth_disconnects_total` metric
counts the disconnects by reason.

When a controller has a link but sends no input, the operator
reconnects it with a disconnect and a connect through `bluetoothd`.
The reconnect posts no `Event`, and the `operator` container logs it.
When `bluetoothd` fails the disconnect, the operator powers the
adapter off and on and posts `AdapterPowerCycled`, as
[Pair a controller](https://liken.sh/bluetooth/docs/guides/pair-a-controller/#when-a-connected-controller-sends-no-input)
describes.

The API server deletes an `Event` an hour after its last write, so
read the object's status and the pod's log for older facts.

<a id="look-inside-the-stack"></a>

## Inspect the Bluetooth stack

The `bluetoothd` image has `btmgmt`, `dbus-send`, and `bluetoothctl`.
Run each with `kubectl exec` directly, with no shell, and always pass
`-i`, because BlueZ's tools read standard input and, when it is
closed, exit without running the command or printing anything.

`btmgmt info` prints the adapter's management settings. Its
`current settings` line shows whether the adapter is `Connectable`,
`Discoverable`, and `Bondable`. `dbus-send` calls any method on
`org.bluez`. `bluetoothctl list` lists the adapters that `bluetoothd`
manages.

    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- btmgmt info
    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- bluetoothctl list

The image has no shell, and BlueZ's argument parser starts one, so
`bluetoothctl` and `btmgmt` refuse every command that takes an
argument with "Unable to parse mandatory command arguments". Only
their commands without arguments work: `bluetoothctl list`, and
`btmgmt info`, `extinfo`, `con`, `keys`, and `ltks`. `dbus-send` has
no such limit, so use it for anything else. To connect one device by
hand:

    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- \
      dbus-send --system --print-reply --dest=org.bluez \
      /org/bluez/hci0/dev_A0_AB_51_33_B7_12 org.bluez.Device1.Connect

The pod's `btmon` container can trace the HCI link, the layer below
`bluetoothd`, which shows a disconnect reason or a retransmission
that no higher layer reports. The trace goes to the container's log,
so you can read it after the pod restarts. It is off by default,
because `btmon` prints the link keys, the long term keys, and the
radio's identity key in plain text, and anyone who can read the pod's
logs can read them. Turn the trace on for one radio while you
diagnose a fault, with `spec.btmon` on its `Adapter`:

    kubectl patch adapter <adapter> --type merge -p '{"spec":{"btmon":true}}'

The trace starts within seconds. The pod does not restart, so no
controller disconnects. When a pod starts for an `Adapter` that
already has `spec.btmon: true`, the trace begins before `bluetoothd`
starts, so it also shows the commands where `bluetoothd` tells the
kernel which bonded devices may reconnect. Read the trace on one
machine:

    kubectl -n liken-system logs -c btmon --timestamps \
      $(kubectl -n liken-system get pods -l app=bluetooth-operator \
          --field-selector spec.nodeName=<node> -o name)

Turn the trace off with the same patch and `false` in place of
`true`. The lines already in the log stay until the kubelet rotates
the log or the pod is deleted.

## The privileges of the pod

The `bluetoothd` container adds `NET_ADMIN`, `NET_BIND_SERVICE`,
`SETUID`, and `SETGID`, because it is the Bluetooth stack. The
`btmon` container adds only `NET_RAW`, because it binds the kernel's
HCI monitor channel and that bind requires `CAP_NET_RAW`. The
`rescan` container adds `NET_RAW` for the same bind and `NET_ADMIN`
for the one management command it sends, as
[When a sleeping remote does not reconnect](https://liken.sh/bluetooth/docs/guides/pair-a-controller/#when-a-sleeping-remote-does-not-reconnect)
explains. The `operator` and `bondfetch` containers drop every
capability. Every container uses the pod's `hostNetwork`, because
Bluetooth sockets exist only in the host's network namespace. The
comments in [`deploy/operator.yaml`](https://liken.sh/bluetooth/deploy/operator.yaml) give the
kernel or daemon check behind each capability.

The pod mounts the kubelet plugin directories that every DRA driver
mounts, `/var/run/cdi`, and `/var/run/bluetooth.liken.sh/dbus` from
the host. The last holds the D-Bus socket that a claim on the
[media bus](https://liken.sh/bluetooth/docs/reference/devices/#the-media-bus) gives to a
workload. It is a host path so that a claim that was already prepared
points to the same socket after this pod restarts.

## Uninstall

Delete the workload:

    kubectl delete -n liken-system -f https://liken.sh/bluetooth/deploy/operator.yaml

The operator's `ResourceSlice` remains, because the operator does not
delete it when its pod stops. A pod restart is ordinary, and the
workloads keep their claims on the controllers across it. The `Node`
owns the slice, so the slice is deleted when the node leaves the
cluster. To delete the slice now:

    kubectl delete resourceslice <node>-bluetooth.liken.sh
