# A GPU with DisplayPort CEC publishes as one exclusive device

Open bug. When i915 registers a CEC adapter on a GPU,
`machine-operator` publishes the whole GPU as one exclusive device with
no `subsystem` attribute. It does not publish the shareable render
node, the `-display` companion, or the wire companions. So the claim
of `display-operator` cannot be satisfied, and the render node stops
sharing.

## How the CEC adapter appears

i915 supports CEC tunneling over DisplayPort AUX (`drm_dp_cec`). When a
connected branch device supports it, i915 registers a CEC adapter as a
child of the GPU's PCI device. A USB-C hub or a DP-to-HDMI converter
with CEC can be that branch device. The kernel then adds two kinds of
node to the GPU's subtree:

- `cec`: `/sys/devices/pci0000:00/0000:00:02.0/cec0`, which is
  `/dev/cec0`.
- `input`: `.../0000:00:02.0/rc/rc0/input6/event5`, the remote-control
  input that the CEC framework registers with the adapter.

This was observed on an 11th-generation Intel GPU (Tiger Lake, Iris Xe)
on a Framework mainboard, with a monitor on the HDMI port of a USB-C
hub. The GPU's full delivery was:

| Subsystem        | Nodes                          |
| ---------------- | ------------------------------ |
| `drm`            | `card1`, `renderD128`          |
| `graphics`       | `fb0`                          |
| `i2c-dev`        | `i2c-0` to `i2c-12`            |
| `drm_dp_aux_dev` | `drm_dp_aux0` to `drm_dp_aux3` |
| `cec`            | `cec0`                         |
| `input`          | `event5` (under `rc0`)         |

Every node had a readable `subsystem` link, so this is not the case of
a node the kernel did not categorize.

## Where the policy refuses the shape

The policy is in
[`publishing.go`](../../machine-operator/publishing.go).
`graphicsDevice` returns true only when every kind is in
`graphicsSubsystems` (`drm`, `graphics`) or in `companionSubsystems`
(`i2c-dev`, `drm_dp_aux_dev`). `cec` and `input` are in neither, so it
returns false.

`splitBySubsystem` therefore does not call `publishGraphics`. The
delivery has more than one kind, so it reaches the last branch. That
branch publishes the whole delivery as one device with `RenderNode`
set, no `Subsystem`, and no `Shareable`.

## What breaks

1. **No `-display` or `-i2c-dev` device.** A claim for `displayNode`
   (the `display-gpu` class of `display-operator`) or for
   `subsystem == "i2c-dev"` (the `display-i2c` class) has no device to
   match. The pod of the `display-operator` `DaemonSet` stays `Pending`
   with "cannot allocate all claims".
2. **The render node stops sharing.** The one device is exclusive. The
   first claimant, for example a pod of the `media-operator`
   capabilities `DaemonSet`, holds the whole GPU, so every other render
   claim on that node stays `Pending`. A pod that gets the device also
   receives `card1` and the i2c and AUX nodes, so a transcoder gets
   display authority it did not ask for.
3. **A stale allocation outlives the fix.** When the CEC adapter goes
   away, for example because the hub is unplugged, the GPU splits
   correctly again. An allocation made while the GPU was one exclusive
   device has no `shareID`, so the scheduler treats it as exclusive,
   and the shareable render node still cannot be shared. The claim of
   `display-operator` stays `Pending` until the holder's pod is deleted
   and its claim is allocated again. This part is Kubernetes DRA
   behavior with `allowMultipleAllocations`, not a `liken` bug, but the
   change of shape that `liken` publishes is what triggers it. A remedy
   can document the behavior in the manual, or detect the stale
   allocation.

## Proposed fix

Teach `publishGraphics` the CEC shape:

- Add `cec` to the kinds that a graphics device accepts.
- Publish the `cec` node as its own exclusive companion, `-cec`, with
  `subsystem: cec`, the same way as the other wire companions. A CEC
  adapter is a bus with one writer and no arbitration, which is the
  same reason that `i2c-dev` is exclusive.
- Deliver the adapter's `input` node (the `rc` event device) with the
  `-cec` companion, not with the render node or the card node, because
  it is the receive half of the same adapter. `publishAudio` delivers
  jack `input` nodes with the sound card for a similar reason.
- Accept `input` only when it is under the CEC adapter's `rc` device,
  not any `input` under a GPU. Then an unknown input device on a GPU
  still gets the default: one device, whole and exclusive.

The fix also lets `equipment-operator` claim a GPU's DisplayPort CEC
adapter, the same way it claims a USB-CEC adapter.

## Tests to add

- A sysfs fixture of a GPU subtree with `cec0` and
  `rc/rc0/inputN/eventM`. It publishes a shareable render primary, a
  `-display` companion, the wire companions, and a `-cec` companion
  that delivers `cec0` and the event node.
- The same fixture with one more `input` node that is not under an
  `rc` parent. It keeps the default of one device, whole and exclusive.
- The existing GPU fixtures publish the same devices as before.

## How to reproduce on hardware

Connect a monitor to an Intel GPU through a USB-C hub or adapter that
supports CEC over DisplayPort, and boot `liken`. `/sys/class/cec/cec0`
appears, and the node's `ResourceSlice` shows one `liken.sh` device for
the GPU with no `subsystem` and no `-display` device. When the hub was
unplugged in the observed case, the GPU split into its devices again
within a minute.
