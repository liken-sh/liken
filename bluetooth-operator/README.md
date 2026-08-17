# bluetooth-operator

A Kubernetes DRA driver that publishes each paired Bluetooth controller
as a device. A pod claims one controller by its MAC address and
receives that controller's evdev nodes, and no other input device on
the machine.

This is an instance of liken's device operator pattern. liken publishes
the hardware facts no other layer can observe, and the paired set is
not one of them: it lives in bluetoothd, so the layer that publishes
controllers runs bluetoothd. That daemon does not belong in the
read-only root every machine boots, because only some machines use
Bluetooth. So the operator is an ordinary workload. It claims the
adapter through a `liken.sh` claim, runs bluetoothd in the same pod,
and publishes what bluetoothd holds under `bluetooth.liken.sh`. The
system image carries no BlueZ and no D-Bus.

The pod is three containers. `bondfetch` runs first and exits: it
restores the adapter's bonds from a Secret so bluetoothd finds them.
`bluetoothd` carries the daemon, its D-Bus bus, and every capability.
`bluetooth-operator` is one static binary with no capabilities. See
[The images](#the-images).

The operator uses no private interface into liken. The raw claim, the
ResourceSlices it writes, and the CDI files it leaves for the runtime
are the public contracts any DRA driver gets. A cluster that never
deploys it behaves as it does now.

## What it publishes

One device for each **paired** controller, not for each connected one.
A paired controller that is switched off still publishes, so a pod can
wait for it and start when somebody turns it on.

The device name is the peer MAC in lowercase with dashes, because a DRA
device name must be a DNS label. The MAC is the only identity on the
machine that survives a reboot: the HID instance suffix in sysfs counts
up from zero each boot, and the `hci0:N` handle changes on every
reconnect, so a claim against either would allocate different hardware
after a reboot.

| Attribute | Type | What it is |
|---|---|---|
| `address` | string | the peer MAC, uppercase with colons: `A0:AB:51:33:B7:12` |
| `connected` | bool | whether bluetoothd holds a connection to it now |
| `name` | string | the controller's alias in BlueZ, truncated to 64 characters |

Two taints go on a device that cannot serve a claim, and they answer
two questions:

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

## Deploying it

    kubectl apply -k deploy/

Or reference `deploy/` from your own GitOps. The base assumes the
namespace `liken-system` exists.

The operator runs as a DaemonSet, so a pod lands on every node and
nobody states which machine has the radio. Each pod claims the adapter
on its node and restores that adapter's bonds from that adapter's
Secret. A node with no adapter publishes no matching device, so the
claim parks that pod Pending, and it costs nothing.

The DaemonSet updates with `RollingUpdate` and `maxSurge: 0`, because
an adapter allocates to one claim at a time. `maxSurge: 0` stops the
old pod on a node before it starts the new one, so the new pod does
not wait forever for the radio, and `maxUnavailable: 1` takes one node
at a time. A node with two adapters serves only one, because the slice
is named for the node and the driver, so two pods on one node would
overwrite each other's slice.

The base ships two DeviceClasses. `bluetooth-adapter` is the raw device
the operator claims from liken
(`device.attributes["liken.sh"].driver == "btusb"`);
`bluetooth-controller` is what a consumer claims
(`device.driver == "bluetooth.liken.sh"`).

## Pairing

There is no pairing API yet. A person pairs each controller once, by
hand, in the operator's pod.

A DualSense needs one setting relaxed for its first pairing. It opens
its HID channel before the bond registers, and BlueZ's
`ClassicBondedOnly` rejects that, so pairing fails with the setting on.
Leaving it off permanently re-opens CVE-2023-45866, where anyone in
range can open an HID channel with no bond and type into the machine,
so it goes back on after pairing. The bonds persist in the Secret, so
the flip costs one restart:

    kubectl set env daemonset/bluetooth-operator -n liken-system \
      -c bluetoothd BLUETOOTH_CLASSIC_BONDED_ONLY=false

The `bluetoothd` container reads the variable. Any value other than
`true` or `false` stops that container, because BlueZ reads an
unrecognized value as `false`, and a misspelling would open the hole
with no error.

Wait for the restart, hold **Create** and **PS** until the light bar
flashes, and pair:

    kubectl exec -it -n liken-system daemonset/bluetooth-operator \
      -c bluetoothd -- bluetoothctl

    scan on
    # wait for "Wireless Controller" and note its address
    pair A0:AB:51:33:B7:12
    trust A0:AB:51:33:B7:12
    connect A0:AB:51:33:B7:12
    scan off

`trust` matters: without it the controller pairs again on every
reconnection. On a cluster with several adapters, exec into the pod
that holds the adapter you are pairing to. Then put the setting back:

    kubectl set env daemonset/bluetooth-operator -n liken-system \
      -c bluetoothd BLUETOOTH_CLASSIC_BONDED_ONLY-

After that, **PS** alone reconnects. The link keys live in the Secret,
so they survive a pod restart and a reboot, and `AutoEnable=true`
powers the adapter on when bluetoothd starts.

## Where the bonds live

One Secret for each adapter, in the operator's namespace, named
`bluetooth-bonds-<address>` after the adapter's MAC. Each Secret holds
two files for each paired device, byte for byte from BlueZ, and nothing
here parses them:

* `<device>.info` holds the link key.
* `<device>.cache` holds the SDP records the adapter read from the
  device. A BR/EDR controller restored from the link key alone connects
  and drops again, because bluetoothd's input profile reads the HID
  report descriptor out of the cache entry and runs no fresh discovery
  for a device it already has a bond with. Both files have to come back.

`bondfetch` asks the kernel for the adapter's address, reads that
Secret, and writes BlueZ's tree into an `emptyDir` before bluetoothd
starts. BlueZ loads the tree once, at adapter registration, so
`bondfetch` must finish first, which the init-container order
guarantees. Nothing in the pod is storage, so a dongle carried to
another machine takes its bonds with it: the pod that claims it there
reads the same Secret.
[A Secret for each adapter](plans/03-a-secret-for-each-adapter.md) has
the details.

The keys sit in the cluster datastore. Whether it is encrypted at rest
is a property of the cluster, not this operator. Without encryption at
rest the keys are base64 in the datastore and its backups.

## Claiming a controller

Select a controller by its address, or leave out the selector to claim
any paired controller:

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
                - key: bluetooth.liken.sh/disconnected
                  operator: Exists
                  effect: NoExecute
                  tolerationSeconds: 30

**Tolerate `/disconnected` only.** The other taint,
`bluetooth.liken.sh/no-input-node`, is `NoSchedule` and must stay
untolerated: it is what parks a claim on a switched-off controller as
`Unschedulable`. Tolerate both and the scheduler allocates a controller
with no evdev node, `NodePrepareResources` fails, and the pod churns
between `ContainerCreating` and eviction for as long as the controller
stays off.

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

## What a consumer receives

Device nodes, and nothing else: `/dev/input/event*` for the one
controller the claim allocated, which on a DualSense is the gamepad and
its motion sensors. No privilege, no host mount, no environment
variable; the container's user must be able to open the node.

joydev stays out. The kernel calls the `/dev/input/jsN` interface
legacy, liken's kernel may not enable `CONFIG_INPUT_JOYDEV` at all, and
joydev publishes a DualSense's motion sensors as a wrong second `jsN`
device. liken's claim on the adapter delivers the usbfs node and stops
at the Bluetooth subtree, so the two drivers never deliver the same
`/dev` path.

## The privilege it takes

`hostNetwork` and four capabilities, all on the `bluetoothd` container;
the `operator` and `bondfetch` containers drop everything, and
`bondfetch` also runs as uid 65534 with a read-only root. Each one
answers a kernel or daemon check:

* **`hostNetwork`** — `AF_BLUETOOTH` sockets exist only in the host's
  network namespace; a socket call elsewhere fails `EAFNOSUPPORT`. The
  Bluetooth stack's whole control surface is a socket family, so no
  device node replaces this.
* **`NET_ADMIN`** — the management channel's privileged commands test
  for it. `NET_RAW` proved unnecessary and stays off.
* **`NET_BIND_SERVICE`** — the SDP and GATT servers bind PSMs 1 and 31,
  and any PSM below 0x1001 takes it. Without it bluetoothd registers no
  adapter.
* **`SETUID`, `SETGID`** — dbus-daemon drops to its messagebus user at
  start, and the drop takes both.

The operator needs none: it reads bluetoothd over D-Bus, walks sysfs,
and writes CDI files, and its uevent netlink socket binds with no
capability. `bondfetch` needs none either, only the pod's `hostNetwork`
for the address socket. The pod sets `hostUsers: true`, because the
kernel delivers uevents only to the initial user namespace and the
failure is silent.

Beside those, the pod takes the two hostPath mounts every DRA driver
takes, the kubelet plugin registry and `/var/run/cdi`, and its own
plugin socket directory. bluetoothd and the operator share one D-Bus
bus on an `emptyDir` at
`/var/run/bluetooth.liken.sh/dbus/system_bus_socket`; both mount the
**directory**, never the socket file, because dbus-daemon recreates the
socket at every start.

## Disconnects and restarts

**A disconnected controller is tainted, never deleted.** The device
stays in the slice with both taints, the `NoExecute` taint evicts the
holder after its `tolerationSeconds`, and a return clears both.
Deleting the device would strand the claim, because the kubelet retries
`NodePrepareResources` against a device in no slice with no bound. A
device leaves the slice only when it is unpaired.

**An empty answer from bluetoothd is not an empty paired set.**
bluetoothd publishes no devices for a moment after it starts, and it
drops every device when the adapter departs. The operator tells the
cases apart by whether an adapter is in the answer:

* **No adapter, no successful read yet** is startup; the operator
  writes nothing.
* **No adapter after a good read** means the adapter departed, so the
  operator republishes the last set fully tainted and `connected:
  false`. The devices stay, so no allocation is stranded.
* **An adapter with no paired devices** is the one sanctioned removal,
  and the slice is deleted.

**A running pod's device set never changes.** CRI carries CDI devices
at container creation only, so the pod is one session and the taint is
what ends it. Hardware that arrives after the pod started is invisible
to that pod.

**The operator's pod can restart under a live claim.** The prepared CDI
files survive on the host, so a running consumer keeps its device
across the restart; the new pod re-registers, re-acquires the adapter,
rewrites its slice, and `bondfetch` restores the pairing again.

**bluetoothd and the operator exit together.** bluetoothd owns the HID
sessions, so the operator ends nonzero when BlueZ leaves the bus, and
the kubelet restarts it. bluetoothd is a sidecar, an init container
with `restartPolicy: Always`, so the kubelet stops it after the
operator and an ordinary delete ends the operator cleanly. `bondfetch`
is a plain init container before it, so the bonds are in place before
bluetoothd starts.

To uninstall, delete the workload; the slice stays until the Node is
deleted or you remove it:

    kubectl delete resourceslice <node>-bluetooth.liken.sh

## Not here yet

* **A pairing API.** By-hand pairing ships first. A later iteration
  leans toward a pairing-request CRD that opens a bounded, audited
  pairing window and gives the `BLUETOOTH_CLASSIC_BONDED_ONLY` flip a
  home. See
  [who owns the pairing UX](plans/open-problems/who-owns-the-pairing-ux.md).
* **The drill.** No drill against a real adapter and two controllers
  has run yet. The plans state what one must show.
* **Metrics.** The operator prints to stderr and reports state through
  the taint. There is no metrics endpoint.

## The images

Three images, one version. They are three parts of one pod, so the
release builds and tags all three from one commit, and a deployment
that mixes versions runs a combination nobody tested.

| Image | What is in it | Size |
|---|---|---|
| `ghcr.io/liken-sh/bluetooth-operator` | one static Go binary | 12 MB |
| `ghcr.io/liken-sh/bluetoothd` | bluetoothd, bluetoothctl, dbus-daemon, the entrypoint that starts them, and their configuration | 8 MB |
| `ghcr.io/liken-sh/bluetooth-bondfetch` | the program that restores one adapter's bonds from its Secret | 6 MB |

All three are `FROM scratch`: no shell, no package manager, no shared
object. `kubectl exec` runs a program by name, and `bluetoothctl` is
the one pairing needs. The `bluetoothd` image builds BlueZ and dbus
from source and links them statically against musl, so nothing depends
on a library version matching. `bluetoothd/Dockerfile` states the two
flags that make the static link real.

## Building it

    go build ./...
    go test ./...
    docker build -t bluetooth-operator .
    docker build -f bluetoothd/Dockerfile -t bluetoothd .

Both builds read this directory, because both binaries come from this
module. BlueZ and dbus are pinned by version and SHA-256 in
`bluetoothd/Dockerfile`. The Kubernetes libraries and the Go version
are pinned to what liken builds against, because the two drivers serve
the same kubelet on the same node.

## License

MIT. See [LICENSE](LICENSE).
