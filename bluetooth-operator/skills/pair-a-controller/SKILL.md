---
name: pair-a-controller
description: "Pair a Bluetooth game controller or remote with kubectl, from the pairing window to a claim a pod holds. Use when a new controller must join a cluster, when approving a device the radio reports, or when unpairing one."
---

This skill is the guide at https://liken.sh/bluetooth/docs/guides/pair-a-controller/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Pair a controller and give it to a pod

This guide pairs a game controller with `kubectl` and hands it to one
pod. The example is a DualSense and a game in a namespace named
`arcade`, on a [`liken`](https://liken.sh/docs/) cluster with
[the operator installed](https://liken.sh/bluetooth/docs/guides/install/). Every step is a Kubernetes
API call, so RBAC controls who may do each one, and nobody needs a
shell on a node or in a pod.

<a id="the-interactive-shortcut"></a>

## Use the pairing command

`liken plugins sync` installs the `kubectl liken bluetooth` plugin, as
[Install the plugins](https://liken.sh/docs/reference/cli/#install-the-plugins)
describes. `kubectl liken bluetooth pair` runs steps 1 through 3 from
a laptop. It opens a window on the radio, lists the devices the radio reports as
they appear, and approves the one you pick. It drives the same
`PairingRequest` flow the numbered steps write by hand, and it reads
your kubeconfig, so RBAC governs it like every other call. The steps
below are the way to script the flow or to read each object it writes.

The command takes these flags:

| Flag | What it does |
| --- | --- |
| `--adapter` | The adapter to open the window on. The first argument after `pair` does the same. With neither, the command uses the cluster's only adapter and stops with an error when the cluster has none or more than one. |
| `--window` | How long the window stays open, in seconds. With no value, the `PairingRequest` takes its default of 180, and the range is 15 to 900. |
| `-n`, `--namespace` | The namespace of the `PairingRequest`. The default is `liken-system`. |
| `--force` | Silences the warning that the CLI's version differs from the operator's. The command runs either way. |
| `--version` | Prints the CLI's version and exits. |

## 1. Open a pairing window

Read the name of the adapter first. It is the radio's address in
lowercase with dashes:

    kubectl get adapters

Then create a `PairingRequest` for it:

    kubectl apply -f - <<'EOF'
    apiVersion: bluetooth.liken.sh/v1alpha1
    kind: PairingRequest
    metadata:
      name: new-gamepad
      namespace: liken-system
    spec:
      adapter: 04-4a-69-66-92-27
      windowSeconds: 180
    EOF

The operator opens a window on that radio. It scans, and the radio
stays pairable and discoverable, for `windowSeconds`. The default is
180, and the range is 15 to 900. Between windows the radio is neither
pairable nor discoverable, so nothing pairs with the cluster unless
somebody asked for a window.

## 2. Put the controller in pairing mode and read what the radio reports

On a DualSense, hold **Create** and **PS** until the light bar
flashes. Then read the request:

    kubectl get pairingrequest new-gamepad -n liken-system -o yaml

A device appears in `status.seen` when the scan finds it and the
cluster holds no bond with it. The entry has its address, its name,
and when the radio first observed it.

<a id="3-approve-the-device-you-meant"></a>

## 3. Approve the device

Approval is a write to the request's spec:

    kubectl patch pairingrequest new-gamepad -n liken-system \
      --type merge -p '{"spec":{"device":"A0:AB:51:33:B7:12"}}'

The operator pairs that device, trusts it, records the bond as a
`Peripheral`, and closes the window. Trust lets a later connection run
with no agent registered. It does not make the device connect. What
starts the connection depends on the device. A controller connects
when you press its own button. The operator connects a speaker
itself, whenever the speaker is powered on and in range. It retries a
failed attempt, and the wait between attempts doubles from 10 seconds
up to two minutes.

The request's `status.phase` goes to `Paired`. When `bluetoothd`
refuses the pairing, `status.message` gives its error, and
`kubectl describe pairingrequest` shows a `PairingRefused` warning.
The [install guide](https://liken.sh/bluetooth/docs/guides/install/#read-the-events) lists every
`Event` the operator posts. A request nobody
approves only scans. An empty `spec.device` never pairs anything, and
the window expires on its own. The finished request is collected
after `spec.ttlSecondsAfterFinished`, a day by default.

To re-pair a device the cluster already records, set `spec.device`
when you create the request. An address set at creation is an
approval in advance.

## 4. See the published device

The bond is now a `Peripheral`, its keys are in a `Secret` the
`Peripheral` owns, and the controller is a device in this node's
`ResourceSlice`:

    $ kubectl get resourceslice liken-1-bluetooth.liken.sh -o yaml
    spec:
      driver: bluetooth.liken.sh
      nodeName: liken-1
      devices:
        - name: a0-ab-51-33-b7-12
          attributes:
            address: {string: "A0:AB:51:33:B7:12"}
            connected: {bool: true}
            name: {string: "DualSense Wireless Controller"}

From here on, **PS** alone reconnects the controller. The keys are
in the `Secret`, so they survive a pod restart, an upgrade, and a
reboot.

## 5. Claim the controller

If the [Dynamic Resource Allocation
(DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
objects are new to you, read
[How a claim reaches your pod](https://liken.sh/bluetooth/docs/concepts/how-the-pieces-fit/) first. Then
create a
[`ResourceClaim`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-claim-v1/)
that selects the controller by its address:

    apiVersion: resource.k8s.io/v1
    kind: ResourceClaim
    metadata:
      name: player-one
      namespace: arcade
    spec:
      devices:
        requests:
          - name: controller
            exactly:
              deviceClassName: bluetooth-input
              selectors:
                - cel:
                    expression: |
                      device.attributes["bluetooth.liken.sh"].address == "A0:AB:51:33:B7:12"
              tolerations:
                - key: bluetooth.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

The toleration sets how long the radio may go silent before the
eviction controller ends the pod. Tolerate
`bluetooth.liken.sh/disconnected` and nothing else.
[Devices](https://liken.sh/bluetooth/docs/reference/devices/#the-taints) explains why the other
taint must stay untolerated. Leave out the selector to claim any
paired controller.

## 6. Give the claim to a pod

    apiVersion: v1
    kind: Pod
    metadata:
      name: player
      namespace: arcade
    spec:
      resourceClaims:
        - name: controller
          resourceClaimName: player-one
      containers:
        - name: game
          image: ...
          resources:
            claims:
              - name: controller

The container receives device nodes and nothing else:
`/dev/input/event*` for the one controller the claim allocated, which
on a DualSense is the gamepad and its motion sensors. No privilege,
no host mount, no environment variable. The container's user must be
able to open the nodes.

If the controller is switched off, the pod parks `Unschedulable` and
starts when somebody turns it on. If the controller disconnects while
the pod runs, the eviction after `tolerationSeconds` ends the pod's
session.

<a id="when-a-connected-controller-sends-no-input"></a>

### When a connected controller sends no input

`bluetoothd` can bring a controller's link up and never create its
HID device. The `Peripheral` then shows `Connected`, the device
drops its `disconnected` taint, and the pod receives no presses. The
operator finds this state. A paired controller that is connected, has
delivered input before, and has no Bluetooth HID device in the
kernel is stuck. After 15 seconds in that state, the operator calls
`Disconnect` and then `Connect` on the controller through
`bluetoothd`, which runs the input profile again.

When a reconnect does not bring the HID device back, the operator
waits a minute before the next one. The wait doubles after each
reconnect that does not help, up to 15 minutes. A device that has
never delivered input, such as a speaker that lists a HID profile and
never opens it, is never reconnected. The operator posts no `Event`
for a reconnect. The `operator` container's log has a line when it
starts one and when it finishes:

    kubectl -n liken-system logs ds/bluetooth-operator -c operator

In a `Deployment`, claim through a `ResourceClaimTemplate` instead of
a standing `ResourceClaim`. A standing claim keeps its allocation
across an eviction, so the `ReplicaSet`'s replacement pods would
schedule onto a device that is gone and be evicted at once. A
template gives each replacement pod a fresh claim. A fresh claim
needs a new allocation, which the taints block.

## Unpair

Deleting the `Peripheral` is the unpair:

    kubectl delete peripheral a0-ab-51-33-b7-12

`kubectl liken bluetooth unpair a0-ab-51-33-b7-12` deletes the same
`Peripheral`. In bash it completes the paired device names.

The operator disconnects the controller, waits for any claim on it to
release, retires the device from the slice, and removes the bond. The
`Secret` with the keys is owned by the `Peripheral`, so it is
collected with the object.
