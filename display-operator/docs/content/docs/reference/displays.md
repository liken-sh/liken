---
title: Displays
weight: 20
toc: true
---

<!-- Generated from deploy/displays.yaml by crdref. Do not edit. -->

A `Display` is one monitor as a Kubernetes resource. The operator
creates one for every monitor it probes, cluster-scoped like a
`Node`, named by the same monitor id the devices publish as
`monitor.liken.sh/id`. You never create or delete one. The operator
writes the whole of `status`: the controls the panel declares, the
values it last saw, and the values it saved before an override. You
write the resting fields of `spec`, and a machine writer, such as a
media layer that darkens idle screens, sets and lifts
`spec.override`.

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
      values: ["on", "off", hardOff]
  observed:
    brightness: 80
    power: "on"
  conditions:
    - type: Connected
      status: "True"
    - type: Responsive
      status: "True"
    - type: CompositorServing
      status: "True"
      reason: Serving
```

The [devices reference](/docs/reference/devices/) describes the
other paths to a panel: the claim parameters a `Play`-style workload
states once at prepare, and the control device a standing pod claims
for the raw wire. The `Display` is the declarative path: state what
the panel should hold, and the operator keeps it there.

One monitor, the controls it reports, and the settings the operator maintains.

## spec

The settings the panel should maintain and a temporary override above them. Every field is optional. The operator writes a declared field when the panel diverges from it and does not write a field that spec leaves out.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--brightness"></span>`brightness` | integer | no | The panel's brightness value. It must not exceed status.capabilities.brightness.max. |
| <span id="spec--contrast"></span>`contrast` | integer | no | The panel's own contrast number. |
| <span id="spec--sharpness"></span>`sharpness` | integer | no | The panel's own sharpness number. |
| <span id="spec--colorpreset"></span>`colorPreset` | string | no | One value from status.capabilities.colorPreset.values. |
| <span id="spec--input"></span>`input` | string | no | One value from status.capabilities.input.values. This resting declaration forces the panel to show that input. On a shared panel, the operator writes the panel back to this machine within one polling window after each switch away. Declare it only on a panel that should always show this machine. |
| <span id="spec--audiovolume"></span>`audioVolume` | integer | no | The panel's own volume number. |
| <span id="spec--audiomute"></span>`audioMute` | boolean | no | Whether the panel's own speakers are muted. |
| <span id="spec--mode"></span>`mode` | string | no | The resting screen mode, in the 1920x1080@60 form, and one of status.modes. A claim's mode parameter takes precedence while the claim holds the screen. An edit here waits for the claim to end. Applying the mode restarts the compositor once and ends every Wayland client on this card. |
| <span id="spec--layout"></span>`layout` | string | no | The Layout this screen shows, by name. If this field is absent, the screen shows every window fullscreen with the newest on top. If the name matches no Layout, the screen shows that same arrangement and reports the name under the LayoutResolved condition. Pattern: `^[a-z0-9]([-a-z0-9.]*[a-z0-9])?$`. |
| <span id="spec--override"></span>`override` | [object](#specoverride) | no | A temporary layer above the resting settings. When a writer adds this block, the operator saves the current values and applies the override. When the writer deletes it, the operator restores the declared value or the saved value when spec declares none. |

### spec.override

A temporary layer above the resting settings. When a writer adds this block, the operator saves the current values and applies the override. When the writer deletes it, the operator restores the declared value or the saved value when spec declares none.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="specoverride--backlight"></span>`backlight` | string | no | Hold the panel dark at brightness zero. One of: `off`. |
| <span id="specoverride--power"></span>`power` | string | no | Hold the panel powered down. Some panels stop answering DDC/CI from power off; state this only for a panel a drill proved wakes. One of: `off`. |

## status

What the operator read and what it last wrote. The operator owns every field here.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="status--node"></span>`node` | string | no | The machine whose graphics card drives this panel. |
| <span id="status--connector"></span>`connector` | string | no | The connector on that card, in the kernel's own spelling. |
| <span id="status--manufacturer"></span>`manufacturer` | string | no | The monitor's manufacturer, from its EDID, the same value the device attribute carries. |
| <span id="status--model"></span>`model` | string | no | The monitor's model name, from its EDID. |
| <span id="status--serial"></span>`serial` | string | no | The monitor's serial, from its EDID, and absent when the monitor states none. |
| <span id="status--widthmillimeters"></span>`widthMillimeters` | integer | no | The panel's physical width, as the monitor states it. |
| <span id="status--heightmillimeters"></span>`heightMillimeters` | integer | no | The panel's physical height, as the monitor states it. |
| <span id="status--physicaladdress"></span>`physicalAddress` | string | no | The HDMI-CEC physical address of the port this machine's cable is in, in the dotted form 1.2.0.0, from the HDMI vendor block of the EDID the connector serves. A CEC adapter announces this address when it speaks for this machine. The field keeps the last valid address while the connector serves no EDID for this monitor, while it serves no valid address in it, or while two connectors on this node serve this monitor with different addresses, and the PhysicalAddressCurrent condition says which. The field is absent while the monitor has never served a valid address, for example on a DisplayPort cable. 0.0.0.0, f.f.f.f, and an address with a non-zero digit after a zero are not valid. |
| <span id="status--mode"></span>`mode` | [object](#statusmode) | no | The mode this output runs, from the two parties that each report one. The kernel syncing a mode on the connector and the compositor serving canvases at that mode are two different facts, and a client draws at the second one, so a gap between the two values is the canvas defect and this object is where it shows. |
| <span id="status--modes"></span>`modes` | []string | no | Every mode the card offers for this connector, whole, where the device attribute of the same name is cut to fit the API's limit on an attribute value. This is the list spec.mode is judged against. |
| <span id="status--capabilities"></span>`capabilities` | [map\[string\]object](#statuscapabilities) | no | The controls the panel declares, of the MCCS common core. A control with a value list takes those values, and a control with a maximum takes a number up to it. |
| <span id="status--observed"></span>`observed` | [object](#statusobserved) | no | The last value the operator read or wrote for each control. It reads the panel during probing, before an override capture, while it actuates a control, and about every ten seconds when the panel is lit and has no override. The ten-second read finds changes made with the panel's own buttons. The operator never reads a panel in standby or off because a DDC read wakes some panels. |
| <span id="status--captured"></span>`captured` | object | no | The values the operator saved before it applied an override. The save commits before the panel goes dark, so the restore value survives an operator restart. |
| <span id="status--surfaces"></span>`surfaces` | [\[\]object](#statussurfaces) | no | Every window the compositor holds on this screen, in arrival order, whether or not a region shows it. A window with no region is running but is not displayed. Read this field first when a program draws nothing visible. An ID lasts only for the compositor that assigned it. A compositor restart ends every window, and programs reconnect under new IDs. |
| <span id="status--layout"></span>`layout` | [object](#statuslayout) | no | The arrangement the screen is drawn to, and what each region shows. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Connected reports the panel on its connector, and Responsive reports the panel answering DDC/CI, with the reason NoDDCReply when it does not. LayoutResolved is False with the reason LayoutNotFound while spec.layout names a Layout the cluster does not hold, and the screen shows the default arrangement until it does. CompositorServing reports the compositor behind the screen. It is False with the reason Down while the compositor's socket refuses the connect, and with the reason Hung while the socket accepts and the compositor answers nothing; the message is the socket's own words. status.surfaces and status.layout are empty for as long as it is False. PhysicalAddressCurrent is True with the reason ReadFromEDID while the connector's current EDID serves status.physicalAddress. It is False with the reason Retained while the connector serves no EDID for this monitor or serves no valid address, and status.physicalAddress then holds the last valid address; the message names the time the connector stopped serving it. It is False with the reason Ambiguous while two connected connectors on this node serve this monitor with different addresses; the message names both connectors and both addresses, and status.physicalAddress keeps the value it held before they disagreed. The condition is absent while the monitor has never served a valid address. |

### status.mode

The mode this output runs, from the two parties that each report one. The kernel syncing a mode on the connector and the compositor serving canvases at that mode are two different facts, and a client draws at the second one, so a gap between the two values is the canvas defect and this object is where it shows.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusmode--kernel"></span>`kernel` | string | no | The mode the card reports this connector is synced to, absent while it drives nothing. |
| <span id="statusmode--weston"></span>`weston` | string | no | The mode the compositor reports it serves canvases at, from its own wl_output events, absent while the operator holds no connection to a compositor. |

### status.capabilities.*

The controls the panel declares, of the MCCS common core. A control with a value list takes those values, and a control with a maximum takes a number up to it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscapabilities--max"></span>`max` | integer | no | The largest number the panel accepts for a continuous control. |
| <span id="statuscapabilities--values"></span>`values` | []string | no | Every value the panel accepts for a non-continuous control. |

### status.observed

The last value the operator read or wrote for each control. It reads the panel during probing, before an override capture, while it actuates a control, and about every ten seconds when the panel is lit and has no override. The ten-second read finds changes made with the panel's own buttons. The operator never reads a panel in standby or off because a DDC read wakes some panels.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusobserved--brightness"></span>`brightness` | integer | no |  |
| <span id="statusobserved--contrast"></span>`contrast` | integer | no |  |
| <span id="statusobserved--sharpness"></span>`sharpness` | integer | no |  |
| <span id="statusobserved--colorpreset"></span>`colorPreset` | string | no |  |
| <span id="statusobserved--input"></span>`input` | string | no |  |
| <span id="statusobserved--audiovolume"></span>`audioVolume` | integer | no |  |
| <span id="statusobserved--audiomute"></span>`audioMute` | boolean | no |  |
| <span id="statusobserved--power"></span>`power` | string | no |  |

### status.surfaces[]

Every window the compositor holds on this screen, in arrival order, whether or not a region shows it. A window with no region is running but is not displayed. Read this field first when a program draws nothing visible. An ID lasts only for the compositor that assigned it. A compositor restart ends every window, and programs reconnect under new IDs.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussurfaces--id"></span>`id` | string | yes | The window's id, which the compositor assigned. status.layout names it beside the region that shows it. |
| <span id="statussurfaces--claim"></span>`claim` | string | no | The ResourceClaim whose socket the window arrived on, as namespace/name. Empty for a window on the shared socket, which belongs to no claim. |
| <span id="statussurfaces--pods"></span>`pods` | []string | no | The pods that hold the claim, each as namespace/name. |
| <span id="statussurfaces--labels"></span>`labels` | map[string]string | no | The labels every holder of the claim carries with the same value. These are the labels a region's selector reads. When one pod holds the claim they are that pod's labels. |
| <span id="statussurfaces--size"></span>`size` | [object](#statussurfacessize) | no | The size the program last drew, in pixels. A window in a region draws at the region's size, so a size that does not match its region is a program that ignored the compositor's request and is scaled to fit. |
| <span id="statussurfaces--region"></span>`region` | string | no | The region that shows the window. Empty for a window no region took, which is not on the screen. |

#### status.surfaces[].size

The size the program last drew, in pixels. A window in a region draws at the region's size, so a size that does not match its region is a program that ignored the compositor's request and is scaled to fit.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statussurfacessize--width"></span>`width` | integer | no | The width in pixels. |
| <span id="statussurfacessize--height"></span>`height` | integer | no | The height in pixels. |

### status.layout

The arrangement the screen is drawn to, and what each region shows.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuslayout--name"></span>`name` | string | no | The Layout in force, or default for a screen that names none or names one that does not exist. |
| <span id="statuslayout--regions"></span>`regions` | [\[\]object](#statuslayoutregions) | no | Each region in stacking order, with the window on top of it. |

#### status.layout.regions[]

Each region in stacking order, with the window on top of it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuslayoutregions--name"></span>`name` | string | yes | The region's name, as the Layout states it. |
| <span id="statuslayoutregions--surface"></span>`surface` | string | no | The id of the window on top of the region, the newest of the claim it shows, or the word empty when no window matched it. |

### status.conditions[]

Connected reports the panel on its connector, and Responsive reports the panel answering DDC/CI, with the reason NoDDCReply when it does not. LayoutResolved is False with the reason LayoutNotFound while spec.layout names a Layout the cluster does not hold, and the screen shows the default arrangement until it does. CompositorServing reports the compositor behind the screen. It is False with the reason Down while the compositor's socket refuses the connect, and with the reason Hung while the socket accepts and the compositor answers nothing; the message is the socket's own words. status.surfaces and status.layout are empty for as long as it is False. PhysicalAddressCurrent is True with the reason ReadFromEDID while the connector's current EDID serves status.physicalAddress. It is False with the reason Retained while the connector serves no EDID for this monitor or serves no valid address, and status.physicalAddress then holds the last valid address; the message names the time the connector stopped serving it. It is False with the reason Ambiguous while two connected connectors on this node serve this monitor with different addresses; the message names both connectors and both addresses, and status.physicalAddress keeps the value it held before they disagreed. The condition is absent while the monitor has never served a valid address.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statusconditions--type"></span>`type` | string | yes |  |
| <span id="statusconditions--status"></span>`status` | string | yes | One of: `True`, `False`, `Unknown`. |
| <span id="statusconditions--reason"></span>`reason` | string | yes |  |
| <span id="statusconditions--message"></span>`message` | string | no |  |
| <span id="statusconditions--lasttransitiontime"></span>`lastTransitionTime` | string | yes |  |

## The resting layer

A declared field is a standing instruction. On every pass the
operator compares the declaration with the value it last saw, and it
writes the panel only where the two diverge, so a settled panel
costs nothing on the wire. A declared value is validated against
`status.capabilities`: a value the panel does not carry fails the
pass and is never written. An empty `spec` writes nothing at all.
The operator invents no value, ever: a panel with no declarations
keeps whatever its own menu holds.

## The override

`spec.override` holds a temporary state above the resting layer, the
way `kubectl cordon` holds `spec.unschedulable` above a `Node`'s
definition. A writer adds the block, and the operator obeys it. The
writer deletes the block, and the operator restores the panel: to
the resting declaration where `spec` states one, otherwise to the
value it captured.

The capture is the load-bearing step. Before the operator obeys
`backlight: off`, it reads the panel's brightness and writes the
value to `status.captured`, and only a committed capture is followed
by the write that darkens the panel. A capture in `etcd` survives an
operator restart, a pod move, and a reboot, so the restore does too.
The restore retries until the panel reads back the value, because a
panel that is waking answers late.

An override has no timeout. If the writer that set one crashes, the
panel stays dark until the writer returns or a person deletes the
block. That failure is visible: `kubectl get display` shows the
standing override, and the block's field manager names the writer
that owes the lift.

## The resting mode

`spec.mode` follows the resting pattern with one difference in
weight: a mode lands through the compositor, and applying it
restarts the compositor once, which ends every Wayland client on
the card. So the operator applies a resting mode only while no
claim holds the screen. A claim's own `mode` parameter wins for the
claim's lifetime, a `spec.mode` edit during a claim waits for the
claim to end, and the unprepare that frees the screen restores the
declaration promptly.

## The two values of the mode

`status.mode` reports the mode twice, because two parties each
report one and they can disagree. `kernel` is the mode the graphics
card is synced to on the connector. `weston` is the mode the
compositor lays canvases out at, read from the compositor's own
`wl_output` events over a standing connection the operator holds.
A client draws at the second one. When the two values differ, the
clients on that screen are drawn at the wrong size, and the
operator restarts the compositor to correct it once the screens are
free. `weston` is absent while the operator holds no connection to
a compositor, and `kernel` is absent while the connector drives
nothing. `kubectl get displays` shows the two as the `MODE` and
`CANVAS` columns.

## The physical address

`status.physicalAddress` is the HDMI-CEC physical address of the
port this machine's cable is in: four hex digits that give the path
from the TV, one digit for each HDMI port on the way. A machine on
input 2 of a receiver that is on the TV's input 1 reads `1.2.0.0`.
An HDMI sink serves each of its ports an EDID whose vendor block
states that port's address, and the operator reads it from the EDID
on the connector. A CEC adapter on the machine has no EDID of its
own, so it announces this address when it sends `Active Source`,
and the receiver and the TV switch to the machine's picture. The
operator does not open a CEC adapter or send a CEC message.

A receiver in standby can stop serving its EDID, or pass the TV's
EDID through, which belongs to another `Display`. The machine's port
has not moved, so the field keeps the last valid address, and the
`PhysicalAddressCurrent` condition states where the value came
from:

| Status | Reason | Meaning |
| --- | --- | --- |
| `True` | `ReadFromEDID` | The connector's current EDID serves this address. |
| `False` | `Retained` | The connector serves no EDID for this monitor, or serves no valid address in it. The message names the time it stopped serving the address. |
| `False` | `Ambiguous` | Two connected connectors serve this monitor with different addresses. The message names both connectors and both addresses. |

The condition is absent while the monitor has never served a valid
address, for example on a DisplayPort cable. `0.0.0.0` is the TV's
own address, which a sink serves when it states none, and `f.f.f.f`
is the invalid address, so the operator publishes neither. It also
refuses an address with a non-zero digit after a zero, such as
`1.0.2.0`, because a zero ends the path. The output device publishes
the same value as its `physicalAddress` attribute, from the current
EDID only, so a dark connector publishes none. Read the retained
value from the `Display`.

The address belongs to one connector, and a `Display` has one
connector. Two connectors on one machine that serve EDIDs with the
same identity, such as two cables to one receiver, map to one
`Display`, and it reports the connector that sorts last by name
among those that are connected.

Those two connectors can serve different addresses, for example one
cable that carries the picture into a receiver input and a second,
through a CEC adapter, into another input of the same receiver. When
they disagree, the `Display` publishes no current address:
`PhysicalAddressCurrent` reads `Ambiguous`, neither connector's
device states a `physicalAddress` attribute, and `status.physicalAddress`
keeps the value it held before the two disagreed.

## Shared screens

A monitor with several inputs dims all of them at once, because
brightness and power are panel-global, and the operator writes what
an override states whenever the panel answers. Panels do not say
reliably which input they show: the query is optional, and a panel
can answer it with the name of the port the question arrived on. So
whether a screen should ever go dark is its owner's declaration,
not the operator's guess. State it in the layer that writes the
override; the media operator's `Player` carries an idle policy
whose `offAfterSeconds: 0` keeps a shared screen's panel untouched.

## Observed values

`status.observed` is what the operator last read or wrote. The
operator touches the wire when it probes, when it captures before
an override, when it actuates, and about every ten seconds for a
panel that is lit and under no override. That last read is what
finds a change a person made at the panel's own menu, and it is
what makes
a resting declaration hold: the pass that finds the divergence
writes the declaration back. A panel in standby or off, a panel an
override holds, and a panel that answers nothing are never read on
a timer, because a DDC/CI read is itself a wake stimulus on some
panels, and a polling loop would relight the screens the override
layer darkened. For those panels, `observed` stays what the
operator last saw.

## One writer per wire

The operator is the one process that writes a panel's i2c wire for
the `Display`: resting declarations, overrides, and restores all
land through the same reconciler. The
[one-writer rule](/docs/reference/devices/#the-control-device) in
the devices reference still governs the other paths: a pod that
holds a connector's control device owns that wire while it runs, so
do not declare resting values or write overrides for a screen whose
control device a pod holds.
