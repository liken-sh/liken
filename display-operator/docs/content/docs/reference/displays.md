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
```

The [devices reference](/docs/reference/devices/) describes the
other paths to a panel: the claim parameters a `Play`-style workload
states once at prepare, and the control device a standing pod claims
for the raw wire. The `Display` is the declarative path: state what
the panel should hold, and the operator keeps it there.

One monitor, what it carries, and what it rests at.

## spec

The settings the panel rests at, and the override above them. Every field is optional: the operator writes a declared field back when the panel diverges from it, and it never writes a field the spec leaves out.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="spec--brightness"></span>`brightness` | integer | no | The panel's own brightness number, up to status.capabilities.brightness.max. |
| <span id="spec--contrast"></span>`contrast` | integer | no | The panel's own contrast number. |
| <span id="spec--sharpness"></span>`sharpness` | integer | no | The panel's own sharpness number. |
| <span id="spec--colorpreset"></span>`colorPreset` | string | no | One of status.capabilities.colorPreset.values. |
| <span id="spec--input"></span>`input` | string | no | One of status.capabilities.input.values: the resting declaration that forces the panel to show that input. On a shared panel it writes the panel back to this machine within a poll window of every switch away, so declare it only on a panel that should always show this machine. |
| <span id="spec--attachedinput"></span>`attachedInput` | string | no | The panel input this machine's cable occupies, one of status.capabilities.input.values. A declared fact, never written to the panel: it defers a darkening override while the panel shows another input, because brightness and power are panel-global. Declare it only where the EDID says nothing; status.attachedInput carries what the operator derived, and this declaration wins over it. Beside spec.input on a shared panel, the resting write would fight every switch away. |
| <span id="spec--audiovolume"></span>`audioVolume` | integer | no | The panel's own volume number. |
| <span id="spec--audiomute"></span>`audioMute` | boolean | no | Whether the panel's own speakers are muted. |
| <span id="spec--mode"></span>`mode` | string | no | The mode the screen rests at, one of status.modes, in the 1920x1080@60 form. A claim's own mode parameter wins while the claim holds the screen, and a change here waits for the claim to end. Applying it restarts the compositor once, which ends every Wayland client on this card. |
| <span id="spec--override"></span>`override` | [object](#specoverride) | no | The temporary layer. A writer adds the block, the operator saves what stood and obeys it, and the writer deletes the block. The operator then restores the declared resting value, or the saved one where the spec declares none. |

### spec.override

The temporary layer. A writer adds the block, the operator saves what stood and obeys it, and the writer deletes the block. The operator then restores the declared resting value, or the saved one where the spec declares none.

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
| <span id="status--attachedinput"></span>`attachedInput` | string | no | The input this machine's cable occupies, as the operator derived it from the EDID: an HDMI sink serves each of its ports an EDID naming that port. It is published so a person can check the derivation against the cabling, and a declared spec.attachedInput wins over it. A DisplayPort cable, and a panel that serves the same address on every port, derive nothing; those are the panels the owner declares for. |
| <span id="status--currentmode"></span>`currentMode` | string | no | The mode this output drives now, read from the card, absent while it drives nothing. |
| <span id="status--modes"></span>`modes` | []string | no | Every mode the card offers for this connector, whole, where the device attribute of the same name is cut to fit the API's limit on an attribute value. This is the list spec.mode is judged against. |
| <span id="status--capabilities"></span>`capabilities` | [map\[string\]object](#statuscapabilities) | no | The controls the panel declares, of the MCCS common core. A control with a value list takes those values, and a control with a maximum takes a number up to it. |
| <span id="status--observed"></span>`observed` | [object](#statusobserved) | no | The last value the operator read or wrote for each control. The operator reads the panel when it probes, when it captures before an override, when it actuates, and about every ten seconds for a panel that is lit and under no override. The ten-second read is what finds a change a person made at the panel's own buttons. A panel in standby or off is never read, because a DDC read wakes some panels. |
| <span id="status--captured"></span>`captured` | object | no | The values the operator saved before it obeyed an override. The save commits before the panel goes dark, so the value that brings the panel back survives a restart of the operator. |
| <span id="status--conditions"></span>`conditions` | [\[\]object](#statusconditions) | no | Connected reports the panel on its connector, and Responsive reports the panel answering DDC/CI, with the reason NoDDCReply when it does not. |

### status.capabilities.*

The controls the panel declares, of the MCCS common core. A control with a value list takes those values, and a control with a maximum takes a number up to it.

| Field | Type | Required | Description |
| --- | --- | --- | --- |
| <span id="statuscapabilities--max"></span>`max` | integer | no | The largest number the panel accepts for a continuous control. |
| <span id="statuscapabilities--values"></span>`values` | []string | no | Every value the panel accepts for a non-continuous control. |

### status.observed

The last value the operator read or wrote for each control. The operator reads the panel when it probes, when it captures before an override, when it actuates, and about every ten seconds for a panel that is lit and under no override. The ten-second read is what finds a change a person made at the panel's own buttons. A panel in standby or off is never read, because a DDC read wakes some panels.

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

### status.conditions[]

Connected reports the panel on its connector, and Responsive reports the panel answering DDC/CI, with the reason NoDDCReply when it does not.

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
