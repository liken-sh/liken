---
title: Displays
weight: 20
toc: true
---

<!-- Generated from deploy/displays.yaml by crdref. Do not edit. -->

# `Display`

A `Display` is one monitor, as a Kubernetes resource. You use it to
read the panel's controls, to set its brightness, input, and other
controls, and to turn the panel off for a while, all without a claim. The operator creates a `Display` for each monitor it
finds. A `Display` is cluster-scoped, like a `Node`, and its name is
the monitor id that the devices publish as `monitor.liken.sh/id`. You
never create or delete one.

The operator writes all of `status`: the controls the panel reports,
the values it last read from the panel, and the values it saved
before an override. You write the settings you want the panel to keep
in `spec`. A program that changes settings for a while, such as one
that darkens idle screens, adds `spec.override` and removes it when
it is done.

A list or a watch can select the monitors of one machine with
`status.node`, which the API server accepts as a field selector:

```sh
kubectl get displays --field-selector status.node=node-1
```

```yaml
apiVersion: display.liken.sh/v1alpha1
kind: Display
metadata:
  name: boe-1080-display
spec:
  brightness: 80
status:
  node: node-1
  connector: HDMI-A-2
  capabilities:
    brightness:
      max: 100
    input:
      values: [VGA-1, DVI-1, DVI-2, DP-1, DP-2, HDMI-1, HDMI-2]
    power:
      values: [On, Off, HardOff]
  observed:
    brightness: 80
    power: On
  conditions:
    - type: Connected
      status: "True"
    - type: Responsive
      status: "True"
    - type: CompositorServing
      status: "True"
      reason: Serving
```

The [devices reference](/docs/reference/devices/) describes two other
ways to control a panel. A claim can carry parameters that the
operator applies once, when it prepares the claim. A long-running pod
can claim the control device, which gives it the panel's DDC/CI
connection directly. On a `Display`, you write
the settings the panel should have, and the operator writes them
again whenever the panel reports a different value.

One monitor, the controls it reports, and the settings that the operator keeps applied.

## spec

The settings that the panel should keep, and a temporary override that takes precedence over them. Every field is optional. When the panel's value differs from a field that `spec` sets, the operator writes the field's value to the panel. The operator does not write a control that `spec` leaves out, and it does not write a value that `status.capabilities` does not allow: a number above the control's `max`, or a value missing from its `values`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--brightness"></span>`brightness` | integer | no | The panel's brightness, in the panel's own units, up to `status.capabilities.brightness.max`. |
| <span id="spec--contrast"></span>`contrast` | integer | no | The panel's contrast, in the panel's own units. |
| <span id="spec--sharpness"></span>`sharpness` | integer | no | The panel's sharpness, in the panel's own units. |
| <span id="spec--colorpreset"></span>`colorPreset` | string | no | The panel's color preset, one of `status.capabilities.colorPreset.values`. |
| <span id="spec--input"></span>`input` | string | no | The input that the panel shows, one of `status.capabilities.input.values`. The operator polls the panel about every 10 seconds, and when it finds the panel on another input, it switches the panel back. It switches back at most three times in one `metadata.generation` and then records the input in `status.unconfirmed`. On a panel that several machines share, set this field only when the panel should always show this machine. |
| <span id="spec--audiovolume"></span>`audioVolume` | integer | no | The volume of the panel's own speakers, in the panel's own units. |
| <span id="spec--audiomute"></span>`audioMute` | boolean | no | Whether the panel's own speakers are muted. |
| <span id="spec--mode"></span>`mode` | string | no | The mode to keep the screen in, such as `1920x1080@60`. It must be one of `status.modes`. The operator does not apply this field while a claim holds the screen, so a change takes effect when the last claim ends. Applying a mode restarts the compositor once, which ends every Wayland client on this card. |
| <span id="spec--layout"></span>`layout` | string | no | The name of the `Layout` that this screen shows. When the field is absent, the screen shows every window fullscreen with the newest on top. When no `Layout` has this name, the screen shows that default arrangement and the `LayoutResolved` condition is False. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--override"></span>`override` | [object](#specoverride) | no | A temporary setting that takes precedence over the other `spec` fields. When you add this block, the operator saves the panel's current values in `status.captured` and applies the override. When you delete the block, the operator restores the value that `spec` sets, or the saved value for a control that `spec` does not set. |

### spec.override

A temporary setting that takes precedence over the other `spec` fields. When you add this block, the operator saves the panel's current values in `status.captured` and applies the override. When you delete the block, the operator restores the value that `spec` sets, or the saved value for a control that `spec` does not set.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specoverride--backlight"></span>`backlight` | string | no | Sets the brightness to 0 and keeps it there. `off` in lowercase has the same meaning. One of: `Off`, `off`. |
| <span id="specoverride--power"></span>`power` | string | no | Powers the panel down and keeps it down. `off` in lowercase has the same meaning. Some panels stop answering DDC/CI while powered off. Set this only for a panel that you know wakes again. One of: `Off`, `off`. |

## status

What the operator last read from the panel and last wrote to it. The operator writes every field of `status`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--node"></span>`node` | string | no | The machine whose graphics card the panel is connected to. |
| <span id="status--connector"></span>`connector` | string | no | The connector on that card, spelled as the kernel names it. |
| <span id="status--manufacturer"></span>`manufacturer` | string | no | The monitor's manufacturer, from its EDID. The device attribute of the same name has the same value. |
| <span id="status--model"></span>`model` | string | no | The monitor's model name, from its EDID. |
| <span id="status--serial"></span>`serial` | string | no | The monitor's serial number, from its EDID. Absent when the EDID has none. |
| <span id="status--widthmillimeters"></span>`widthMillimeters` | integer | no | The panel's physical width in millimeters, from its EDID. |
| <span id="status--heightmillimeters"></span>`heightMillimeters` | integer | no | The panel's physical height in millimeters, from its EDID. |
| <span id="status--physicaladdress"></span>`physicalAddress` | string | no | The HDMI-CEC physical address of the port that this machine's cable is plugged into, in the dotted form `1.2.0.0`. The operator reads it from the HDMI vendor block of the EDID that the connector serves, and a CEC adapter that sends CEC messages for this machine uses it as its own address. The field keeps the last valid address while the connector serves no EDID for this monitor, while the EDID has no valid address, and while two connectors on this node serve this monitor with different addresses. The `PhysicalAddressCurrent` condition reports which of these applies. The field is absent until the monitor first serves a valid address, so it stays absent for a monitor on a DisplayPort cable. The addresses `0.0.0.0` and `f.f.f.f`, and an address with a non-zero digit after a zero, are not valid. |
| <span id="status--mode"></span>`mode` | [object](#statusmode) | no | The mode of this output, as the kernel and the compositor each report it. The two can differ: the connector can run at one mode while the compositor draws at another, and clients draw at the compositor's mode. |
| <span id="status--modes"></span>`modes` | []string | no | Every mode that the card offers for this connector. The operator checks `spec.mode` against this list. The device attribute `modes` holds the same list, cut after the last mode that fits in the 64-character limit of a device attribute. Absent while the operator has no connection to the compositor. |
| <span id="status--capabilities"></span>`capabilities` | [map\[string\]object](#statuscapabilities) | no | The controls that the panel declares, from the MCCS common core. A control with a list of values accepts one of them, and a control with a maximum accepts a number up to it. |
| <span id="status--observed"></span>`observed` | [object](#statusobserved) | no | The last value that the operator read from or wrote to each control. The operator reads the panel when it probes it, before it saves values for an override, and when it changes a control. It also reads about every 10 seconds while the panel is lit and has no override, to find changes made with the panel's own buttons. It never reads a panel in standby or off, because a DDC read wakes some panels. |
| <span id="status--captured"></span>`captured` | object | no | The values that the operator saved before it applied an override. The operator writes this record before the panel goes dark, so the values to restore survive an operator restart. |
| <span id="status--unconfirmed"></span>`unconfirmed` | [\[\]object](#statusunconfirmed) | no | Each write that the device did not confirm in the current `metadata.generation`. The operator lists a write here when the panel reads back a different value than the operator wrote, when a restore runs out of attempts, when the compositor restarts for `spec.mode` and still reports another mode, and when the panel changes a value by itself after the operator wrote it back three times. The operator does not repeat a listed write until `spec` changes, even after an operator restart. It removes the entry when the device reports the value. When a restore is listed here, `status.captured` keeps the saved values. |
| <span id="status--written"></span>`written` | [\[\]object](#statuswritten) | no | How many times the operator wrote each value that `spec` sets, in the current `metadata.generation`, counting only writes that the panel confirmed. The first write sets the value, and each later write puts it back after the panel changed it. The operator writes a value back at most three times in one generation, and then records it in `status.unconfirmed`. |
| <span id="status--surfaces"></span>`surfaces` | [\[\]object](#statussurfaces) | no | Every window that the compositor has on this screen, in the order the windows arrived, including windows that no region shows. Read this field first when a program draws nothing visible. A window's `id` is valid only for the compositor that assigned it. A compositor restart ends every window, and the programs reconnect with new ids. |
| <span id="status--layout"></span>`layout` | [object](#statuslayout) | no | The arrangement that the screen is drawn with, and the window that each region shows. |
| <span id="status--arrangers"></span>`arrangers` | [\[\]object](#statusarrangers) | no | The arranger of each claim on this screen that has one. An arranger is a Wayland client of the claim that places the claim's windows inside the claim's region, through the `liken_arrangement_v1` protocol. The `Layout` still sets where the region is. A claim with no arranger is not listed, and its windows fill its region. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | `Connected` is True while a panel is connected to the connector. It turns False only after the connector has had no panel for 5 seconds, so a shorter loss of the hot plug signal, such as an A/V receiver causes when it goes to standby, changes no field of `status` and posts no `Event`. `Responsive` is True while the panel responds to DDC/CI, and False with the reason `NoDDCReply` when it does not. `LayoutResolved` is False with the reason `LayoutNotFound` while `spec.layout` names a `Layout` that does not exist, and the screen shows the default arrangement until the `Layout` exists. `CompositorServing` is False with the reason `Down` while the compositor's socket refuses connections, and with the reason `Hung` while the socket accepts connections and the compositor does not respond. Its message is the error that the connection returned. While `Connected` or `CompositorServing` is False, `status.surfaces`, `status.layout`, and `status.arrangers` are empty, because the compositor shows no window. `PhysicalAddressCurrent` is True with the reason `ReadFromEDID` while the EDID that the connector serves now gives `status.physicalAddress`. It is False with the reason `Retained` while the connector serves no EDID for this monitor or no valid address in it, and `status.physicalAddress` then holds the last valid address. The message gives the time that the connector stopped serving it. It is False with the reason `Ambiguous` while two connected connectors on this node serve this monitor with different addresses. The message names both connectors and both addresses, and `status.physicalAddress` keeps the value it had before they differed. The condition is absent until the monitor first serves a valid address. |

### status.mode

The mode of this output, as the kernel and the compositor each report it. The two can differ: the connector can run at one mode while the compositor draws at another, and clients draw at the compositor's mode.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmode--kernel"></span>`kernel` | string | no | The mode that the card reports for the connector. Absent while the connector has no mode set and while the operator has no connection to the compositor. |
| <span id="statusmode--weston"></span>`weston` | string | no | The mode that the compositor reports in its `wl_output` events, which is the mode that clients draw at. Absent while the operator has no connection to the compositor. |

### status.capabilities.*

The controls that the panel declares, from the MCCS common core. A control with a list of values accepts one of them, and a control with a maximum accepts a number up to it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscapabilities--max"></span>`max` | integer | no | The largest number the panel accepts for a continuous control. |
| <span id="statuscapabilities--values"></span>`values` | []string | no | Every value that the panel accepts for a non-continuous control. The power control lists its values by the names that `status.observed.power` uses. |

### status.observed

The last value that the operator read from or wrote to each control. The operator reads the panel when it probes it, before it saves values for an override, and when it changes a control. It also reads about every 10 seconds while the panel is lit and has no override, to find changes made with the panel's own buttons. It never reads a panel in standby or off, because a DDC read wakes some panels.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusobserved--brightness"></span>`brightness` | integer | no |  |
| <span id="statusobserved--contrast"></span>`contrast` | integer | no |  |
| <span id="statusobserved--sharpness"></span>`sharpness` | integer | no |  |
| <span id="statusobserved--colorpreset"></span>`colorPreset` | string | no |  |
| <span id="statusobserved--input"></span>`input` | string | no |  |
| <span id="statusobserved--audiovolume"></span>`audioVolume` | integer | no |  |
| <span id="statusobserved--audiomute"></span>`audioMute` | boolean | no |  |
| <span id="statusobserved--power"></span>`power` | string | no | The panel's power state: `On`, `Standby`, `Suspend`, `Off`, or `HardOff`. A state that the MCCS table has no name for is a hexadecimal number, such as `0x06`. The operator reads a power name in any letter case, so values saved in lowercase still restore the panel. |

### status.unconfirmed[]

Each write that the device did not confirm in the current `metadata.generation`. The operator lists a write here when the panel reads back a different value than the operator wrote, when a restore runs out of attempts, when the compositor restarts for `spec.mode` and still reports another mode, and when the panel changes a value by itself after the operator wrote it back three times. The operator does not repeat a listed write until `spec` changes, even after an operator restart. It removes the entry when the device reports the value. When a restore is listed here, `status.captured` keeps the saved values.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusunconfirmed--control"></span>`control` | string | yes | The control, spelled as in `status.capabilities`, or `mode` for `spec.mode`. |
| <span id="statusunconfirmed--value"></span>`value` | string | yes | The value that the operator wrote. |
| <span id="statusunconfirmed--readback"></span>`readback` | string | no | The value that the device reported after the write. Absent when the device did not respond. |
| <span id="statusunconfirmed--generation"></span>`generation` | integer | yes | The `metadata.generation` that the write was made for. |
| <span id="statusunconfirmed--message"></span>`message` | string | no | The failure, in the text of the device, tool, or compositor that reported it. |
| <span id="statusunconfirmed--time"></span>`time` | string | yes | When the operator recorded the write. |

### status.written[]

How many times the operator wrote each value that `spec` sets, in the current `metadata.generation`, counting only writes that the panel confirmed. The first write sets the value, and each later write puts it back after the panel changed it. The operator writes a value back at most three times in one generation, and then records it in `status.unconfirmed`.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuswritten--control"></span>`control` | string | yes | The control, spelled as in `status.capabilities`. |
| <span id="statuswritten--value"></span>`value` | string | yes | The value that `spec` sets. |
| <span id="statuswritten--generation"></span>`generation` | integer | yes | The `metadata.generation` that the writes were made for. |
| <span id="statuswritten--count"></span>`count` | integer | yes | The number of confirmed writes. |

### status.surfaces[]

Every window that the compositor has on this screen, in the order the windows arrived, including windows that no region shows. Read this field first when a program draws nothing visible. A window's `id` is valid only for the compositor that assigned it. A compositor restart ends every window, and the programs reconnect with new ids.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussurfaces--id"></span>`id` | string | yes | The window id that the compositor assigned. `status.layout` lists it beside the region that shows the window. |
| <span id="statussurfaces--claim"></span>`claim` | string | no | The `ResourceClaim` whose socket the window connected through, as `namespace/name`. Empty for a window on the compositor's own socket, `wayland-0`, which belongs to no claim. |
| <span id="statussurfaces--pods"></span>`pods` | []string | no | The pods that hold the claim, each as `namespace/name`. |
| <span id="statussurfaces--labels"></span>`labels` | map[string]string | no | The labels that every pod holding the claim has, with the same value on each pod. A region's `selector` matches against these labels. When one pod holds the claim, they are that pod's labels. |
| <span id="statussurfaces--size"></span>`size` | [object](#statussurfacessize) | no | The size in pixels that the program last drew. A window in a region draws at the region's size, so a different size means that the program ignored the compositor's request, and the compositor scales the window to fit. |
| <span id="statussurfaces--region"></span>`region` | string | no | The region that shows the window. Empty when no region shows the window, so the window is not on the screen. |
| <span id="statussurfaces--appid"></span>`appID` | string | no | The xdg `app_id` that the program set on the window. Empty until the program sets one. An arranger refers to the windows of its claim by `app_id`. |

#### status.surfaces[].size

The size in pixels that the program last drew. A window in a region draws at the region's size, so a different size means that the program ignored the compositor's request, and the compositor scales the window to fit.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussurfacessize--width"></span>`width` | integer | no | The width in pixels. |
| <span id="statussurfacessize--height"></span>`height` | integer | no | The height in pixels. |

### status.layout

The arrangement that the screen is drawn with, and the window that each region shows.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuslayout--name"></span>`name` | string | no | The `Layout` in use, or `default` when `spec.layout` is absent or names a `Layout` that does not exist. |
| <span id="statuslayout--regions"></span>`regions` | [\[\]object](#statuslayoutregions) | no | Each region in stacking order, with the window on top of it. |

#### status.layout.regions[]

Each region in stacking order, with the window on top of it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuslayoutregions--name"></span>`name` | string | yes | The region's name, as the `Layout` gives it. |
| <span id="statuslayoutregions--surface"></span>`surface` | string | no | The id of the window on top of the region, which is the newest window of the claim that the region shows, or `empty` when no window matched the region. |

### status.arrangers[]

The arranger of each claim on this screen that has one. An arranger is a Wayland client of the claim that places the claim's windows inside the claim's region, through the `liken_arrangement_v1` protocol. The `Layout` still sets where the region is. A claim with no arranger is not listed, and its windows fill its region.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusarrangers--claim"></span>`claim` | string | yes | The `ResourceClaim` whose socket the arranger connected through, as `namespace/name`. |
| <span id="statusarrangers--region"></span>`region` | string | no | The region that shows the claim's windows. Empty while no region shows them. |
| <span id="statusarrangers--pods"></span>`pods` | []string | no | The pods that hold the claim, each as `namespace/name`. The arranger is a process in one of them. The compositor cannot tell which container, because it reads the client's pid from the socket, and the kernel reports pid 0 for a client in another pod's pid namespace. |
| <span id="statusarrangers--pid"></span>`pid` | integer | no | The arranger's pid in the compositor's pid namespace. Absent for a client in another pod, which includes every workload. |
| <span id="statusarrangers--arrangement"></span>`arrangement` | string | no | The name of the last arrangement that the arranger committed. Absent before its first commit. The compositor replaces each non-printable character with an underscore, and reports an empty name as a hyphen. |
| <span id="statusarrangers--duplicateappids"></span>`duplicateAppIDs` | []string | no | Each `app_id` that two or more of the claim's windows have. An arrangement places every window with the `app_id` that it names, so these windows always move together. |

### status.conditions[]

`Connected` is True while a panel is connected to the connector. It turns False only after the connector has had no panel for 5 seconds, so a shorter loss of the hot plug signal, such as an A/V receiver causes when it goes to standby, changes no field of `status` and posts no `Event`. `Responsive` is True while the panel responds to DDC/CI, and False with the reason `NoDDCReply` when it does not. `LayoutResolved` is False with the reason `LayoutNotFound` while `spec.layout` names a `Layout` that does not exist, and the screen shows the default arrangement until the `Layout` exists. `CompositorServing` is False with the reason `Down` while the compositor's socket refuses connections, and with the reason `Hung` while the socket accepts connections and the compositor does not respond. Its message is the error that the connection returned. While `Connected` or `CompositorServing` is False, `status.surfaces`, `status.layout`, and `status.arrangers` are empty, because the compositor shows no window. `PhysicalAddressCurrent` is True with the reason `ReadFromEDID` while the EDID that the connector serves now gives `status.physicalAddress`. It is False with the reason `Retained` while the connector serves no EDID for this monitor or no valid address in it, and `status.physicalAddress` then holds the last valid address. The message gives the time that the connector stopped serving it. It is False with the reason `Ambiguous` while two connected connectors on this node serve this monitor with different addresses. The message names both connectors and both addresses, and `status.physicalAddress` keeps the value it had before they differed. The condition is absent until the monitor first serves a valid address.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |

## The declared settings

The fields of `spec` declare the values you want a panel to keep. On
every pass the operator compares each declared value with the value
it last read from the panel, and it writes the panel only when they
differ. It first checks the value against `status.capabilities`, and
when the panel does not support it, the pass fails and nothing is
written. An empty `spec` makes no writes, so a panel with no
declarations keeps whatever its own menu sets.

Some panels accept a write and then report a different value, such
as an input under a different code from the one the operator wrote.
The operator writes such a value once for each `metadata.generation`
and records it in `status.unconfirmed`, with the value the panel
reported. It doesn't write the value again until `spec` changes. The
record is in status, so a restarted operator doesn't write it again
either.

Other panels confirm a write and later change the value on their
own, for example by switching to the input that has a signal. After
the first write, the operator writes a declared value back at most
three more times in one generation, and counts the writes in
`status.written`. After the third, it records the value in
`status.unconfirmed` with a message that the panel keeps changing it,
and writes it again only after `spec` changes. A change that a person
makes with the panel's buttons counts the same way.

Each write to a panel adds a line to the operator's log, with the
value before and after the write.

## The override

`spec.override` sets a temporary state that takes priority over the
rest of `spec`, much as `kubectl cordon` sets `spec.unschedulable` on
a `Node` without changing the rest of it. A controller or a person
adds the `override` block, and the operator applies it. When the
block is deleted, the operator restores the panel to the value that
`spec` declares, or, when `spec` declares none, to the value it
captured before the override.

Before the operator applies `backlight: Off`, it reads the panel's
brightness and writes it to `status.captured`. It writes the panel
only after the API server has saved the capture. The capture is in
`etcd`, so the restore still works after an operator restart, a pod
move, or a reboot.

A panel that is waking up can respond late, so the restore retries
until the panel reads back the value. It makes at most eight writes
of each control, over about 90 seconds. When the restore runs out of
writes, `status.captured` stays in place and `status.unconfirmed`
names the control. An edit to `spec` starts the restore again.

An override has no timeout. When the controller that set an override
crashes, the panel stays dark until the controller returns or a
person deletes the block. `kubectl get display` shows the active
override, and the field manager of the `override` block names the
controller that has to remove it.

## The declared mode

`spec.mode` works like the other declared values, except that the
compositor applies a mode, and applying it restarts the compositor.
That ends every Wayland client on the graphics card. So the operator
applies `spec.mode` only while no claim holds the screen. A claim's
own `mode` parameter takes priority for as long as the claim exists.
When you edit `spec.mode` during a claim, the operator applies the new
mode as soon as the kubelet unprepares the claim and frees the screen.

A claim holds the screen while a pod that is not being deleted holds
the claim, even before the claim's prepare succeeds. The kubelet
retries a failed prepare, and each prepare switches the screen to the
claim's mode. If the operator applied `spec.mode` between two
retries, it would restart the compositor once for each retry.

A new operator pod starts the compositor at each monitor's
`spec.mode`, so the screen changes mode once at startup, not first to
the preferred mode and then to `spec.mode`. When the compositor runs
a different mode after its restart, the operator records the mode in
`status.unconfirmed` and doesn't restart the compositor for that mode
again until `spec` changes.

When the compositor crashes, the kubelet delays its next start with a
crash backoff. The operator waits up to six minutes for the
compositor to start, and then gives it 10 seconds to serve the mode.

## The two values of the mode

The kernel and the compositor each report a mode, and they can
disagree, so `status.mode` has both. `kernel` is the mode that the
graphics card drives on the connector. `weston` is the mode that the
compositor lays out canvases at, which the operator reads from the
compositor's `wl_output` events. Clients draw at the `weston` mode,
so when the two differ, clients on that screen draw at the wrong
size. The operator then restarts the compositor to correct it, once
no claim holds the screen.

`weston` is absent while the operator has no connection to a
compositor. `kernel` is absent while the connector drives no display,
and also while the operator has no connection to a compositor,
because the operator opens the graphics card only while it has that
connection. `kubectl get displays` shows the two values in the `MODE`
and `CANVAS` columns.

## The physical address

`status.physicalAddress` is the HDMI-CEC physical address of the port
that this machine's cable is plugged into. Its four hex digits give
the path from the TV, one digit for each HDMI port on the way. A
machine on input 2 of a receiver that is on the TV's input 1 reads
`1.2.0.0`.

An HDMI sink serves an EDID on each of its ports, and the EDID's
vendor block has that port's address. The operator reads the address
from the EDID on the connector. A CEC adapter on the machine has no
EDID of its own, so the program that drives the adapter needs this
address to send `Active Source`, which switches the receiver and the
TV to the machine's picture. `display-operator` itself does not open
a CEC adapter or send CEC messages.

A receiver in standby can stop serving its EDID, or pass through the
TV's EDID, which belongs to another `Display`. The machine's port has
not moved, so `status.physicalAddress` keeps the last valid address,
and the `PhysicalAddressCurrent` condition says where the value came
from:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `ReadFromEDID` | The connector's current EDID has this address. |
| `False` | `Retained` | The connector serves no EDID for this monitor, or the EDID has no valid address. The message gives the time that the connector stopped serving the address. |
| `False` | `Ambiguous` | Two connected connectors serve this monitor with different addresses. The message lists both connectors and both addresses. |

The condition is absent while the monitor has never served a valid
address, for example on a DisplayPort cable. The operator never
publishes `0.0.0.0`, the TV's own address, which a sink serves when
it has no address to give, or `f.f.f.f`, the invalid address. It also
refuses an address with a non-zero digit after a zero, such as
`1.0.2.0`, because a zero ends the path.

The output device has the same value in its `physicalAddress`
attribute, but only while the current EDID has it, so a connector with
no signal publishes none. To read the retained value, read the
`Display`.

When two connectors on one machine serve EDIDs with the same
identity, such as two cables to one receiver, the operator creates
one `Display` for them. It reports whichever connected connector has
the name that sorts last.

The two connectors can serve different addresses, for example when
one cable carries the picture into one input of a receiver and a
second cable, through a CEC adapter, goes into another input. Then
neither address is right, so the `Display` publishes no current
address. `PhysicalAddressCurrent` reads `Ambiguous`, neither
connector's device has a `physicalAddress` attribute, and
`status.physicalAddress` keeps the value it had before the connectors
disagreed.

## Shared screens

Brightness and power apply to the whole panel, so dimming a monitor
dims every input on it. The operator can't tell which input a panel
is showing, because that query is optional in DDC/CI, and a panel can
answer it with the port that the query arrived on. So the operator
applies an override whenever the panel responds, and the component
that writes the override decides whether a shared screen may go dark.
For example, the `Player` of `media-operator` has an idle policy, and
`offAfterSeconds: 0` in it leaves the panel of a shared screen alone.

## Observed values

`status.observed` holds the values that the operator last read from
the panel or wrote to it. The operator reads or writes the panel when
it probes the panel, when it captures a value before an override, and
when it applies a value. It also reads a panel every 10 to 20 seconds
while the panel is on, responds, and has no override. That read finds
a change that a person made in the panel's own menu, and the same pass
writes the declared value back, up to three times in one generation.

The operator doesn't read a panel on a timer while it is in standby
or off, while an override or a restore applies to it, or while it
doesn't respond. A DDC/CI read wakes some panels, so polling them
would light up screens that an override turned dark. For those
panels, `observed` keeps the values the operator last read.

## Arrangers

The `Layout` that `spec.layout` names divides the screen into regions
and gives each claim a region. Inside its region, one client of the
claim can place the claim's own windows through the
`liken_arrangement_v1` Wayland protocol. That client is the claim's
arranger. The [arrangement reference](/docs/reference/arrangement/)
describes the protocol for the author of a workload.

`status.arrangers` lists each claim on the screen that has an
arranger, with the region that shows the claim and the name of the
arrangement that the arranger committed last. Each window in
`status.surfaces` has the `appID` that an arrangement uses to name
it. `duplicateAppIDs` lists each `app_id` that more than one window of
the claim has, because one entry of an arrangement moves all of those
windows:

```yaml
status:
  surfaces:
    - id: film-cla-4
      claim: media/film
      appID: liken-film
      region: main
    - id: film-cla-5
      claim: media/film
      appID: liken-display
      region: main
  arrangers:
    - claim: media/film
      region: main
      pods: [media/player-0]
      arrangement: credits
```

The arranger is a process in one of the pods that hold the claim.
`status.arrangers` lists those pods, and gives no pid or container,
because the compositor reads a client's pid from the socket, and the
kernel reports pid 0 for a client in another pod's pid namespace.

## One writer per wire

Declared values, overrides, and restores all go through the operator,
so it is the only process that writes a panel's DDC/CI bus for a
`Display`. A pod that holds a connector's
[control device](/docs/reference/devices/#the-control-device) writes
that bus itself while it runs. Don't set declared values or write
overrides for a screen whose control device a pod holds.

## Events

The operator posts an `Event` on a `Display` for each condition that
changes and for each action it takes on the screen. The API server
deletes an `Event` one hour after its last write. A `Display` is
cluster-scoped, so its `Event`s are in the `default` namespace.
`kubectl describe display` shows them, and `kubectl events` shows them
only with `-n default` or `-A`:

    kubectl events -n default --for display/boe-1080-display

A condition change posts one `Event` with the condition's reason and
message. A new message with the same status and reason posts nothing.
The operator posts the `Event` after the status write succeeds, so a
write that the API server refuses posts nothing. When the same
`Event` is posted again within 10 minutes, the operator adds to the
count of the first one, and `kubectl describe` shows it once, with
the count.

| Reason | Type | Condition or action |
| --- | --- | --- |
| `PanelAttached` | Normal | `Connected` is `True`: a panel is on the connector. |
| `NoPanel` | Normal | `Connected` is `False`: the connector has served no EDID for this monitor for 5 seconds. |
| `AnswersDDC` | Normal | `Responsive` is `True`. |
| `NoDDCReply` | Normal | `Responsive` is `False`. A panel in standby does not respond to DDC/CI. |
| `Serving` | Normal | `CompositorServing` is `True`. |
| `Down` | Warning | `CompositorServing` is `False`: the compositor's socket refuses the connection. |
| `Hung` | Warning | `CompositorServing` is `False`: the socket accepts the connection and the compositor does not respond. |
| `ReadFromEDID` | Normal | `PhysicalAddressCurrent` is `True`. |
| `Retained` | Normal | `PhysicalAddressCurrent` is `False`, and `status.physicalAddress` keeps the last valid address. |
| `Ambiguous` | Warning | `PhysicalAddressCurrent` is `False`: two connectors serve this monitor with different addresses. |
| `LayoutFound`, `DefaultLayout` | Normal | `LayoutResolved` is `True`. |
| `LayoutNotFound` | Warning | `LayoutResolved` is `False`: no `Layout` has the name in `spec.layout`. |
| `WriteUnconfirmed` | Warning | A write is new in `status.unconfirmed`. The message gives the control, the value, and the value the device read back. |
| `ModeChanged` | Normal | A mode change restarts the compositor, and every screen on the card goes blank. The message gives the mode before and after. |
| `CompositorKilled` | Warning | The compositor did not respond for 10 seconds, and the operator ended it. The compositor's container starts it again. The `Event` is on every `Display` of the node. |
| `PanelStandbyFailed` | Warning | The panel did not enter standby after its last claim ended, and stays on. The operator does not try again. |
| `Captured` | Normal | A capture through the API returned bytes. The message gives the subject, the aspect, and the media type. |
| `ArrangerGranted` | Normal | A client of a claim became the claim's arranger, and is new in `status.arrangers`. The message gives the claim, the pods that hold it, and the region. |
| `ArrangerLost` | Warning | A claim's arranger disconnected while a pod that holds the claim still runs and is not being deleted, as when the client crashes. The claim's windows return to fill the region. |
| `ArrangerReleased` | Normal | A claim's arranger destroyed its arranger object and gave up the region, or it disconnected after the pods that held the claim were gone or being deleted. |

An arranger that commits a new arrangement posts no `Event`, because
a commit follows each key press. `status.arrangers` shows the name of
the last arrangement. A compositor restart ends every arranger, and
the pass that reads the new compositor posts no `ArrangerLost` for
them, because `CompositorServing` or `CompositorKilled` already
reports the restart.
