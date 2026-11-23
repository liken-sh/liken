---
name: pair-a-controller
description: "Pair a Bluetooth game controller or remote with kubectl, from the pairing window to a claim a pod holds. Use when a new controller must join a cluster, when approving a device the radio reports, or when unpairing one."
---

This skill is the guide at https://liken.sh/bluetooth/docs/guides/pair-a-controller/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

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
a laptop. It opens a pairing window on the radio, lists the devices
the radio finds as they appear, and approves the one you pick. It
creates the same `PairingRequest` that the steps below write by hand,
with the credentials of your kubeconfig. Follow the steps instead when
you want to script pairing or see each object.

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

The radio scans for devices, and is pairable and discoverable, for
`windowSeconds` seconds. The default is 180, and the range is 15 to
900. When no
window is open, the radio is neither pairable nor discoverable, so no
device can pair with the cluster unless somebody opened a window.

## 2. Put the controller in pairing mode and read what the radio reports

On a DualSense, hold **Create** and **PS** until the light bar
flashes. Then read the request:

    kubectl get pairingrequest new-gamepad -n liken-system -o yaml

A device appears in `status.seen` when the scan finds it and the
cluster has no bond with it. Each entry gives the device's address,
its name, and when the scan first found it.

<a id="3-approve-the-device-you-meant"></a>

## 3. Approve the device

Approval is a write to the request's spec:

    kubectl patch pairingrequest new-gamepad -n liken-system \
      --type merge -p '{"spec":{"device":"A0:AB:51:33:B7:12"}}'

The operator pairs that device, trusts it, records the bond as a
`Peripheral`, and closes the window. Trust lets the device connect
later without a pairing agent, but it does not start a connection. A
controller connects when you press its button. A speaker does not
connect on its own, so the operator connects it whenever it is powered
on and in range. When an attempt fails, the operator tries again, and
the wait between attempts doubles from 10 seconds up to two minutes.

The request's `status.phase` goes to `Paired`. When `bluetoothd`
refuses the pairing, `status.message` gives its error, and
`kubectl describe pairingrequest` shows a `PairingRefused` warning.
The [install guide](https://liken.sh/bluetooth/docs/guides/install/#read-the-events) lists every
`Event` the operator posts. While `spec.device` is empty, the request
only scans, and the window closes when its time runs out. The operator
deletes a finished request after `spec.ttlSecondsAfterFinished`, which
is one day by default.

To pair a device again that the cluster already has a bond with, set
`spec.device` when you create the request. The operator then pairs
that address as soon as the window opens.

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

The `Adapter`'s `spec.privacy` affects Low Energy links only, such as a
remote's. A DualSense pairs and reconnects over a classic link, which
always uses the radio's public address.
[Privacy](https://liken.sh/bluetooth/docs/concepts/privacy/) explains the setting.

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

`tolerationSeconds` sets how long the controller may stay
disconnected before Kubernetes evicts the pod. Tolerate
`bluetooth.liken.sh/disconnected` and nothing else.
[Devices](https://liken.sh/bluetooth/docs/reference/devices/#the-taints) explains why the other
taints must stay untolerated. Leave out the selector to claim any
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

The container receives the `/dev/input/event*` nodes of the
controller that the claim allocated. On a DualSense, those are the
gamepad and its motion sensors. The claim adds no privilege, no host
mount, and no environment variable. The container's user must be
able to open the nodes.

If the controller is switched off, the pod stays `Pending` because the
scheduler cannot allocate the claim. The pod starts after somebody
turns the controller on. If the controller disconnects while the pod
runs, Kubernetes evicts the pod after `tolerationSeconds`.

In a `Deployment`, use a `ResourceClaimTemplate` instead of a
`ResourceClaim`. A `ResourceClaim` keeps its allocation after an
eviction, so each replacement pod would schedule onto the
disconnected controller and be evicted at once. A template creates a
new claim for each pod, and the taints keep the scheduler from
allocating a disconnected controller to it.

<a id="when-a-connected-controller-sends-no-input"></a>

### When a connected controller sends no input

`bluetoothd` can bring a controller's link up and never create its
HID device. The `Peripheral` then shows `Connected`, the device
drops its `disconnected` taint, and the pod receives no presses. The
operator treats a controller as stuck when it is connected, has sent
input before, and the kernel has no Bluetooth HID device for it.
After 15 seconds in that state, the operator calls
`Disconnect` and then `Connect` on the controller through
`bluetoothd`, which runs the input profile again.

`bluetoothd` can also report a link that the kernel no longer has.
Then the `Disconnect` fails or gets no reply, and a `Connect` would
do nothing. So when the `Disconnect` fails, the operator powers the
adapter off and on through `bluetoothd` instead. The power cycle
drops every link on the adapter, not only the stuck one. Each
controller connects again on its next press, and the operator
connects each speaker again. The operator posts an `AdapterPowerCycled`
`Event` on the stuck controller's `Peripheral`. If `bluetoothd` does
not power the adapter on again, the operator posts an
`AdapterPowerOnFailed` Warning, and the adapter stays off until you
delete the operator's pod on that node.

When a reconnect or a power cycle does not bring the HID device back,
the operator waits a minute before the next one. The wait doubles
after each one that does not help, up to 15 minutes. The operator
never reconnects a device that has not sent input, such as a speaker
that lists a HID profile and never opens it. The operator posts no
`Event` for a reconnect, but the `operator` container logs a line
when a reconnect or a power cycle starts and when it finishes:

    kubectl -n liken-system logs ds/bluetooth-operator -c operator

<a id="why-a-controller-is-not-connected"></a>

### Why a controller is not connected

`kubectl get peripherals` shows each bond's link, the reason of its
`Connected` condition, and how long ago the link came up or ended:

    $ kubectl get peripherals
    NAME                ALIAS         DEVICE      ADDRESS             BATTERY   PAIRED   CONNECTED   REASON   SINCE   NODE     AGE
    c2-11-22-33-44-55   sofa-remote   T6-Remote   C2:11:22:33:44:55   75        true     False       Asleep   42m     node-2   24d

The reason comes from `bluetoothd`'s report of why the link ended:

| Reason | What it means |
|---|---|
| `LinkUp` | The link is up. |
| `Asleep` | The link timed out on a device that sleeps between sessions. A T6 remote stops answering after an idle hour, and its next press connects it again. |
| `LinkLost` | The link timed out on a device that does not sleep. It went out of range, or its battery ran out. |
| `ClosedByDevice` | The device ended the link: it was switched off, or it disconnected. An X6 air-mouse remote ends its own link after an idle time, and its next press connects it again. |
| `ClosedByRadio` | This radio ended the link, at an unpair or when the operator reconnected a controller that sent no input. |
| `AuthenticationFailed` | The keys did not match. Delete the `Peripheral` and pair the device again. |
| `NotConnected` | The operator received no reason. The link ended while the operator was not running, or `bluetoothd` restarted. |
| `NotBonded` | `bluetoothd` has no object for the device. |

The reason changes only when the link changes, so a remote that
sleeps keeps `Asleep` when the operator restarts. The operator posts
an `Event` on the `Peripheral` for each change, as the
[install guide](https://liken.sh/bluetooth/docs/guides/install/#read-the-events) lists.

<a id="when-a-remote-wakes-from-sleep"></a>

### When a remote wakes from sleep

A remote that sleeps connects again when you press a button, and
remotes differ in what they send for that press:

* An X6 air-mouse remote ends its own link, so its reason is
  `ClosedByDevice`. It sends the button you pressed once the link is
  up, and that press reaches the claim like any other.
* A T6 remote lets its link time out, so its reason is `Asleep`. It
  sends no key for the press that wakes it. So when a remote whose
  reason is `Asleep` connects again, at least 10 seconds after its
  link ended, the operator writes one `KEY_UNKNOWN` press into the
  remote's virtual node, and the operator's log has a line for it.

Either way, one press of any button wakes the remote and a sleeping
screen. When nothing plays and the screen's panel is on, a real
button press also acts on the screen, so a press of down on an X6
wakes the screen and moves the focus. `KEY_UNKNOWN` has no action on
a screen, so a T6's first press only wakes it. The
[idle screen guide](https://liken.sh/media/docs/guides/handing-the-idle-screen-to-another-controller/#read-the-presses)
in the `media-operator` manual gives the rules.

If a T6 remote fell asleep during a film, your first press, such as
pause, only wakes the remote, and the second press pauses.

A remote that went out of range also gets the reason `Asleep`. When
it comes back into range, it can connect with no press, and the
operator writes the `KEY_UNKNOWN` press then. So the screen can wake
when a remote comes back into range, although nobody pressed a
button.

<a id="when-a-sleeping-remote-does-not-reconnect"></a>

### When a sleeping remote does not reconnect

A Low Energy remote reconnects because the kernel scans for it while
it sleeps, and connects when a press makes it advertise. Bugs in
Linux and in BlueZ can stop that scan or that connection, with no
log line, until the pod restarts. The pod works around each of them.

- **The kernel leaves the scan off after a failed connection.** When
  a connection to the remote fails, for example because the remote
  stopped advertising while the radio connected, Linux can leave the
  scan off.
- **The kernel forgets the remote after a link that closes late.**
  When the kernel ends a link to the remote, it waits 2 seconds for
  the radio to close it. A remote that went quiet can make the radio
  take longer. Then Linux takes the remote off its list of devices to
  reconnect, and `bluetoothd` still reports the remote connected.
- **BlueZ 5.87 and 5.88 turn off the reconnect after an
  authentication failure.** When the remote connects again within 4 seconds of a
  link that ended with an authentication failure, `bluetoothd` never
  turns the kernel's reconnect of the remote back on. The
  `bluetoothd` image includes a patch for this. The image lists its
  patches in `/usr/share/doc/liken/bluez-patches/`.

The pod's `rescan` container watches for both kernel bugs on the
kernel's HCI monitor channel. When a connection to a device that
`bluetoothd` reconnects on its own fails, or the kernel ends a link
to it, and the kernel does not start a scan within 5 seconds, the
container adds the device to the kernel's list again, and the kernel
starts the scan. No link drops. The container logs a line each time:

    kubectl -n liken-system logs ds/bluetooth-operator -c rescan

If the `rescan` log says the kernel did not scan after the device was
added again, or a remote stops reconnecting with no line in that log,
turn on the [btmon trace](https://liken.sh/bluetooth/docs/guides/install/#look-inside-the-stack)
for the radio, press the remote's buttons, and read the trace before
you delete the pod. A restart of the pod recovers the remote, and it
also ends the state that the trace shows.

## Unpair

To unpair a device, delete its `Peripheral`:

    kubectl delete peripheral a0-ab-51-33-b7-12

`kubectl liken bluetooth unpair a0-ab-51-33-b7-12` deletes the same
`Peripheral`. In bash, the plugin's completion completes the names
of paired devices.

The operator disconnects the controller, waits until no claim holds
it, removes the device from the `ResourceSlice`, and deletes the bond
from `bluetoothd`. The `Peripheral` owns the `Secret` that holds the
keys, so Kubernetes deletes the `Secret` with it.
