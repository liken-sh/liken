---
title: Devices
weight: 10
toc: true
---

# Devices

`display-operator` publishes one
[Dynamic Resource Allocation (DRA)](https://kubernetes.io/docs/concepts/scheduling-eviction/dynamic-resource-allocation/)
device for each connector on the graphics card its pod claims
from [`liken`](https://liken.sh/docs/). The device count follows the
card's connectors, whether or not a monitor is plugged into each
one. A cabled monitor that is asleep still answers its EDID,
so its output publishes untainted and a claim on it starts a pod. An
empty connector publishes with the `disconnected` taint, so a claim
on it parks instead of failing.

The operator publishes the devices into one
[`ResourceSlice`](https://kubernetes.io/docs/reference/kubernetes-api/resource/resource-slice-v1/)
per node, named `<node>-display.liken.sh`, beside the slice `liken`
itself publishes:

    kubectl get resourceslice <node>-display.liken.sh -o yaml
    spec:
      driver: display.liken.sh
      nodeName: kitchen
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

A consumer claims through a
[`DeviceClass`](https://kubernetes.io/docs/reference/kubernetes-api/resource/device-class-v1/)
that selects this driver. You create it, because a class a
workload claims through is cluster policy, and this manual calls
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

The `appId` guard keeps the class on outputs, because the driver
also publishes each panel's
[control device](#the-control-device), which carries no `appId`.

The class is yours to rename or narrow, the way a `StorageClass`
is;
[Install the operator](/docs/guides/install/#2-the-device-classes)
explains the split between the shipped wiring classes and this
one, and its
[Generic or specific](/docs/guides/install/#generic-or-specific)
section shows a class that names one screen. This generic class
alone allocates any output that has a monitor. To name one screen
from a claim instead, add a selector on the attributes below, as
[Put a window on a screen](/docs/guides/claim/) shows.

The other three classes from the install guide, `display-gpu`,
`display-render`, and `display-i2c`, are not for consumers. They
select the raw card node, the render node, and the card's
monitor-control wires that `liken`'s own driver publishes, and the
operator's own pod claims one of each. The pod cannot start without
them.
[Devices](https://liken.sh/docs/reference/devices/) in the `liken`
manual describes those raw devices.

## The attributes

The device name is the connector in lowercase, because a DRA device
name must be a DNS label. Every attribute except `connector` and
`appId` comes from the monitor's EDID, which the operator reads from
sysfs.

| Attribute | Type | What it is |
|---|---|---|
| `connector` | string | the kernel's connector name: `HDMI-A-1` |
| `appId` | string | what the compositor routes a window by: `hdmi-a-1` |
| `manufacturer` | string | the EDID's three-letter PNP id: `GSM` is LG |
| `model` | string | the monitor name the EDID states |
| `serial` | string | the serial the EDID states |
| `widthPixels`, `heightPixels` | int | the preferred mode, which the compositor drives unless a claim states another |
| `refreshMillihertz` | int | the preferred mode's refresh rate, in millihertz: a selector that wants 60 Hz exactly asks for `60000`, and a real monitor may answer `59999` |
| `widthMillimeters`, `heightMillimeters` | int | the panel's physical size |
| `modes` | string | the resolutions the monitor accepts, described below |
| `currentMode` | string | the mode the output runs right now, described below |
| `monitor.liken.sh/id` | string | the pairing identity, described below |
| `controlsBrightness` | bool | the panel answered the brightness control over DDC/CI, described below |
| `controlsPower` | bool | the panel answered the power control over DDC/CI, described below |

A selector reads an unqualified attribute through the driver's
domain: `device.attributes["display.liken.sh"].model`.

Only `connector` and `appId` are always present. The monitor's
attributes leave when the monitor does, and a monitor that states
nothing in a field publishes no attribute for it. A selector that
reads a missing attribute fails the whole allocation, so guard every
monitor attribute first:

    has(device.attributes["display.liken.sh"].model) &&
    device.attributes["display.liken.sh"].model == "LG HDR WQHD"

## The modes

`modes` holds the resolutions the kernel says the connector can
drive, space joined, with the preferred mode first and the rest in
descending order. A name carries no refresh rate, so a resolution
the monitor accepts at both 60 Hz and 30 Hz appears once. The
preferred mode is the same one `widthPixels`, `heightPixels`, and
`refreshMillihertz` describe.

The preferred mode is the same one `widthPixels`, `heightPixels`,
and `refreshMillihertz` describe, and it is what an output runs
until a claim states another one.

The list is one string because a device attribute holds one bool,
int, string, or version, and no array. A selector asks with
`.contains()`:

    has(device.attributes["display.liken.sh"].modes) &&
    device.attributes["display.liken.sh"].modes.contains("3840x2160")

The list can be shorter than the monitor's. The API stops a string
attribute at 64 characters, so the value ends on the last whole
resolution that fits, and the descending order makes the dropped
tail the smallest ones. A monitor that accepts sixteen resolutions
publishes about six. Read the attribute as the large modes the
monitor accepts, never as the whole list.

A claim selects one of these names with an opaque parameter this
driver reads. The operator writes the name into the compositor's
config and restarts the compositor, which is why every Wayland
client on every output of that card ends when a claim states a
mode. [Ask for a mode](/docs/guides/claim/#ask-for-a-mode) has the
whole recipe and the warning.

    spec:
      devices:
        config:
          - opaque:
              driver: display.liken.sh
              parameters:
                mode: "1280x720"

This driver reads three parameters, `mode`, `brightness`, and
`power`, and a key it does not read fails the prepare, so a typo
stops the pod instead of running a mode nobody asked for. The value
of `mode` is a resolution name,
spelled exactly as the kernel spells it, with an optional refresh
after an `@`: `1280x720` takes whatever rate the compositor picks
for that name, and `3840x1600@24` asks for the 24 Hz timing. The
refresh is a whole number of hertz. `@59.94` fails the prepare,
because the compositor reads a refresh as an integer and a
fraction would fall back silently.

The mode is validated against the connector's own kernel mode list
and never against this attribute, so a mode the attribute's
64-character cut dropped is still a mode a claim can ask for. A
name the connector does not offer fails the prepare, and the
failure names the whole list. A refresh the connector does not
offer for that name also fails the prepare, and the failure names
the refreshes that exist.

A `DeviceClass` can carry the same block as cluster policy. The
scheduler resolves the class's config and the claim's into one list
on the allocation and marks each entry's source, and the claim's
own choice wins over the class's, whichever order the two are
listed in.

## The current mode

`currentMode` is the mode the output runs right now, with its
refresh: `3840x1600@24`. It is read from the card itself with the
DRM `GETCRTC` ioctl on every reconcile pass, and the name stands
alone when the card reports no refresh. It follows a claim's mode,
and it is what shows the mode a released claim left behind, because
releasing a claim restarts nothing.

The attribute is absent while the output drives nothing, which
covers a connector with no monitor and one the compositor left
disabled, and absent when the card could not answer the ioctl.
Guard it with `has()`, like every other monitor attribute.

## The pairing identity

`monitor.liken.sh/id` pairs a screen with that screen's speakers,
which the [audio operator](https://audio.liken.sh) publishes from
the same monitor's HDMI ELD. Both drivers build the value the same
way, byte for byte, because the scheduler compares them under a
`matchAttribute` constraint. The value is the lowercase PNP id, the
four-digit hexadecimal product code, then the lowercase monitor name
with each run of spaces turned to one dash. An LG ultrawide reads
`gsm-5b09-lg-ultrawide`. A monitor with no name keeps the two-part
form, `boe-095f`. Two monitors of one model share one value, so
either pairing satisfies a constraint.

The attribute has its own domain because an unqualified name
belongs to the driver that published it. A bare `model` here and a
bare `model` in the audio driver's slice would never match.

The identity needs the manufacturer, so a monitor whose EDID states
no readable manufacturer publishes no `monitor.liken.sh/id` at all,
even while it is connected. A value without the manufacturer would
match every other monitor that also states none. Guard a selector
on this attribute with `has()`, like every other monitor attribute.

## The panel's controls

A monitor's brightness and power live in the panel, not in the
graphics card, and DDC/CI is the channel that reaches them: a slow
serial protocol on two wires of the display cable, speaking to the
same settings as the buttons on the monitor's bezel. Support is per
panel. Of two monitors on one lab machine, one answers the protocol
and one refuses it, and a panel that answers at all may still carry
only some controls.

So the operator asks instead of assuming. At inventory it probes each
connected panel, read-only: it reads the panel's own feature list,
the MCCS capability string, and asks each declared core control for
its value. Two of the answers publish on the devices as attributes:

* `controlsBrightness`: the panel answered VCP code `0x10`.
* `controlsPower`: the panel answered VCP code `0xD6`.

Each attribute is present and true, or absent. The whole list,
brightness and power beside contrast, sharpness, color preset, input
source, and the audio controls, publishes on the panel's
[`Display`](/docs/reference/displays/) under `status.capabilities`.
The probe runs once per monitor and its answer is cached against the
monitor's EDID, so steady hardware costs no bus traffic. A panel
that refused the probe is asked again about once a minute, because
DDC/CI can arrive later than the panel: an input switch or a menu
toggle turns it on with no event the operator could see.

A claim states what it wants with two opaque parameters, beside
`mode`:

    spec:
      devices:
        config:
          - opaque:
              driver: display.liken.sh
              parameters:
                brightness: 87
                power: onWhileClaimed

`brightness` is a whole number from 0 to 100, a percentage of the
panel's own maximum, because one panel counts its scale to 100 and
another to 255. The operator sets it at prepare and reads it back,
and a readback that disagrees fails the claim, because a panel
acknowledges a write whether it applies the value or not.

`power` takes two values. `on` powers the panel on at prepare and
never touches it again. `onWhileClaimed` also powers the panel back
down when the claim ends, for a claimant that owns the screen
outright. The two exist apart because a `Deployment` that replaces
its pod ends one claim and makes another, and a power-down between
them would blink the screen on every rollout. The power-down writes
standby first and falls back to off, because some panels implement
only a subset of the power values.

For a claim, the operator writes to a panel only when the claim
states one of these parameters. A claim with no parameters changes
nothing, at prepare or after. The panel's
[`Display`](/docs/reference/displays/) is the declarative write
path beside this one: a resting declaration or an override there is
also written by the operator, and only the operator.

The scheduler reads no opaque parameter, so a selector is what keeps
a claim off a panel that cannot serve it. Select on the attribute
that matches the parameter you state:

    has(device.attributes["display.liken.sh"].controlsBrightness) &&
    device.attributes["display.liken.sh"].controlsBrightness

A claim that states a parameter anyway, on a panel with no matching
attribute, fails at prepare with the missing capability named.

## The control device

A connector whose panel answered at least one control publishes a
second device, named after the output with a `-control` suffix:
`hdmi-a-2` and `hdmi-a-2-control`. Its claim is for a pod that drives
the panel itself while it runs, with its own `ddcutil` and its own
protocol code, rather than a value stated once at prepare. For a
panel that only needs its settings held or temporarily overridden,
the [`Display`](/docs/reference/displays/) does the same job with no
wire handover: state the desire on the resource, and the operator
writes it.

The control device's attributes are `connector`, the
`monitor.liken.sh/id` pairing identity, the two control booleans, and
`control`, a marker that is always true. A class selects on the
marker:

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

Like `display-output`, this class is yours to create and to narrow;
the operator ships no consumer class. The pairing identity is on both
of the connector's devices, so one claim takes a screen and its
control channel with a `matchAttribute` constraint across its two
requests.

A prepared control claim delivers two things and performs no write:

| What | Value |
|---|---|
| device node | the connector's `/dev/i2c-N`, read-write |
| `DISPLAY_CONTROL_BUS` | that node's path |

Read the bus from the variable, never guess the number: the kernel
numbers i2c adapters in the order it registers them, and the number
holds for one boot only.

The three parameters above act on outputs, and a control request
takes none of them. A config block that names no request applies to
every request in a claim, so a claim that asks for a screen and its
control channel together must name the screen's request on the block
that states the parameters, or the prepare fails.

**Do not write to any i2c address other than `0x37`, the DDC/CI
address.** The node is the connector's whole i2c bus, and the
monitor's EDID EEPROM answers on the same bus at address `0x50`. On
some panels that EEPROM accepts writes, and a write corrupts the
monitor's identity block on every machine it ever plugs into, until
someone reprograms the chip. Tools built for DDC/CI, such as
`ddcutil`, stay on the right address.

**One writer per wire.** The operator itself writes this bus for two
of its surfaces, a claim on the same connector that states
`brightness` or `power`, at prepare and at unprepare, and the
panel's [`Display`](/docs/reference/displays/), whenever its spec
diverges from the panel. The i2c layer does not arbitrate between
two userspace writers. So on a screen whose control device a pod
holds, the holder owns the panel: no claim states `power`, no
`Display` spec declares a resting value, and no writer sets an
override there. A claim may still state `brightness`. That write
lands once at the claim's prepare, and a holder that dims the panel
restores the value it last read from the panel, so the two writers
never pull in opposite directions.

The control device carries the output device's taints, whatever they
are, so it is never claimable while the screen beside it can serve
nobody. That includes the compositor-down case: a control claim today
waits for the compositor even though the wire does not need one.

## The taint

`display.liken.sh/disconnected`, with effect `NoExecute`, is the one
taint a device has, and it means the output can serve nobody
right now. It appears in two cases:

* the connector has no monitor,
* nothing answers on the compositor's socket, which covers the
  moment before the compositor's container is up and every restart
  of that container. The kubelet restarts a dead compositor alone,
  and the taint lifts on the pass that finds the socket answering.

A consumer tolerates it with a `tolerationSeconds`, which is how
long the pod may hold a dark screen before the eviction controller
ends it. Thirty seconds covers a reseated cable and an operator
restart. The operator taints a device and never deletes it: the
allocation on a running claim names the device, and a device removed
from the slice would strand the kubelet's prepare retries.

## What a prepared claim delivers

An output's delivery is a mount and three environment variables,
which the runtime applies to the container. It has no device node,
because a Wayland client draws through the compositor, which holds
the card. A control device's delivery is the node and the variable
the [control device](#the-control-device) section lists.

| What | Value |
|---|---|
| mount | `/var/run/display.liken.sh`, read-write, the same path as on the host |
| `XDG_RUNTIME_DIR` | `/var/run/display.liken.sh` |
| `WAYLAND_DISPLAY` | `wayland-0` |
| `DISPLAY_APP_ID` | the allocated output's app-id |

A claim that allocates two outputs into one container delivers two
app-ids, and only the last `DISPLAY_APP_ID` survives. One container
drives one screen; a pod that drives two screens runs two
containers, each naming its own request.

## The slice's lifetime

The operator creates its slice on the first pass, rewrites it when
it differs from sysfs, and never deletes it. The `Node` owns the
slice, so a node that leaves the cluster takes the slice with it.
The slice outlives the operator's pod on purpose, so removing the
operator for good ends with:

    kubectl delete resourceslice <node>-display.liken.sh
