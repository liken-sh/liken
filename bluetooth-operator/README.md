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

The pod is three containers from three images. `bondfetch` runs first
and exits: it restores the adapter's bonds from a Secret so that
bluetoothd finds them. `bluetoothd` carries the daemon, its D-Bus bus,
and every capability. `bluetooth-operator` carries one static binary
with no capabilities at all. See [The images](#the-images).

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
scheduler puts the pod where the hardware is. To serve the adapters on
several machines, raise `replicas` on the Deployment to the number of
machines: each replica's claim allocates a distinct adapter, each
replica restores that adapter's own bonds from that adapter's own
Secret, and a replica past that number parks Pending and costs
nothing.

The Deployment updates with `strategy: Recreate`, because an adapter
allocates to one claim at a time. A second pod that claimed the same
adapter would park Pending until the first released the radio, and a
rolling update would never finish. The price is that every adapter is
down for the length of one update, rather than one adapter at a time.

Two replicas on one machine is a different case, and this operator
does not serve it. Both would write the same ResourceSlice, because
the slice is named for the node and the driver, and each write would
replace the other's devices. Discovery would collide as well, because
it reads every Bluetooth device on the HID bus without asking which
adapter carries it. A machine with two adapters therefore serves one.

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
after the pairing. The bonds persist in the adapter's Secret, so the
flip costs one restart and nothing else.

    kubectl set env deployment/bluetooth-operator -n liken-system \
      -c bluetoothd BLUETOOTH_CLASSIC_BONDED_ONLY=false

The variable is read by the `bluetoothd` container, which is where
BlueZ's `input.conf` is written. Anything other than `true` or `false`
stops that container, because BlueZ reads a value it does not
recognize as `false`, and a misspelling would open the hole quietly.

Wait for the pod to restart, then hold **Create** and **PS** together
until the light bar flashes quickly, and pair:

    kubectl exec -it -n liken-system deployment/bluetooth-operator \
      -c bluetoothd -- bluetoothctl

    scan on
    # wait for "Wireless Controller" and note its address
    pair A0:AB:51:33:B7:12
    trust A0:AB:51:33:B7:12
    connect A0:AB:51:33:B7:12
    scan off

`trust` matters. Without it the controller has to be paired again on
every reconnection.

`deployment/bluetooth-operator` picks one of the Deployment's pods. On
a cluster with several adapters, name the pod that holds the adapter
you are pairing to instead.

Then put the setting back:

    kubectl set env deployment/bluetooth-operator -n liken-system \
      -c bluetoothd BLUETOOTH_CLASSIC_BONDED_ONLY-

After that, pressing **PS** alone reconnects. The link keys live in
the adapter's Secret, so they survive a restart of the operator's pod
and a reboot of the machine. `AutoEnable=true` in the bluetoothd
image's `main.conf` powers the adapter on when bluetoothd starts, so
nothing in the cluster has to press a button after a reboot.

## Where the bonds live

One Secret for each adapter, in the operator's own namespace, named
`bluetooth-bonds-<address>`. The address is the adapter's own MAC in
lowercase with dashes, because a Secret's name has to be a DNS
subdomain. Each Secret holds two entries for each device paired to
that adapter. The keys are the device's address in the same form, with
a suffix that names the file, and each value is one of BlueZ's own
files, byte for byte. Nothing here parses either file.

    $ kubectl get secret -n liken-system bluetooth-bonds-14-b4-57-91-2f-c8
    NAME                                TYPE     DATA   AGE
    bluetooth-bonds-14-b4-57-91-2f-c8   Opaque   2      3d

DATA counts files and not devices, so one paired controller reads as
2. `kubectl describe` on the same Secret names the keys.

`<device>.info` is `/var/lib/bluetooth/<adapter>/<device>/info`, which
holds the link key. `<device>.cache` is
`/var/lib/bluetooth/<adapter>/cache/<device>`, which holds the SDP
records the adapter read from that device. Both have to come back. A
BR/EDR controller restored from its link key alone connects and drops
again, because bluetoothd's input profile reads the HID report
descriptor out of the cache entry and runs no new discovery for a
device it already holds a bond with.

The adapter's cache directory also holds an entry for every other
device the radio has resolved a name for, which in a house is the
neighbours' phones. Those never travel. A cache entry reaches the
Secret only when the device has a directory of its own under the
adapter, which is what pairing creates.

The adapter's address is the identity BlueZ files a link key under, so
the Secret carries the same identity the keys do. The `bondfetch` init
container asks the kernel for the address of the adapter its pod
claimed, reads that Secret, and writes BlueZ's tree into an `emptyDir`
at `/var/lib/bluetooth` before bluetoothd starts. The operator reads
the same tree and writes changes back. Nothing in the pod is storage,
so a dongle carried to another machine takes its bonds with it: the
pod that claims it there reads the same Secret by the same name.

BlueZ loads the bonds once, at adapter registration, and watches the
tree for nothing afterwards. So `bondfetch` has to finish before
bluetoothd starts, and it does: a plain init container listed before a
sidecar runs to completion first.

The keys are in the cluster's datastore. Whether that datastore is
encrypted at rest is a property of the cluster, not of this operator,
and the operator cannot check it for you. On a cluster with no
encryption at rest the keys sit there base64-encoded, and any read of
the datastore or of its backups reads them.

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

`hostNetwork` and four capabilities, with everything else dropped. The
four are all on the `bluetoothd` container. The `operator` container
drops every capability and adds none. So does `bondfetch`, which also
runs as uid 65534 with a read-only root filesystem.

Each one is what a kernel or daemon check demands.

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
* **`NET_BIND_SERVICE` is for the low L2CAP PSMs.** bluetoothd's SDP
  server binds PSM 1 and its GATT server binds PSM 31, and binding
  any PSM below 0x1001 takes `CAP_NET_BIND_SERVICE`. Without it,
  bluetoothd starts, reports `Permission denied` on both binds, and
  registers no adapter. The first hardware drill found this, because
  a pod that drops nothing keeps the capability by default and hides
  the requirement.
* **`SETUID` and `SETGID` are for dbus-daemon.** The bus daemon drops
  to its messagebus user at start, and the drop itself takes both.
  Without them the forking parent exits 0 while the bus dies, which
  is why the entrypoint also waits for the socket.
* **The operator needs none of them.** It reads bluetoothd over
  D-Bus, walks sysfs, writes CDI files, and serves a socket to the
  kubelet. Its uevent socket is not privileged either: the kernel
  creates the uevent netlink socket with `NL_CFG_F_NONROOT_RECV`, so
  binding group 1 takes no capability, only the initial user
  namespace.
* **`bondfetch` needs none of them either.** It reads the adapter's
  address, reads one Secret, and writes files into an `emptyDir`,
  which works as uid 65534 with every capability dropped and a
  read-only root filesystem. It does need the pod's `hostNetwork`,
  for the same reason bluetoothd does: the address comes over an
  `AF_BLUETOOTH` socket.
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
through `DBUS_SYSTEM_BUS_ADDRESS`. The directory is one `emptyDir`
that both containers mount, so the bus serves this pod and goes when
the pod goes. A later capability that has to reach this bluetoothd
over its bus, such as a PipeWire handling Bluetooth audio, runs in
this pod and mounts the same volume. Share the **directory**, never
the socket file: dbus-daemon unlinks and recreates the socket at every
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
re-prepares whatever the kubelet asks it to. The pairing survives in
the adapter's Secret, which `bondfetch` restores into the new pod.

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
itself dies. The kubelet restarts the operator's container, and it
waits up to 30 seconds for the bus to come back before it gives up
and exits again.

**bluetoothd is a sidecar, so the order is stated rather than raced.**
It is an init container with `restartPolicy: Always`, which the
kubelet starts before the operator and stops after it. A pod deletion
therefore ends the operator first and its exit is clean. Without that
order, an ordinary deletion would stop bluetoothd first and the
operator would report that bluetoothd left the bus on its way out.
`bondfetch` is a plain init container listed before that sidecar, so
it runs to completion before bluetoothd starts. Listed after it, it
would run beside bluetoothd and restore the bonds too late to be
read.

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

## The images

Three images, one version. They are three parts of one pod, so the
release builds and tags all of them from the same commit, and a
deployment that mixes versions is a pairing nobody tested.

| Image | What is in it | Size |
|---|---|---|
| `ghcr.io/liken-sh/bluetooth-operator` | one static Go binary | 12 MB |
| `ghcr.io/liken-sh/bluetoothd` | bluetoothd, bluetoothctl, dbus-daemon, the entrypoint that starts them, and their configuration | 8 MB |
| `ghcr.io/liken-sh/bluetooth-bondfetch` | the program that restores one adapter's bonds from its Secret | 6 MB |

All three are `FROM scratch`. There
is no shell in any of them, no package manager, and no shared object:
`kubectl exec` runs a program in the image by name, and `bluetoothctl`
is the program pairing needs.

The `bluetoothd` image builds BlueZ and dbus from source and links
them statically against musl. A distribution's bluetoothd opens six
shared libraries on Alpine and fifteen on Debian, and each one is a
file whose version has to agree with the binary that opens it. There
is nothing to disagree here. `bluetoothd/Dockerfile` states the two
flags that make the static link real, because both are easy to lose.

## Building it

    go build ./...
    go test ./...
    docker build -t bluetooth-operator .
    docker build -f bluetoothd/Dockerfile -t bluetoothd .

Both builds read this directory, because both binaries come from this
module. The BlueZ and dbus releases are pinned by version and SHA-256
in `bluetoothd/Dockerfile`, so that build takes minutes rather than
seconds.

The Kubernetes libraries and the Go version are pinned to what liken
builds against, because the two drivers serve the same kubelet on the
same node.

## License

MIT. See [LICENSE](LICENSE).
