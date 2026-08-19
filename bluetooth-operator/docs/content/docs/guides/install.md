---
title: Install the operator
weight: 10
---

# Install the operator

This guide installs `bluetooth-operator` on a
[`liken`](https://liken.sh/docs/) cluster and verifies that it holds
the radio. The operator is an ordinary workload: everything it needs
is in one `kustomize` base, and nothing here touches a machine over SSH.

## What you need

* A `liken` cluster. The operator claims the Bluetooth adapter from
  `liken`'s own [Dynamic Resource Allocation
  (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
  driver, so the cluster's operating system is what publishes the raw
  hardware. [Devices](https://liken.sh/docs/reference/devices/)
  describes that inventory.
* A machine with a USB Bluetooth adapter. The operator selects on the
  `btusb` kernel driver, which covers the plug-in dongles and the
  radios built into a board. You do not have to say which machine has
  the radio: the claim places the pod where the radio is.

## The device classes

A [`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
is cluster-scoped policy, the same convention a `StorageClass`
follows: the cluster owner names and curates the classes workloads
may ask for. The classes split by owner. If the DRA objects are new
to you, read
[How the pieces fit](/docs/guides/#how-the-pieces-fit) first.

* `bluetooth-adapter` is wiring, and the base ships it, served at
  [`deviceclasses.yaml`](/deploy/deviceclasses.yaml). The
  operator's own pod claims the raw radio through it, its selector
  picks the `btusb` adapter that `liken` publishes, and the claim
  template in [`operator.yaml`](/deploy/operator.yaml) names it
  literally, so the operator cannot start without it. Do not
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
bus](/docs/reference/devices/#the-media-bus) out of this class. The
bus is the audio operator's to claim, through a class of its own that
names the shared `sound.liken.sh/supportsSound` attribute.

### Generic or specific

A class is the cluster's vocabulary for a kind of device, and you
choose its grain. A generic class such as `bluetooth-input`
matches every paired input device: the class list stays short, and
each claim picks its controller with a CEL selector. A specific
class holds the selector itself. This one matches exactly one
controller, so a claim names the class and writes no CEL, and you
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
[/deploy/](/deploy/kustomization.yaml), so you can install from here
without a clone. Apply the three files into `liken-system`, the
namespace a `liken` cluster already has:

    kubectl apply -n liken-system \
      -f https://bluetooth.liken.sh/deploy/crds.yaml \
      -f https://bluetooth.liken.sh/deploy/rbac.yaml \
      -f https://bluetooth.liken.sh/deploy/operator.yaml

Or point your own GitOps at the same files with a `Kustomization`:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    namespace: liken-system
    resources:
      - https://bluetooth.liken.sh/deploy/crds.yaml
      - https://bluetooth.liken.sh/deploy/rbac.yaml
      - https://bluetooth.liken.sh/deploy/operator.yaml

The site serves the manifests of the current `main`, and the images
in `operator.yaml` name `:latest`. To pin a release instead,
reference the repository's `kustomize` base at a release tag. One tag
versions the manifests and the three images together, so pin all four
to the same version:

    apiVersion: kustomize.config.k8s.io/v1beta1
    kind: Kustomization
    resources:
      - https://github.com/liken-sh/bluetooth-operator//deploy?ref=2026.08.17-022
    images:
      - name: ghcr.io/liken-sh/bluetooth-operator
        newTag: 2026.08.17-022
      - name: ghcr.io/liken-sh/bluetoothd
        newTag: 2026.08.17-022
      - name: ghcr.io/liken-sh/bluetooth-bondfetch
        newTag: 2026.08.17-022

Whichever path you take, the manifests contain:

* The `bluetooth-adapter` `DeviceClass`, the wiring the operator's
  own claim names. Your consumer class, such as `bluetooth-input`
  above, is not in the manifests: you create it.
* The three `CustomResourceDefinitions` of the pairing API:
  `Adapter`, `Pairing`, and `PairingRequest`. The operator records
  every bond as a `Pairing` and stores its keys in a `Secret` that
  the `Pairing` owns, so install the CRDs with the workload.
* The operator's `ServiceAccount` and its RBAC.
* A `DaemonSet` and the `ResourceClaimTemplate` its pods claim the
  adapter through.

## How the pod finds the radio

The `DaemonSet` puts a pod on every node, and each pod claims one
`bluetooth-adapter` device. On a node with an adapter the claim
matches and the pod runs. On a node with no adapter the claim matches
nothing, so the pod parks `Pending` and costs nothing. Nobody writes
down which machine has the radio, and a dongle moved to another
machine is served there on the next pod start.

The claim also makes the pod the only Bluetooth stack on that radio,
because `liken` publishes the adapter as a device that allocates
once. The kernel arbitrates nothing between two stacks on one
adapter.

## Verify

    kubectl get pods -n liken-system -l app=bluetooth-operator

One `Running` pod on each machine with an adapter, and a `Pending`
pod on each machine without one, is the healthy shape. Then read the
radio the operator holds:

    $ kubectl get adapters
    NAME                ALIAS   ADDRESS             NODE      POWERED   AGE
    04-4a-69-66-92-27           04:4A:69:66:92:27   liken-1   true      1m

The operator creates an `Adapter` object for the radio its pod
claimed, named for the radio's address. The `ResourceSlice` of paired
controllers appears when the first controller is paired:
[Pair a controller and give it to a pod](/docs/guides/pair-a-controller/) is
the next step.

## Look inside the stack

The `bluetoothd` image holds four tools for a person. Each runs as
a direct `kubectl exec`, with no shell between, and every one of
them needs the `-i` flag: BlueZ's shells attach to their standard
input, and with stdin closed the attach fails and the command never
runs, with nothing printed.

`btmgmt info` prints the adapter's management settings, and its
`current settings` line is where `Connectable`, `Discoverable`, and
`Bondable` read. `btmon` traces the HCI link live, the layer under
D-Bus and under `bluetoothd`: it shows a disconnect reason or a
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
has no such limit, so it is the way to reach anything else. This is
how you connect one device by hand:

    kubectl -n liken-system exec -i ds/bluetooth-operator -c bluetoothd -- \
      dbus-send --system --print-reply --dest=org.bluez \
      /org/bluez/hci0/dev_A0_AB_51_33_B7_12 org.bluez.Device1.Connect

## The privilege it takes

The pod is three containers, and the privilege is confined to one of
them.
`NET_RAW` is the one capability `bluetoothd` itself does not use:
it is there for `btmon`, whose bind of the kernel's HCI monitor
channel tests `CAP_NET_RAW`.
The `bluetoothd` container takes `hostNetwork` and five
capabilities (`NET_ADMIN`, `NET_RAW`, `NET_BIND_SERVICE`, `SETUID`,
`SETGID`), because it is the Bluetooth stack. The `operator` and `bondfetch`
containers drop every capability. The comments in
[`deploy/operator.yaml`](/deploy/operator.yaml) state the kernel or
daemon check behind each grant.

The pod mounts four host paths: the two kubelet plugin directories
every DRA driver takes, `/var/run/cdi`, and
`/var/run/bluetooth.liken.sh/dbus`, which holds the D-Bus socket a
claim on the [media bus](/docs/reference/devices/#the-media-bus)
delivers. The bus directory is a host path so that a prepared claim
names the same socket across a restart of this pod.

## Uninstall

Delete the workload. The published `ResourceSlice` stays, because the
operator does not retract it on shutdown: its pod restarts for
ordinary reasons while consumers hold prepared claims. The `Node` owns
the slice, so a node that leaves the cluster takes it along. To
remove it now:

    kubectl delete resourceslice <node>-bluetooth.liken.sh
