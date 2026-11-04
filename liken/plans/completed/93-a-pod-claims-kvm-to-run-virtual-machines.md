# 93. A pod claims KVM to run virtual machines

Milestone 93. Proposed and built on 2026-11-04. None of the drills
below has run. Question 1, a pod with `hostUsers: false`, is not
built, and the guide states the limit.

A workload that boots a virtual machine, such as a CI runner that
starts Firecracker microVMs or a QEMU test fleet like the `lab`, needs
`/dev/kvm`. Today the only way to give a pod that node on a `liken`
machine is a privileged container. This milestone makes `/dev/kvm` a
device that `machine-operator` publishes in the node's
`ResourceSlice`, the same way it publishes a GPU's render node. A pod
that holds a claim on it gets the node with no privilege, and the
scheduler places the pod only on a machine that has KVM. Two companion
nodes that a virtual machine monitor opens beside it, `/dev/vhost-net`
and `/dev/vhost-vsock`, publish the same way. For `/dev/vhost-vsock`,
each pod also gets its own vsock namespace, so the guests of one pod
cannot collide with the guests of another or be reached from another
pod.

## What a machine has today

These facts were read on `liken-1` on 2026-11-04, with release
2026.11.01-001, kernel 7.3.1, and k3s v1.37.1+k3s1.

- The kernel builds `kvm`, `kvm_intel`, `kvm_amd`, `vhost_net`, and
  `vhost_vsock` as modules, and `tun` and `fuse` in. The image carries
  the whole module tree, so a machine gets KVM by naming `kvm_intel`
  or `kvm_amd` in `spec.modules`. `liken-1` names `kvm_intel`,
  `vhost_vsock`, and `tun`. `stick-1` names none of them.
- The kernel registers each of these nodes as a misc device under
  `/sys/class/misc/`, outside any bus. devtmpfs creates every node
  with mode `0600` and owner `root:root`, because no driver names a
  mode and `liken` runs no udev: `/dev/kvm` is `10, 232`,
  `/dev/vhost-vsock` is `10, 241`, and `/dev/net/tun` is `10, 200`.
  The GPU's `/dev/dri/card0` and `/dev/dri/renderD128` have the same
  mode and owner.
- `/sys/module/kvm_intel/parameters/nested` reads `Y`.
- In an unprivileged pod with containerd's default device rules,
  `mknod` and an open of `10, 200` succeed, and `mknod` of `10, 232`
  and of `10, 241` fails with `EPERM`. So every container can already
  create and open `/dev/net/tun`, and no container can reach
  `/dev/kvm` or `/dev/vhost-vsock` without a grant.
- `/proc/sys/net/vsock/` exists in a pod, with `ns_mode` and
  `child_ns_mode` both `global`. The kernel has vsock namespaces, and
  the machine does not use them.

The inventory walk does not see the misc devices. `hardware.boardOwner`
skips every node under `/devices/virtual/`, because a virtual node has
no hardware behind it. `hardware.InventoryEvent` already wakes a pass
for a uevent under `/devices/virtual/misc/`, and `hardware.MiscNode`
already reads a misc device's node, for the `/dev/uhid` and
`/dev/uinput` that a Bluetooth adapter's claim receives.

## Why the OS publishes it

[Milestone 38](38-device-attributes-and-sharing.md) set the
rule: `liken` publishes the facts about hardware that no other layer
can observe, in the slice, because no other layer can add them there
later. KVM passes that test. The node exists only when the CPU has
VT-x or AMD-V, the firmware enables it, and the vendor module loaded.
A node label could say that, but a label grants nothing: the pod would
still need privilege to receive the node. A slice device is both the
scheduling fact and the grant.

`machine-operator` is the right publisher, not a device operator.
The device operators exist because their hardware needs a userspace
server, such as a compositor or a sound server. A virtual machine
monitor opens `/dev/kvm` directly, so the kernel node is the whole
interface.

## The design

### Three devices from a table

`machine-operator` publishes a misc device only when a table names it.
This is the reverse of the default for bus devices, which publish
whole and exclusive when no rule names them. A bus device is one piece
of equipment. A misc device is a service of the whole kernel, and
several of them hand over the machine itself: `loop-control` and
`device-mapper` create block devices on the host, and `snapshot`
reads the hibernation image. So a misc device that nobody examined
must not publish.

| Device | Node | Numbers | Module to declare | Published when |
|---|---|---|---|---|
| `misc-kvm` | `/dev/kvm` | `c 10:232` | `kvm_intel` or `kvm_amd` | `/sys/class/misc/kvm` exists |
| `misc-vhost-net` | `/dev/vhost-net` | `c 10:238` | `vhost_net` | `/sys/class/misc/vhost-net` exists |
| `misc-vhost-vsock` | `/dev/vhost-vsock` | `c 10:241` | `vhost_vsock` | `/sys/class/misc/vhost-vsock` exists, and `net.vsock.child_ns_mode` in the host's network namespace reads `local` |

The numbers are fixed in the kernel's `include/linux/miscdevice.h`.
`/dev/kvm` exists only after a vendor module loads, so its presence
is the fact that the hardware works. When the firmware turns VT-x off,
`kvm_intel` fails to load, `status.modules` reports it `Failed`, and
the slice has no `misc-kvm`.

`spec.modules` stays the gate. An addition to `spec.modules` loads
live, the misc device's uevent wakes a pass, and the device reaches
the slice about a second later, with no reboot.

### Names and attributes

The name is `misc-` and the kernel's name for the misc device. No bus
is named `misc`, so the name cannot collide with a bus device's name.

Each device carries `subsystem: misc` and one boolean that names what
it is, present only when true, in the same form as `renderNode` and
`displayNode`:

| Attribute | Type | On | What it is |
|---|---|---|---|
| `kvm` | bool | `misc-kvm` | the device supplies `/dev/kvm` |
| `vhostNet` | bool | `misc-vhost-net` | the device supplies `/dev/vhost-net` |
| `vhostVsock` | bool | `misc-vhost-vsock` | the device supplies `/dev/vhost-vsock` |
| `driver` | string | `misc-kvm` | `kvm_intel` or `kvm_amd`, whichever is in `/sys/module/` |
| `nested` | bool | `misc-kvm` | the vendor module's `nested` parameter is on: `Y` for `kvm_intel`, `1` for `kvm_amd` |

A DeviceClass selects with `has()`, as the guide's GPU classes do:

```yaml
apiVersion: resource.k8s.io/v1
kind: DeviceClass
metadata:
  name: kvm
spec:
  selectors:
    - cel:
        expression: |
          device.driver == "liken.sh" &&
          has(device.attributes["liken.sh"].kvm)
```

`nested` is there for a guest that must itself run KVM, such as a
`lab` machine. Both vendor modules turn it on by default, and
`spec.moduleParameters` can turn it off. A parameter changes only when
the module loads again, which removes and adds the misc device, so the
uevents of the reload update the attribute.

The devices have no `bus`, `address`, `vendor`, or `product`. The
CPU's model is a fact about the node, not about this device, and a
claim that needs it can select the node.

### Sharing

All three devices publish with `allowMultipleAllocations: true`. Each
open of one of these nodes is an independent instance in the kernel:
a VM for `/dev/kvm`, and a vhost worker for the other two. Nothing
that one claim does through its open file reaches another claim's.

The devices have no capacity. A guest's memory is the memory of the
virtual machine monitor's process, so the pod's own memory and CPU
requests already bound how many guests a node holds. A count of
allocations would be a number with no fact under it.

### The mode inside the container

A claim delivers each node with its type and numbers stated, and with
`fileMode` `0o666`, through the same path that `cdi.go` uses for the
evdev range. The runtime creates the node with `mknod` inside the
container, with that mode, and adds the cgroup rule for its numbers.
The host's node keeps `0600`.

The mode `0666` is the one systemd's default rules give these three
nodes: `rules.d/50-udev-default.rules.in` sets them to
`{{DEV_KVM_MODE}}`, and the `dev-kvm-mode` build option is `0666` by
default. Ubuntu builds it as `0660` with the `kvm` group. The kernel
designs the KVM and vhost interfaces for processes with no privilege,
and the device cgroup still refuses the node to every container
without the claim, as the `mknod` test above showed. With `0666`, a
monitor that runs as a user other than root opens the node, which is
how QEMU usually runs.

Every other node that `liken` delivers keeps the host's mode, which
is `0600 root:root`. This plan changes only the three nodes in the
table.

### vsock namespaces

A guest's vsock address, its CID, is unique on the host. The kernel
documents two modes for each network namespace, in
`Documentation/admin-guide/sysctl/net.rst`. In `global` mode, the
namespace shares the host's CIDs. In `local` mode, the namespace has
its own CIDs, and its sockets connect only to VMs and sockets in the
same namespace. A new namespace takes its mode from its parent's
`child_ns_mode`, and the host's namespace is always `global`.

With `child_ns_mode` `global`, as every machine has it now, two pods
that each start a guest at CID 3 collide, and the second fails. Any
pod on the node can also open an `AF_VSOCK` socket to any guest's CID,
with no claim, because a socket needs no device node. So a claim on
`/dev/vhost-vsock` would grant a guest and leave the guest open to
every pod.

So the OS sets `net.vsock.child_ns_mode` to `local` in
`machine.OSSysctls`. Each pod's network namespace is then created in
`local` mode, with its own CIDs, and only the pod's own containers can
reach its guests. A pod with `hostNetwork: true` is in the host's
namespace and stays `global`, and the kernel stops a `global` socket
from reaching a `local` namespace.

Two rules of this parameter change how init applies it:

- The parameter exists only while the `vsock` module is loaded, which
  `vhost_vsock` loads. Init loads the declared modules before it
  applies the sysctls, so a machine that declares `vhost_vsock` has
  it. On every other machine, init skips an OS default whose parameter
  the kernel does not have, without an error line, because the module
  that registers it is optional. A key in `spec.sysctls` that the
  kernel does not have still reports its failure.
- The first write locks the value, and a later write of another value
  fails with `EBUSY`. So the Machine spec cannot overwrite the OS
  default after init writes it. Init skips an OS default that the
  Machine spec names, and applies the spec's value as the only write.

The publish condition reads the parameter back, so the slice offers
`misc-vhost-vsock` only on a machine whose pods get their own
namespaces. A machine whose spec sets `global` publishes no
`misc-vhost-vsock`. When `vhost_vsock` loads live, the operator's
next sysctl pass writes `local`, and the misc device's uevent wakes
the inventory pass. A namespace keeps the mode it was created with,
so a pod that existed before the write keeps `global`. Such a pod
holds no claim on `misc-vhost-vsock`, because the device is not
published until the parameter reads `local`, and a `global` socket
cannot reach a `local` namespace's guests.

`vhost-net` needs no such rule. It moves packets for a tap device
that the pod's own process created and passed to it, in the pod's
own network namespace.

### Where the code goes

- `machine-operator/publishingvirtualization.go`, beside
  `publishingserio.go`, holds the table, its publish conditions, and
  the reason for each row.
- `inventoryDevices` appends the table's devices after the bus loop,
  and the slice stays sorted by name.
- `resolveAllocated` resolves a `misc-` name from the same table, so
  the prepare call and the slice cannot disagree about a device.
- `cdi.go`'s `deviceNodes` states the type, the numbers, and the mode
  for the three nodes, in the same way as for the evdev range.
- The operator reads `/proc/sys/net/vsock/child_ns_mode` through a
  path variable beside `draSysfsRoot`, so a test can supply its own
  tree.
- The comment on `hardware.InventoryEvent` names the new readers of
  the misc class.

The unplugged-device pass of
[milestone 89](89-an-unplugged-device-evicts-its-pod.md)
must treat a table device that leaves as it treats a bus device that
leaves. `rmmod` refuses a module while a process holds its node open,
so the case is a claim that no running process holds open. The build
checks that the pass finds these devices by their published names.

## Changes during the build

- **The class word.** Each virtualization device also carries
  `class: virtualization`. The device metric, `liken_devices{class}`,
  takes its label from `class`, and a device without one counts as
  `unknown`.
- **No eviction for a virtualization device that leaves.** The
  unplugged-device pass reads the bus and address of the hardware
  under a claim, and a virtualization device has neither, so a claim
  on one never takes the `liken.sh/unplugged` taint. The refresh of
  the claim's CDI specification still names the absent node when the
  device leaves, so the next container of the claim fails to start
  and the kubelet retries it. `rmmod` refuses the module while a
  process holds the node open, so a running guest cannot lose it.
- **The rebinding of drivers skips these devices.** The unprepare
  call binds a driver again to each USB interface a claim held, and
  it logged a virtualization device as a device the machine does not
  publish. It now skips them, because they have no driver to bind.
- **One function for the defaults.** `machine.DefaultSysctls` answers
  the defaults that init and the operator apply: `OSSysctls`, and
  each entry of the new table `machine.ModuleSysctls` that has a file
  and that the spec does not name. The operator adds each absent entry
  to the parameters its check of the sysctls watches for, so the
  value applies within ten seconds of a live load of `vhost_vsock`.

## What this plan does not publish

- `/dev/net/tun`. containerd's default device rules already admit it
  to every container, so a claim would gate nothing, and the node is
  built in, so it would add the same device to every slice. A pod that
  needs a tap or tun interface creates the node with `mknod`, which
  the default capabilities allow, and adds `NET_ADMIN` to create the
  interface in its own network namespace. The guide says so.
- `/dev/fuse` and the other misc devices. FUSE in a pod also needs a
  mount, which needs more than a node, so it needs its own design.
- A runtime that runs each pod in a VM, such as Kata Containers. That
  is a runtime class in the OS, not a device for a pod.
- A suggestion of `kvm_intel` or `kvm_amd` in the hardware report.
  The report lists hardware that is present and undriven, and the CPU
  is driven. The guide names the modules.

## Choices

- **A slice device instead of a device plugin.** KubeVirt's device
  plugin publishes `devices.kubevirt.io/kvm` as a count of 1000 per
  node. A device plugin is a second daemon and a second scheduling
  path, and its count is a number with no fact under it. `liken`
  already publishes every other device through one DRA driver.
- **Privileged pods and a node label, rejected.** The label schedules
  the pod, but the pod still needs privilege to receive the node, and
  privilege grants every other node too.
- **`/dev/kvm` in every container, rejected.** containerd's base spec
  could add the node and its cgroup rule to every container. Every pod
  would then get KVM, and the scheduler would know nothing about which
  nodes have it.
- **`fileMode` in the claim instead of a host mode.** Init could set
  the host's nodes to `0666`, as udev does on a distribution. The
  claim's mode changes nothing outside the container that holds it,
  and it needs no new behavior in init. Its limit is the next
  question.
- **One device per node instead of one bundle.** A Firecracker guest
  needs `/dev/kvm` alone, and QEMU adds `/dev/vhost-net` or
  `/dev/vhost-vsock` only for some guests. A claim with several
  requests gets all of its devices on one node, so separate devices
  cost a workload nothing.

## Questions for review

1. **A pod with `hostUsers: false`.** In a user namespace, runc cannot
   `mknod`, so it bind-mounts the host's node, and the host's `0600
   root:root` applies. The claim's `fileMode` then does nothing, and
   the pod cannot open `/dev/kvm`. A host mode of `0666` for the three
   nodes would serve these pods too. Is that worth a change to init in
   this milestone, or does it wait for a workload that needs it?
2. **Non-root holders of other devices.** The reference page says the
   workload supplies "the group membership that its image gives its
   user". Every delivered node is `root:root` with mode `0600` inside
   the container, so a group gives a non-root user nothing. That is a
   separate fault, outside this plan.

## The manual

- `docs/content/docs/reference/devices.md`: "What a node publishes"
  names the virtualization devices as a fourth source, beside the PCI
  bus, the USB bus, and the board. "Device names" gives the `misc-`
  form. "Attributes" adds `kvm`, `vhostNet`, `vhostVsock`, and
  `nested`, and the `driver` value of `misc-kvm`. "Sharing" lists the
  three devices. "What a claim delivers" gets a "Virtualization
  devices" section with the mode and the vsock namespaces.
- `docs/content/docs/guides/devices.md` gets a section that runs a
  virtual machine in a pod: declare the module, write the `kvm`
  DeviceClass, claim it from a `ResourceClaimTemplate`, size the pod's
  memory for the guest, and add a tap interface with `mknod` and
  `NET_ADMIN`.
- `docs/content/docs/guides/hardware-modules.md` names `kvm_intel`,
  `kvm_amd`, `vhost_net`, and `vhost_vsock`, and says that a `Failed`
  `kvm_intel` or `kvm_amd` usually means that virtualization is off in
  the firmware setup.
- `docs/content/docs/reference/sysctls.md` names `net.vsock.child_ns_mode` among the OS
  defaults, with the two rules that init follows for it.
- The skills that the docs generate are regenerated.

## Tests

- An inventory test, table-driven over sysfs fixtures: `kvm` with
  `kvm_intel` and `nested` `Y`, with `kvm_amd` and `nested` `1`, with
  `nested` off, and absent. `vhost-vsock` with `child_ns_mode`
  `local`, `global`, and absent. `vhost-net` present and absent. A
  misc device that the table does not name, such as `loop-control`,
  publishes nothing.
- A CDI test: a claim on each device writes its node with type `c`,
  its numbers, and mode `0o666`.
- A prepare test: `resolveAllocated` resolves each `misc-` name, and
  refuses `misc-vhost-vsock` when `child_ns_mode` reads `global`.
- A sysctl test: init skips an OS default whose parameter is absent,
  with no error, and skips an OS default that the spec names.

## Drills

On `liken-1` and `stick-1`:

1. `liken-1`'s slice has `misc-kvm`, with `driver: kvm_intel` and
   `nested`, and `misc-vhost-vsock`. Add `vhost_net` to `liken-1`'s
   `spec.modules`, and `misc-vhost-net` appears without a reboot.
   `stick-1`'s slice has none of the three.
2. An unprivileged pod with `runAsUser: 1000` and a claim on the
   `kvm` class runs `qemu-system-x86_64 -accel kvm` with the `liken`
   kernel and no root filesystem. The guest kernel boots to its panic
   on the missing root. Without the claim, QEMU fails at `-accel kvm`.
3. Two such pods run on `liken-1` at the same time, and both claims
   allocate `misc-kvm`.
4. Two pods on `liken-1` each start a guest with
   `-device vhost-vsock-pci,guest-cid=3`, and both guests start. This
   checks that a pod's guests belong to its own namespace.
5. A pod with `NET_ADMIN` creates `/dev/net/tun` with `mknod`, makes a
   tap interface, and starts a guest with `-netdev tap,vhost=on`
   through a claim on `misc-vhost-net`.
6. A pod whose claim asks for the `kvm` class, with a node selector
   for `stick-1`, stays `Pending`, and the scheduler's event names the
   claim that cannot be allocated.
7. The pod of drill 2 with `hostUsers: false` records what happens,
   for question 1.
