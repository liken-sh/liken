# 83. A USB device is named for its unit

Milestone 83. Proposed 2026-10-10. Rejected on 2026-10-28 without a
build, for
[milestone 89](../completed/89-an-unplugged-device-evicts-its-pod.md), which
evicts the pod whose device was unplugged so that Kubernetes allocates
its claim again. A review against Kubernetes 1.37.1 found that the
eviction also covers a move to another machine, which this design did
not, and that it needs no resolution of old port names on an upgrade
and has no limit on a rollback. The design below is kept as the record.

A claim that selects a
USB device by its `serial` attribute stays allocated to the device's
old port after someone moves the cable to a different port. The
workload then fails, and nothing in the cluster repairs it. This
milestone names a USB device by its vendor, product, and serial
instead of its port, so that an allocation follows the unit to a new
port on the same machine.

## The failure

The [device reference](../../docs/content/docs/reference/devices.md)
names each device by its bus address (`deviceName` in
[`dra.go`](../../machine-operator/dra.go)). For USB, the address is the
port path, so a UPS whose interface address is `1-4:1.0` is
`usb-1-4-1-0`. The reference says that a claim which must select one
physical unit selects on `serial`.

A typical claim for a UPS:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata:
  name: ups
spec:
  devices:
    requests:
      - name: ups
        exactly:
          deviceClassName: cyberpower-ups
          selectors:
            - cel:
                expression: |
                  device.attributes["liken.sh"].serial == "PY5LP0000000"
```

The scheduler matches the selector once, then writes the device name
into the claim's status:

```yaml
status:
  allocation:
    devices:
      results:
        - device: usb-1-4-1-0
          driver: liken.sh
          pool: node-a
          request: ups
```

Someone moves the UPS cable to another port on the same machine. The
node's `ResourceSlice` removes `usb-1-4-1-0` and publishes
`usb-1-1-1-0`, with the same `serial`. The claim's allocation does not
change. Kubernetes allocates a claim once and does not allocate it
again while a pod reserves it.

The pod runs Network UPS Tools. Its driver finds no UPS in the
container and exits:

```
Network UPS Tools 2.8.5 release - Generic HID driver 0.71
USB communication driver (libusb 1.0) 0.53
No matching HID UPS found
Driver failed to start (exit status=1)
```

The liveness probe fails, and the kubelet restarts the container in
the same pod, with the same prepared claim. The scheduler never
evaluates the pod again, so the pod stays in `CrashLoopBackOff`. While
the claim's port is empty, its CDI spec names
`/dev/liken.sh/device-absent` (`refreshCDISpec` in
[`cdi.go`](../../machine-operator/cdi.go)), so a restarted container
fails to start rather than receive whatever device took the old
node's name.

The repair today is by hand. Delete the claim, which waits for its
finalizer because the pod still reserves it. Delete the pod, and the
claim is then removed. Create the claim again, for example with a
GitOps reconcile, and the scheduler allocates the new claim to
`usb-1-1-1-0` by its serial. The same failure occurred with a Zigbee
adapter that a claim selected by serial, and the same repair fixed
it.

The selector states "this unit, in any port". The allocation records
the port, so only the allocation is out of date. When the device name
follows the unit, the allocation stays correct after the move.

## What identifies a USB unit

The host knows a USB device by its descriptors and by its port. No
descriptor identifies every unit:

- `iSerialNumber` is optional. Many HID devices, USB serial adapters
  such as the CH340, and internal Bluetooth modules report none.
- A serial that a device reports is not always unique. CP210x chips
  report `0001` unless the vendor programs them, and some vendors
  write one string into every unit.
- The BOS Container ID is a UUID that USB 3 hubs must report. Few
  peripherals report one.
- The vendor ID, product ID, and the other descriptors identify the
  model. Two units of one model report the same bytes.

A home cluster measured this on 2026-10-10. The slices of its nine
machines with USB devices published 30 USB devices from 16 physical
units:

| Unit | Units | `serial` |
|---|---|---|
| Sonoff Zigbee 3.0 dongle (CP210x) | 2 | 32 hex digits, different on each unit |
| CSCTEK USB audio adapter | 3 | `A34004801402` on all three |
| Pulse-Eight CEC adapter | 2 | `v12` on both, which looks like a firmware version |
| Realtek 2.5G network adapter | 1 | `4013000001` |
| Realtek Bluetooth radio, three models | 4 | `00e04c000001` on all four |
| Intel Bluetooth radio | 4 | none |
| Realtek card reader | 2 | `20100201396000000` on both |

The serial alone identifies only the two Zigbee dongles. But no
machine in that cluster holds two units of one model, so vendor,
product, and serial together are unique on every machine. A device
name needs to be unique only inside its machine's pool, because the
allocation also names the pool.

For two units that report the same descriptors, the port is the only
identity the host has. A person who moves one of those units changes
what a claim on it means.

## The name

A USB device takes the name
`usb-<vendor>-<product>[-<serial>]`, followed by the interface part of
its address and any suffix that the device has today. A device whose
vendor, product, and serial match another USB device on the same
machine keeps its port name. A device with no serial omits the serial
part.

The same cluster's devices, by the new rule:

| Today | By the new rule |
|---|---|
| `usb-1-4-1-0` | `usb-10c4-ea60-34db8dcb1322ec1196d823c32c86906c-1-0` |
| `usb-1-6-1-0` | `usb-0573-1573-a34004801402-1-0` |
| `usb-1-4-1-0-cec` | `usb-2548-1002-v12-1-0-cec` |
| `usb-1-4-1-2` | `usb-2548-1002-v12-1-2` |
| `usb-1-10-1-0` | `usb-8087-0026-1-0` |
| `usb-6-1` | `usb-0bda-8156-4013000001` |

The serial stays in the name when it is present, even when it is not
unique across a cluster. A serial that is real is part of the unit's
identity: when someone replaces a Zigbee dongle with another unit, the
name changes, and a claim that selected the old serial receives the
`device-absent` node and not the wrong dongle.

The port leaves the name and stays in the `address` attribute. A
claim on "the adapter in this port" selects on `address`. Its
allocation then names the unit, so a replacement unit in the same port
does not take over the claim.

A whole device that a program drives through libusb (`userspace.go`)
takes the same name without an interface part.

### Decisions for the build

- **Characters and length.** A serial can hold characters that a DNS
  label cannot, and the longest name in the table above is 50
  characters, against the label's 63. `deviceName` today replaces each
  such character with a dash and truncates at 63, which can make two
  names equal. The recommendation: use the lowercased serial when it
  is short enough and holds only letters and digits, and otherwise use
  the first 12 hex digits of its SHA-256. The uniqueness check runs on
  the final names, so a collision falls back to port names.
- **Where uniqueness is decided.** `deviceName` reads one device. The
  rule needs every USB device on the machine, so the name comes from
  a table that each pass builds from the whole discovery. Prepare,
  the CDI refresh, the serio path, and the rebind on unprepare all
  resolve names through `byName` maps today, and each map must come
  from the same table.

## Names come from discovery, not from the slice

A device can be absent from the slice while a claim holds it. Network
UPS Tools detaches `usbhid` from a UPS and claims the interface
through libusb. The interface then has no driver, so it leaves the
slice until unprepare binds the driver again
([`rebinding.go`](../../machine-operator/rebinding.go)). The same home
cluster showed this on 2026-10-10: three UPS claims were allocated to
devices that no slice listed, and all three UPS pods were running and
reported `OL`.

So the name table is built from discovery, including interfaces with
no driver. The serial is on the USB device above the interface
(`parentAttr` in [`sysfs.go`](../../hardware/sysfs.go)), so a driverless
interface can still compute its name. The rebind on unprepare reads
the allocated name from the claim's CDI spec file and resolves it to
an interface address through the same table. It must not parse a port
out of the name.

A moved UPS then recovers with no person involved. Unplugging it ends
the libusb hold. The UPS enumerates on the new port with `usbhid`
bound, and its name matches the allocation. The refresh writes its
nodes into the claim's CDI spec. The runtime reads the spec at each
container creation, so the next container start in the
`CrashLoopBackOff` receives the device, and Network UPS Tools detaches
`usbhid` again.

## Claims allocated before the upgrade

Every claim allocated before this release names a port. An OS upgrade
reboots each machine, and the kubelet prepares each claim again. If
only the new names resolved, prepare would fail for every USB claim on
the cluster at once.

So prepare, the CDI refresh, and the rebind also resolve a port name
to the device that is in that port now. A claim allocated before the
upgrade keeps its port meaning until someone creates it again. Claims
that an operator makes for each pod, such as the audio, Bluetooth, and
CEC claims, take the new names when their pods are replaced. A named
`ResourceClaim` takes the new name when a person deletes it and its
pod and the claim is created again.

A rollback to a release before this one cannot resolve the new names,
so every claim allocated after the upgrade fails to prepare on the
older release. The rollback guide must say so, and give the same
repair as above. Whether the Cluster should refuse such a rollback is
open.

A later release removes the port-name resolution. Before it does, a
person needs a way to find the claims that still name ports. That way
is not designed.

## Out of scope

A move to a different machine changes the pool, and only a new
allocation can follow the unit there, which needs a new pod. A device
taint with a `NoExecute` effect evicts the pod, as `bluetooth-operator`
does for a disconnected controller, but an eviction does not allocate
a named `ResourceClaim` again. Only a workload that uses a
`ResourceClaimTemplate` receives a new claim with its new pod. So that
case stays a manual repair, and the troubleshooting guide documents
it.

PCI devices and board devices keep their names. Their addresses are
fixed by the board, and a PCI function rarely reports a serial.

## The manual

The commit that builds this updates:

- the device names section of the
  [device reference](../../docs/content/docs/reference/devices.md);
- the `troubleshoot` skill and guide, with the repair for a move to
  another machine and for a claim allocated before the upgrade;
- the rollback guide, with the limit above;
- the comment on `deviceName`, which states that the address is the
  right identity.

## Drills

On liken-1, each drill starts with a claim held by a running pod:

1. Move a USB device that reports a unique serial to another port.
   The pod runs again with no person involved.
2. Move a USB device that reports no serial and is the only one of
   its model on the machine. The pod runs again.
3. Move a device that a program holds through libusb, such as a UPS
   under Network UPS Tools. The pod runs again, and the device leaves
   the slice again while the program holds it.
4. Plug in a second unit with the same vendor, product, and serial.
   Both units take port names, and the first unit's claim fails until
   it is created again.
5. Upgrade a machine with claims allocated to port names. Every pod
   starts after the reboot.
6. Unplug a device and do not return it. The claim's container fails
   to start on `device-absent`.
