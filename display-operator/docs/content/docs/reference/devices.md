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
            expression: device.driver == "display.liken.sh"

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

The other two classes from the install guide, `display-gpu` and
`display-render`, are not for consumers. They select the raw card
node and render node that `liken`'s own driver publishes, and the
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

`mode` is the only parameter this driver reads, and a key it does
not read fails the prepare, so a typo stops the pod instead of
running a mode nobody asked for. The value is a bare resolution
name with no refresh rate, spelled exactly as the kernel spells
it.

The name is validated against the connector's own kernel mode list
and never against this attribute, so a mode the attribute's
64-character cut dropped is still a mode a claim can ask for. A
name the connector does not offer fails the prepare, and the
failure names the whole list.

A `DeviceClass` can carry the same block as cluster policy. The
scheduler resolves the class's config and the claim's into one list
on the allocation and marks each entry's source, and the claim's
own choice wins over the class's, whichever order the two are
listed in.

## The current mode

`currentMode` is the mode the output runs right now, read from the
card itself with the DRM `GETCRTC` ioctl on every reconcile pass.
It follows a claim's mode, and it is what shows the mode a released
claim left behind, because releasing a claim restarts nothing.

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

The delivery is a mount and three environment variables, which the
runtime applies to the container. There is no device node, because a
Wayland client draws through the compositor, which holds the card.

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
