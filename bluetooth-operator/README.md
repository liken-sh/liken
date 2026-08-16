# bluetooth-operator

A Kubernetes DRA driver that publishes each paired Bluetooth
controller as its own device. A pod claims one controller by its MAC
address and receives that controller's evdev nodes, and no other input
device on the machine.

This is an instance of liken's device operator pattern (milestone 56).
liken publishes the facts about hardware that no other layer can
observe, and the paired set is not one of them: it lives in
bluetoothd, so the layer that publishes controllers has to be the
layer that runs bluetoothd. That daemon does not belong in the
read-only root that every machine boots, where every machine would
carry it for the one machine that uses it. So the operator is an
ordinary workload. It claims the Bluetooth adapter through an ordinary
`liken.sh` claim, runs bluetoothd beside itself in the same pod, and
publishes what bluetoothd holds under its own driver name,
`bluetooth.liken.sh`. The system image carries no BlueZ and no D-Bus.

The operator uses no private interface into liken. The raw claim, the
ResourceSlices it writes, and the CDI files it leaves for the
container runtime are the public contracts that any DRA driver on any
Kubernetes cluster gets. A cluster that never deploys it behaves
exactly as it does now, and deleting the workload plus its
ResourceSlice is the whole of the retraction.

## What it publishes

One device for each **paired** controller, not for each connected one.
A paired controller that is switched off still publishes, so a person
can create a pod for it and the pod starts when somebody turns the
controller on.

The device name is the peer MAC address in lowercase with dashes,
because a DRA device name must be a DNS label. The address publishes
as an attribute in the form BlueZ prints and the label on the
controller carries.

| Attribute | Type | What it is |
|---|---|---|
| `address` | string | the peer MAC, uppercase with colons: `A0:AB:51:33:B7:12` |
| `connected` | bool | whether bluetoothd holds a connection to it now |
| `name` | string | the controller's alias in BlueZ, truncated to 64 characters |

Two taints go on a device that cannot serve a claim right now, and
they answer two different questions.

| Taint | Effect | When |
|---|---|---|
| `bluetooth.liken.sh/disconnected` | `NoExecute` | bluetoothd reports the controller disconnected, or it registers no evdev node, or the adapter itself has departed |
| `bluetooth.liken.sh/no-input-node` | `NoSchedule` | the controller registers no evdev node, or the adapter itself has departed |

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

The MAC is the only identity on the machine that survives a reboot.
The HID instance suffix in a sysfs directory name counts up from zero
on every boot, and the `hci0:N` connection handle changes on every
reconnect, so a claim written against either one allocates different
hardware after the next boot.

## Deploying it

    kubectl apply -k deploy/

Or reference `deploy/` from your own GitOps and patch it there. The
base assumes the namespace `liken-system` exists.

Nothing states which machine has the radio. The operator's pod claims
the adapter, only a machine with an adapter publishes one, and the
scheduler puts the pod where the hardware is. To serve more than one
adapter, raise `replicas` on the StatefulSet to the number of
adapters: each replica's claim allocates a distinct one, each replica
gets its own bonds volume from the `volumeClaimTemplates`, and a
replica past the number of adapters parks Pending and costs nothing.

Two DeviceClasses come with the base. `bluetooth-adapter` is the raw
device the operator claims from liken, selected by its driver:

    device.driver == "liken.sh" &&
    device.attributes["liken.sh"].driver == "btusb"

`bluetooth-controller` is what a consumer claims:

    device.driver == "bluetooth.liken.sh"

## Pairing

There is no pairing API in this version. A person pairs each
controller once, by hand, in the operator's pod.

A DualSense needs one setting relaxed for its first pairing. The
controller opens its HID channel before the bond registers, and
BlueZ's `ClassicBondedOnly` rejects that connection, so the pairing
fails with the setting on. Leaving it off permanently re-opens
CVE-2023-45866, where anybody in radio range can open an HID channel
with no bond at all and type into the machine, so it goes back on
after the pairing. The bonds persist on their volume, so the flip
costs one restart and nothing else.

    kubectl set env statefulset/bluetooth-operator -n liken-system \
      BLUETOOTH_CLASSIC_BONDED_ONLY=false

Wait for the pod to restart, then hold **Create** and **PS** together
until the light bar flashes quickly, and pair:

    kubectl exec -it -n liken-system bluetooth-operator-0 -- bluetoothctl

    scan on
    # wait for "Wireless Controller" and note its address
    pair A0:AB:51:33:B7:12
    trust A0:AB:51:33:B7:12
    connect A0:AB:51:33:B7:12
    scan off

`trust` matters. Without it the controller has to be paired again on
every reconnection.

Then put the setting back:

    kubectl set env statefulset/bluetooth-operator -n liken-system \
      BLUETOOTH_CLASSIC_BONDED_ONLY-

After that, pressing **PS** alone reconnects. The link keys live on
the `bonds` volume, so they survive a restart of the operator's pod
and a reboot of the machine. `AutoEnable=true` in the image's
`main.conf` powers the adapter on when bluetoothd starts, so nothing
in the cluster has to press a button after a reboot.

## Claiming a controller

Name one controller by its address:

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
              deviceClassName: bluetooth-controller
              selectors:
                - cel:
                    expression: |
                      device.attributes["bluetooth.liken.sh"].address == "A0:AB:51:33:B7:12"
              tolerations:
                # A controller that drops for a moment is not a loss.
                # This number belongs to the workload, not to the
                # operator: it says how long this pod may hold a
                # controller that is off the air before the pod ends.
                - key: bluetooth.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

**Tolerate `/disconnected` only.** Do not tolerate
`bluetooth.liken.sh/no-input-node`. That taint is `NoSchedule`, and an
untolerated `NoSchedule` taint is what makes a claim on a switched-off
controller park as Unschedulable. With it tolerated, the scheduler
allocates a controller that registers no evdev node,
`NodePrepareResources` fails because there is no node to inject, the
pod sits in `ContainerCreating` until the `NoExecute` toleration runs
out, the eviction controller ends it, and the scheduler places it
again. That loop runs for as long as the controller stays off.
Tolerating one taint and not the other is the difference between a pod
that waits quietly and a pod that churns.

Then the pod names the claim, and the container that reads the
controller names the pod's entry:

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

Leave out the `selectors` block to claim any paired controller.

## What a consumer receives

Device nodes, and nothing else. The container gets
`/dev/input/event*` for the one controller the claim allocated: every
evdev node whose HID parent carries that address, which on a DualSense
is the gamepad and its motion sensors.

No privilege, no host mount, and no environment variable. The
container's own user must be able to open the node.

joydev stays out. The kernel's own documentation calls the `/dev/input/jsN`
interface legacy and names evdev as its replacement, liken's kernel
build may not enable `CONFIG_INPUT_JOYDEV` at all, and joydev
publishes a DualSense's motion sensors as a second `jsN` device, which
is wrong. A consumer that reads evdev never meets that bug.

liken's own claim on the adapter delivers the adapter's usbfs node and
nothing under it. liken stops its delivery walk at a Bluetooth
subtree, so the two drivers never deliver the same `/dev` path.

## The privilege it takes

`hostNetwork` and `NET_ADMIN`, and nothing else. Every other
capability drops.

* **`hostNetwork` is not optional.** `AF_BLUETOOTH` sockets exist only
  in the host's network namespace. A socket call in a pod's own
  network namespace fails with `EAFNOSUPPORT`. No device node and no
  mount changes this, because the Bluetooth stack's whole control
  surface is a socket family.
* **`NET_ADMIN` is what the kernel checks.** The Bluetooth management
  channel's privileged commands test for `CAP_NET_ADMIN`. `NET_RAW`
  looked like a companion requirement and proved unnecessary, because
  bluetoothd uses the management channel and seqpacket L2CAP sockets,
  not raw HCI, so it stays off.
* **`hostUsers: true`.** The kernel delivers uevents to the initial
  user namespace only. A pod in its own user namespace receives an
  empty stream with no error to read, and no controller would ever
  appear. This is the default, and the pod spec states it because the
  failure is silent.

Beside those, the pod takes the two hostPath mounts every DRA driver
takes: the kubelet's plugin registry directory, so the kubelet finds
the driver, and `/var/run/cdi`, so prepared claims land where the
container runtime reads them. Its own plugin socket directory,
`/var/lib/kubelet/plugins/bluetooth.liken.sh`, is the third.

The D-Bus bus that bluetoothd and the operator share runs at
`/var/run/bluetooth.liken.sh/dbus/system_bus_socket`, and both find it
through `DBUS_SYSTEM_BUS_ADDRESS`. Nothing mounts that directory
today. The path is written down now because a later capability that
has to reach this bluetoothd over its bus, such as a PipeWire handling
Bluetooth audio from another pod, needs a path that was stable before
it arrived. Share the **directory** if you ever share it, never the
socket file: dbus-daemon unlinks and recreates the socket at every
start, so a mount of the file alone pins an inode the daemon already
deleted.

## Disconnects and restarts

**A controller that disconnects is tainted, never deleted.** The
device stays in the slice with both taints on it, and the
taint-eviction controller ends the pod that holds the claim once the
claim's `tolerationSeconds` runs out. A return clears both taints and
the scheduler places the consumer again. Deleting the device instead
would strand the next consumer: the allocation still names the device,
`NodePrepareResources` retries against a device that is in no slice,
and nothing bounds that retry. A device leaves the slice only when it
is unpaired.

**An empty answer from bluetoothd is not an empty paired set.**
bluetoothd publishes no device objects in the moments after it starts,
and it removes every device object when the adapter goes away. Neither
case retracts a controller that a claim holds. The operator tells the
two apart by whether an adapter is in the answer, and handles them
differently.

* **No adapter, and no successful read yet.** This is the startup
  window. The operator writes nothing, because it has nothing true to
  say about the node yet.
* **No adapter, after a successful read.** The adapter departed, by an
  unplug or a USB reset. Every controller it held is now unreachable,
  so the operator republishes the last known set with both taints on
  every device and `connected: false`. The devices stay, so no
  allocation is stranded; the `NoExecute` taint ends the sessions that
  are running; and the `NoSchedule` taint parks the next claim. A
  slice frozen on its last good state would instead offer a connected
  controller that is not there.
* **An adapter, and no paired devices.** Every controller was
  unpaired, which is the one sanctioned removal, and the slice is
  deleted.

**A running pod's device set never changes.** CRI carries CDI devices
only at container creation, CDI has no re-apply operation, and NRI's
post-create updates reach cgroup settings and not device nodes. The
pod is one session. Hardware that arrives after the pod started is not
visible to that pod, and the taint is what ends the session so the
scheduler can start the next one.

**The operator's pod can restart under a live claim.** The prepared
CDI files survive on the host, so a consumer that is already running
keeps its device. The slice survives too: the operator deletes nothing
on the way out, because a pod restarts for ordinary reasons such as a
new image or a node drain, and a slice that left with each restart
would strand every consumer. The new pod re-registers with the
kubelet, re-acquires the adapter claim, rewrites its slice, and
re-prepares whatever the kubelet asks it to. The pairing survives on
the bonds volume.

**Uninstalling leaves the slice behind, and one command removes it.**
Deleting the workload stops the operator, and the ResourceSlice stays
in the API until either the Node is deleted, because the Node owns it,
or a person removes it:

    kubectl delete resourceslice <node>-bluetooth.liken.sh

Leave it in place for a redeploy. The next operator on that node
rewrites the same slice by name, so nothing has to be cleaned up
first.

**bluetoothd and the operator live and die together.** bluetoothd owns
the HID sessions, and killing it disconnects every controller at once,
so an operator that outlived it would publish devices that no pod can
use. The operator watches BlueZ's bus name and ends with a nonzero
exit when it goes away, and it does the same when the D-Bus connection
itself dies. The container ends with the operator, and the kubelet
restarts both.

## Not here yet

* **A pairing API.** By-hand pairing ships first, because it works
  today and needs no new surface. The leaning for a later iteration is
  a pairing-request CRD: a person creates a pairing-request resource
  naming the adapter and a duration, the operator opens a pairing
  window for that long, and the resource's status reports which
  controller paired or why none did. That shape keeps pairing a
  deliberate, audited, time-bounded act, which matters on a radio that
  reaches past the house walls, and it gives the
  `BLUETOOTH_CLASSIC_BONDED_ONLY` flip a home that is not a restart of
  the whole workload.
* **The drill.** Milestone 58 states what a drill against a real
  adapter and two real controllers must show. None of it has run.
* **Metrics.** The operator prints what it does to stderr and reports
  device state through the taint. It exposes no metrics endpoint.

## Building it

    go build ./...
    go test ./...
    docker build -t bluetooth-operator .

The Kubernetes libraries and the Go version are pinned to what liken
builds against, because the two drivers serve the same kubelet on the
same node.

## License

MIT. See [LICENSE](LICENSE).
