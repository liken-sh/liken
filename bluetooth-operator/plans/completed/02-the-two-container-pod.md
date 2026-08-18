# The two-container pod

Plan 02. Built, and drilled on liken-1 on 2026-08-17. The pod is two
images instead of one: the operator's Go binary alone, and bluetoothd
with its bus. Both are `FROM scratch`. This document records the
decisions and what the drill measured.

## The problem

The pod was one container. It ran a Debian image with dbus-daemon,
bluetoothd, and the operator behind a shell entrypoint, and every
capability the Bluetooth stack needs applied to all three.

Two costs came with that shape.

The privilege was wrong. The operator reads bluetoothd over D-Bus,
walks sysfs, writes CDI files, and serves a socket to the kubelet. It
needs no capability at all. In one container it ran with `NET_ADMIN`,
`NET_BIND_SERVICE`, `SETUID`, and `SETGID`, because the daemon beside
it needs them.

The image was 149 MB, and almost none of that was the two programs
that do the work. A Debian bluetoothd opens fifteen shared libraries,
and each one is a file whose version has to agree with the binary that
opens it.

## The two images

The pod is two images now, both `FROM scratch`.

| Image | What is in it | Size |
|---|---|---|
| `ghcr.io/liken-sh/bluetooth-operator` | one static Go binary | 12.4 MB |
| `ghcr.io/liken-sh/bluetoothd` | bluetoothd, bluetoothctl, dbus-daemon, the Go entrypoint, and their configuration | 7.85 MB |

149 MB became 20.3 MB. On the node the compressed pulls were
4,933,225 bytes for the operator and 3,553,369 bytes for bluetoothd.

The `bluetoothd` image builds BlueZ 5.82 and dbus 1.16.2 from source
and links them statically against musl. The result opens no shared
object, so there is no version to disagree with anything.

## bluetoothd is a sidecar, and the reason is the shutdown order

bluetoothd is an init container with `restartPolicy: Always`. The
kubelet starts a sidecar before the app containers and stops it after
them.

The startup half is convenient. The bus has to exist before the
operator connects to it, and the sidecar order states that rather than
racing it.

The shutdown half is the reason. The operator watches BlueZ's bus name
and exits nonzero when that name goes away, because bluetoothd owns
the HID sessions and an operator that outlived it would advertise
controllers it can no longer deliver. In the other order, an ordinary
`kubectl delete pod` would stop bluetoothd first, and the operator
would report that bluetoothd left the bus and exit 1 on every routine
delete. As a sidecar, bluetoothd stops second, the operator ends
first, and the exit is 0.

## The capability split

The `bluetoothd` container takes `NET_ADMIN`, `NET_BIND_SERVICE`,
`SETUID`, and `SETGID`, with everything else dropped. The `operator`
container drops `ALL` and adds nothing. `hostNetwork` and
`hostUsers: true` are pod-level settings, so both containers take
them. The install guide's "The privilege it takes" section gives the
kernel or daemon check behind each capability.

The operator's uevent socket was the one entry in doubt, because
binding a netlink multicast group is a privileged operation on most
netlink families. It is not on this one. A probe that bound
`NETLINK_KOBJECT_UEVENT` group 1 under `--cap-drop ALL` returned
success. That matches the kernel, which creates the uevent socket with
the `NL_CFG_F_NONROOT_RECV` flag, so binding group 1 takes the initial
user namespace and no capability.

## The bus is a directory, never a socket file

The D-Bus socket is at
`/var/run/bluetooth.liken.sh/dbus/system_bus_socket`, on an `emptyDir`
that both containers mount at `/var/run/bluetooth.liken.sh/dbus`. Both
containers state the same address in `DBUS_SYSTEM_BUS_ADDRESS`.

The mount is the directory and never the socket file. dbus-daemon
unlinks and recreates the socket at every start, so a mount of the
file alone would pin the inode the daemon deleted, and the container
that mounted it would hold a socket nobody listens on.

The bus serves this pod alone, so the volume is the pod's own and it
goes when the pod goes. Plan 01 keeps that path, because a PipeWire
that handles Bluetooth audio would run in this pod and mount this
volume.

## The adapter claim goes on the bluetoothd container

The `resources.claims` entry for the adapter is on the `bluetoothd`
container, because that container is the Bluetooth stack. The operator
never opens the radio.

Whether Kubernetes accepts `resources.claims` on a restartable init
container could not be tested away from hardware. The drill answered
it. A server-side dry run returned `configured` with no validation
error. The claim allocated device `usb-1-8-1-0` and reserved it for
the pod. bluetoothd powered the radio and logged
`hci0 new_settings: powered bondable ssp br/edr le secure-conn`, from
BlueZ 5.82, on controller `04:4A:69:66:92:27`.

## The entrypoint is a Go binary in this module

The shell entrypoint became `bluetoothd/main.go`, because the image
has no shell. It does four things in order:

1. It writes `/etc/bluetooth/input.conf` from the environment.
2. It starts dbus-daemon with `--system --nopidfile --address=`.
3. It waits up to 10 seconds for the socket to appear, polling every
   100 ms.
4. It calls `syscall.Exec` on bluetoothd with `-n`.

The wait is the real check on the bus. dbus-daemon forks to the
background, and the parent exits 0 whether the bus came up or not, so
a failed start shows up only as a socket that never appears.

The `exec` matters for shutdown. bluetoothd becomes the container's
process 1, so the kubelet's TERM reaches bluetoothd directly instead
of a supervisor that would have to forward it.

## BLUETOOTH_CLASSIC_BONDED_ONLY takes true or false and nothing else

The entrypoint accepts `true` or `false`, and refuses to start on any
other value. The default is `true`.

BlueZ reads this setting with glib, and glib reads an unrecognized
value as `false`. So a misspelled `yes` would turn `ClassicBondedOnly`
off and report nothing. That setting off is CVE-2023-45866, where
anybody in radio range can open an HID channel with no bond and type
into the machine. The container stops on a bad value, so the hole
cannot open without somebody seeing a failed pod.

## What the drill measured

The drill ran on liken-1 on 2026-08-17, with one adapter and one
DualSense controller.

- The pod reached 2/2 in about six seconds and held it with zero
  restarts.
- The bonds PVC was reused and not recreated: the same name
  `bonds-bluetooth-operator-0`, the same `creationTimestamp`, and the
  same volume.
- The DualSense republished as connected, with no taints on it.
- A consumer pod that held a controller claim was never evicted and
  kept working across the pod replacement. A prepared claim is CDI
  files on disk, and resolving them needs no running operator.

The one check the drill could not run was a directory listing of
`/var/lib/bluetooth`, because the image has no `ls`.
