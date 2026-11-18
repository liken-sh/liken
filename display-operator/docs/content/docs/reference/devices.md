---
title: Devices
weight: 10
toc: true
---

# Devices

`display-operator` publishes the connectors of a node's graphics card
as [Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
devices, so that a pod can claim a screen.
[Put a window on a screen](/docs/guides/claim/) shows how.

The operator publishes devices for every connector on the card that
its pod claims from [`liken`](https://liken.sh/docs/), whether or not
a monitor is plugged in. A monitor that is cabled but asleep still
returns its EDID, so its devices have no taint and a pod that claims
one starts. A connector that has had no monitor since the operator
started has the `display.liken.sh/disconnected` taint, so a pod that
claims it stays `Pending` until a monitor is plugged in.

The devices of a node are in one
[`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
named `<node>-display.liken.sh`, separate from the slice that `liken`
publishes for the same node:

    kubectl get resourceslice <node>-display.liken.sh -o yaml
    spec:
      driver: display.liken.sh
      nodeName: node-1
      devices:
        - name: hdmi-a-1
          attributes:
            connector: {string: "HDMI-A-1"}
            appId: {string: "hdmi-a-1"}
            manufacturer: {string: "GSM"}
            model: {string: "LG HDR WQHD"}
            serial: {string: "202NTRLCC070"}
            widthPixels: {int: 3840}
            heightPixels: {int: 1600}
            refreshMillihertz: {int: 59999}
            widthMillimeters: {int: 879}
            heightMillimeters: {int: 366}
            modes: {string: "3840x1600 3840x2160 3440x1440 1920x1080 1680x1050 1600x900"}
            monitor.liken.sh/id: {string: "gsm-7716-lg-hdr-wqhd"}
        - name: dp-1
          attributes:
            connector: {string: "DP-1"}
            appId: {string: "dp-1"}
          taints:
            - key: display.liken.sh/disconnected
              effect: NoExecute

## The device class

A workload claims an output through a
[`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
that selects this driver. You create the class, because the classes
that workloads claim through are cluster policy. This manual calls
it `display-output`:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: display-output
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "display.liken.sh" &&
              has(device.attributes["display.liken.sh"].appId)

The `appId` check limits the class to output devices, because each
connector also has devices that are not outputs. A
[control device](#the-control-device) exists when the panel supports
DDC/CI. A draw device, named like the output with a `-draw` suffix,
has the `draw` attribute, the `connector`, and the monitor's
`manufacturer`, `model`, `serial`, and `monitor.liken.sh/id`. Many
claims can allocate a draw device at the same time, and each claim
gets its own Wayland socket on that output, so other programs can draw
on a screen that an output claim drives. Neither of these devices has
`appId`.

You can rename or narrow the class, as you can a `StorageClass`.
[Install the operator](/docs/guides/install/#2-the-device-classes)
explains how this class differs from the classes the operator's own
pod claims, and its
[Generic or specific](/docs/guides/install/#generic-or-specific)
section shows a class that matches one screen. The generic class
matches every output. To pick one screen in a claim instead, add a
selector on the attributes below, as
[Put a window on a screen](/docs/guides/claim/) shows.

The install guide also defines `display-gpu`, `display-render`, and
`display-i2c`. Workloads do not claim these. They select the raw card
node, the render node, and the card's i2c buses, which `liken`'s own
driver publishes. The operator's pod claims one of each and does not
start without them. [Devices](https://liken.sh/docs/reference/devices/)
in the `liken` manual describes those devices.

## The attributes

The device name is the connector name in lowercase, because a DRA
device name must be a DNS label. Most attributes come from the
monitor's EDID, which the operator reads from sysfs. `modes` and
`currentMode` come from the kernel, and `controlsBrightness` and
`controlsPower` come from a DDC/CI probe of the panel.

| Attribute | Type | What it is |
|---|---|---|
| `connector` | string | the connector name that the kernel uses: `HDMI-A-1` |
| `appId` | string | the connector name in lowercase, `hdmi-a-1`. The operator also sets it as the `DISPLAY_APP_ID` environment variable of the claiming pod. The `display-output` class selects on it to match only outputs |
| `manufacturer` | string | the three-letter PNP ID from the EDID: `GSM` is LG |
| `model` | string | the monitor name in the EDID |
| `serial` | string | the serial number in the EDID |
| `widthPixels`, `heightPixels` | int | the preferred mode, which the compositor runs unless a claim sets another mode |
| `refreshMillihertz` | int | the preferred mode's refresh rate, in millihertz. A monitor that runs at 60 Hz can report `59999`, so a selector for exactly `60000` does not match it |
| `widthMillimeters`, `heightMillimeters` | int | the panel's physical size |
| `modes` | string | the resolutions the monitor accepts, described below |
| `currentMode` | string | the mode the output runs right now, described below |
| `physicalAddress` | string | the HDMI-CEC physical address of the port that the cable is plugged into, such as `1.2.0.0`, from the HDMI vendor block of the EDID. It is absent while the connector is disconnected, and when two connectors report the same monitor with different addresses. The [`Display`](/docs/reference/displays/#the-physical-address) keeps the last address |
| `monitor.liken.sh/id` | string | the identity that pairs this screen with its speakers, described below |
| `controlsBrightness` | bool | the panel responded to a brightness read over DDC/CI, described below |
| `controlsPower` | bool | the panel responded to a power read over DDC/CI, described below |

A selector reads an unqualified attribute through the driver's
domain: `device.attributes["display.liken.sh"].model`.

Only `connector` and `appId` are always present. A monitor whose EDID
has no value for a field has no attribute for it. While a connector is
disconnected, its device keeps the `manufacturer`, `model`, `serial`,
and `monitor.liken.sh/id` of the monitor it last had, as
[the taint](#the-taint) explains, and has none of the other monitor
attributes. A selector that reads a missing attribute makes the whole
allocation fail, so check each monitor attribute with `has()` first:

    has(device.attributes["display.liken.sh"].model) &&
    device.attributes["display.liken.sh"].model == "LG HDR WQHD"

## The modes

`modes` lists the resolutions that the kernel reports for the
connector, separated by spaces. The preferred mode is first and the
rest follow in descending order. A resolution has no refresh rate in
its name, so a resolution that the monitor accepts at both 60 Hz and
30 Hz appears once. The output runs the preferred mode until a claim
sets another.

The list is one string because a device attribute can hold a bool, an
int, a string, or a version, and cannot hold an array. A selector
checks it with `.contains()`:

    has(device.attributes["display.liken.sh"].modes) &&
    device.attributes["display.liken.sh"].modes.contains("3840x2160")

The API limits a string attribute to 64 characters, so the operator
ends the value at the last whole resolution that fits, and the
smallest resolutions can be missing. Treat the attribute as the larger
modes that the monitor accepts, not as all of them.

A claim picks a mode with an opaque parameter that this driver reads.
The operator writes the mode into the compositor's configuration and
restarts the compositor. That ends every Wayland client on every
output of the card, so a claim that sets a mode interrupts the other
screens. [Ask for a mode](/docs/guides/claim/#ask-for-a-mode) has the
whole recipe and the warning.

    spec:
      devices:
        config:
          - opaque:
              driver: display.liken.sh
              parameters:
                mode: "1280x720"

The driver reads three parameters, `mode`, `brightness`, and
`power`. A claim with any other key fails at prepare, so a typo stops
the pod instead of running with a mode nobody asked for. The value of
`mode` is a resolution name spelled as the kernel spells it, with an
optional refresh rate after an `@`. `1280x720` uses the refresh rate
that the compositor picks for that resolution, and `3840x1600@24`
asks for the 24 Hz timing. The refresh rate is a whole number of
hertz. `@59.94` fails at prepare, because the compositor reads the
refresh rate as an integer and would silently drop the fraction.

The operator checks the mode against the connector's full kernel mode
list, not against the `modes` attribute, so a claim can set a mode
that the 64-character limit cut from the attribute. A resolution that
the connector does not offer fails at prepare, and the error lists
every resolution the connector offers. A refresh rate that the
connector does not offer for that resolution also fails, and the error
lists the refresh rates it does offer. The panel's
[`Display`](/docs/reference/displays/) lists every mode with every
refresh rate under `status.modes`, which has no length limit.

A `DeviceClass` can carry the same `config` block as cluster policy.
The scheduler merges the class's config and the claim's config into
one list on the allocation and records where each entry came from. An
entry from the claim wins over an entry from the class, in whichever
order they are listed.

## The current mode

`currentMode` is the mode that the output runs now, with its refresh
rate, such as `3840x1600@24`. On each pass of the reconcile loop the
operator reads it from the graphics card with the DRM `GETCRTC` ioctl.
When the card reports no refresh rate, the value is the resolution
alone. The value changes when a claim sets a mode. After the claim is
released, the output keeps running that mode, because releasing a
claim does not restart the compositor.

The attribute is absent when the output shows nothing, because no
monitor is connected or the compositor left the output disabled. It is
also absent when the card does not respond to the ioctl, and while the
operator has no connection to the compositor, for example while the
compositor restarts. The operator opens the card only while it is
connected to a compositor, so that its read cannot take DRM master
away from the compositor.

## The pairing identity

`monitor.liken.sh/id` lets one claim take a screen and that screen's
speakers. The [audio operator](https://liken.sh/audio/) publishes the
speakers from the ELD of the same monitor's HDMI connection. Both
drivers build the value in the same way, because the scheduler
compares the two values in a `matchAttribute` constraint. The value is
the lowercase PNP ID, the four-digit hexadecimal product code, and the
lowercase monitor name, joined by dashes. Each run of spaces in the
name becomes one dash. An LG ultrawide monitor has the value
`gsm-5b09-lg-ultrawide`. A monitor with no name has only the first two
parts, such as `boe-095f`. Two monitors of the same model have the
same value, so a constraint can match either of them.

The attribute has its own domain, `monitor.liken.sh`, because an
unqualified attribute name belongs to the driver that published it.
A `model` attribute in this driver's slice and a `model` attribute in
the audio driver's slice are different attributes, and a constraint
never matches them.

A monitor whose EDID has no readable manufacturer has no
`monitor.liken.sh/id`, even when it is connected, because the value
would then be the same as that of every other monitor with no
manufacturer.

## The panel's controls

A monitor keeps its brightness and power settings in the panel, not
in the graphics card. DDC/CI reads and writes them. It is a slow
serial protocol on two wires of the display cable, and it changes the
same settings as the buttons on the monitor. Support differs between
panels. One monitor can support DDC/CI and another on the same machine
can refuse it, and a panel that supports it can support only some of
the controls.

So the operator probes each connected panel. The probe only reads. It
reads the panel's capability string, which is the MCCS list of the
controls the panel supports, and it reads the value of each core
control that the panel lists. Two results appear on the device as
attributes:

* `controlsBrightness`: the panel responded to a read of VCP code `0x10`.
* `controlsPower`: the panel responded to a read of VCP code `0xD6`.

An attribute is either present and true, or absent. The full list of
controls is on the panel's [`Display`](/docs/reference/displays/)
under `status.capabilities`. It covers brightness and power, and also
contrast, sharpness, color preset, input source, and the audio
controls. The operator caches the probe result against the monitor's
EDID, so the same monitor is probed once. A panel that refused is
probed again about once a minute, because DDC/CI can become available
later without any event that the operator can see. Switching the
monitor's input or changing a setting in its menu can turn it on.

A claim can set two more opaque parameters beside `mode`:

    spec:
      devices:
        config:
          - opaque:
              driver: display.liken.sh
              parameters:
                brightness: 87
                power: OnWhileClaimed

`brightness` is a whole number from 0 to 100. It is a percentage of
the panel's own maximum, because some panels count to 100 and others
to 255. At prepare the operator reads the panel's brightness, writes
the new value if it differs, and reads it back. If the value that it
reads back differs, the claim fails, because a panel can acknowledge
a write and not apply it.

`power` takes `On` or `OnWhileClaimed`. The lowercase forms `on` and
`onWhileClaimed` mean the same. `On` powers the panel on at prepare
and does not touch it again. `OnWhileClaimed` also powers the panel
down 30 seconds after the claim ends. Use it when the claiming
workload is the only user of the screen. A `Deployment` that replaces
its pod ends one claim and creates another. If the new claim is
prepared on the same connector within the 30 seconds, the operator
cancels the power-down, so the screen does not turn off during a
rollout. The operator records the pending power-down in the config
volume of its pod, so when the operator container restarts within the
30 seconds, the 30 seconds start again. Before the power-down, the
operator reads the power mode and writes nothing if the panel is
already off. It writes standby first and then off, because some
panels support only one of the two values.

At prepare, the operator reads the power mode before it writes
anything, and it does not write to a panel that is already on. If the
panel does not respond to the read, the operator writes nothing and
the prepare fails with the error of the read.

A claim with none of these parameters changes nothing on the panel, at
prepare or afterward. The other way to set a panel is its
[`Display`](/docs/reference/displays/): when you set a value
or an override in its spec, the operator writes it to the panel.

The scheduler does not read opaque parameters, so only a selector
keeps a claim off a panel that cannot do what the claim asks. Select
on the attribute that matches the parameter that the claim sets:

    has(device.attributes["display.liken.sh"].controlsBrightness) &&
    device.attributes["display.liken.sh"].controlsBrightness

A claim that sets a parameter on a panel without the matching
attribute fails at prepare, and the error names the missing control.

## The control device

A connector whose panel responded to at least one control has a
control device, named like the output with a `-control` suffix:
`hdmi-a-2` and `hdmi-a-2-control`. A pod claims it to send its own
DDC/CI commands to the panel while the pod runs, with `ddcutil` or its
own protocol code. A claim for the output, by contrast, sets a value
once at prepare. If a panel only needs a setting kept or overridden
for a while, use the panel's [`Display`](/docs/reference/displays/)
instead: you set the value on the resource, the operator writes it,
and no pod needs access to the bus.

The control device has the attributes `connector`,
`monitor.liken.sh/id`, `controlsBrightness`, `controlsPower`, and
`control`, which is always true. A class selects on `control`:

    apiVersion: resource.k8s.io/v1
    kind: DeviceClass
    metadata:
      name: display-control
    spec:
      selectors:
        - cel:
            expression: |
              device.driver == "display.liken.sh" &&
              has(device.attributes["display.liken.sh"].control)

Like `display-output`, you create this class and can narrow it. The
operator ships no class for workloads. The output and the control
device of a connector both have `monitor.liken.sh/id`, so one claim
can take a screen and its control device with a `matchAttribute`
constraint across its two requests.

When a control claim is prepared, the operator writes nothing to the
panel and gives the pod:

| What | Value |
|---|---|
| device node | the connector's `/dev/i2c-N`, read-write |
| `DISPLAY_CONTROL_BUS` | that node's path |

Read the bus path from the variable and do not assume the number. The
kernel numbers i2c adapters in the order it registers them, so a
number is valid only until the next boot.

A control request accepts none of the `mode`, `brightness`, and
`power` parameters. A config block that names no request applies to
every request in the claim, so when one claim requests a screen and
its control device, the config block that sets the parameters must
name the screen's request, or the prepare fails.

**Do not write to any i2c address other than `0x37`, the DDC/CI
address.** The device node is the connector's whole i2c bus, and the
monitor's EDID EEPROM is on the same bus at address `0x50`. On some
panels that EEPROM accepts writes. A write corrupts the monitor's
identity data, on every machine the monitor is plugged into
afterward, until someone reprograms the chip. Tools made for DDC/CI,
such as `ddcutil`, use the correct address.

**Only one writer may use the bus at a time.** The operator writes to
this bus for a claim on the same connector that sets `brightness` or
`power`, at prepare and at unprepare, and for the panel's
[`Display`](/docs/reference/displays/) whenever its spec differs from
the panel. The i2c layer does not arbitrate between two userspace
writers, and the operator does not stop you from setting both. So
while a pod holds the control device of a screen, do not set `power`
in another claim on that screen, and do not set a value or an
override in its `Display`. A claim can still set `brightness`, because
that write happens once, at the claim's prepare. A pod that holds the
control device and dims the panel should afterward restore the
brightness it last read from the panel.

The operator's own exchanges with one panel run one at a time. A
claim's prepare, the `Display` pass, the probe, and a restore each
wait for the bus. Each also waits 50 ms after the previous exchange
ends, because a panel handles one message at a time and needs that
gap before the next one.

The control device exists only while a monitor is connected, so it
gets the `disconnected` taint only when a different monitor replaces
the one that the connector had. It does not get the taint while the
compositor is down, because the i2c bus reaches the panel without a
compositor. A claim that holds only control devices is prepared while
the compositor restarts, and its pod keeps running through the
restart. A claim that also holds an output or a draw device waits for
the compositor, because those devices give the pod a Wayland socket.

## The taint

`display.liken.sh/disconnected`, with effect `NoExecute`, is the only
taint that this driver sets. It means that no workload can use the
output now. The operator sets it in these cases:

* The connector has had no monitor since the operator started.
* A different monitor appears on the connector. The taint stays for
  90 seconds, because a claim on that connector was allocated for the
  monitor that left, and the taint-eviction controller must read the
  taint and evict the pod.
* The compositor does not respond on its socket. This is the case
  before the compositor's container is running and during every
  compositor restart. After a restart that the operator orders, the
  compositor's container starts weston again. After a crash, the
  kubelet restarts the container. In both cases the operator removes
  the taint on the first pass that finds the compositor responding.
  Only output and draw devices get the taint in this case, because a
  control device gives the pod an i2c node, which needs no compositor.
  An output whose claim is still preparing does not get the taint
  either, because a claim that sets a mode restarts the compositor
  during its prepare, before the pod has started anything that the
  restart could end.

A connector that had a monitor and then reads disconnected does not
get the taint. The operator keeps that monitor's identity for as long
as the connector stays disconnected, so the claim on that screen still
allocates and the pod on it keeps running. The reason is that the
kernel reports a monitor that switches to another input the same way
as a pulled cable, because the monitor drops hot plug detect. An A/V
receiver does the same while it renegotiates its HDMI link after each
input change.
The cost is that a monitor that someone really unplugged keeps its
devices claimable, and the pod on it keeps drawing to a screen that
is not there. The `Connected` condition on the `Display` reports what
the kernel reads.

A workload that must survive a short disconnection sets
`tolerationSeconds` on its toleration. That is how long the pod can
stay on a screen that has the taint before the taint-eviction
controller evicts it. Thirty seconds covers a cable that is plugged in
again and a restart of the operator.

The operator taints a device and never deletes it. The allocation of
a running claim names the device, and if the operator removed the
device from the slice, the kubelet's retries of the prepare would
never succeed.

## What the container receives

For an output or a draw device, the operator gives the container a
mount and three environment variables, and the container runtime
applies them. There is no device node, because a Wayland client draws
through the compositor, and the compositor holds the card. For a
control device, the container receives the device node and the
variable that [the control device](#the-control-device) lists.

| What | Value |
|---|---|
| mount | `/var/run/display.liken.sh`, read-write, at the same path as on the host |
| `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| `WAYLAND_DISPLAY` | `wayland-<the claim's UID>`, a socket that the compositor opened for this claim |
| `DISPLAY_APP_ID` | the name of the allocated output. The compositor does not route by it. The operator sets it for images that pass it to their toolkit, and it may be removed in a later release |

The compositor identifies a claim by the socket that a window
connects on, so every window on that socket belongs to the claim, and
a container cannot name the socket of another claim.

A claim that allocates two outputs for one container sets
`WAYLAND_DISPLAY` and `DISPLAY_APP_ID` twice, and only the last
value of each takes effect, so a container can draw on one screen. A
pod that draws on two screens needs two containers, each referring to
its own request.

## The lifetime of the slice

The operator creates its slice on its first pass and rewrites the
slice whenever it differs from what the operator reads. It never
deletes the slice. The `Node` owns the slice, so when a node leaves
the cluster, Kubernetes deletes the slice with it. The slice stays
after the operator's pod is gone. When you remove the operator for
good, delete the slice yourself:

    kubectl delete resourceslice <node>-display.liken.sh
