# Blanking moves to the Display

The idle sidecar holds a control claim and writes DDC/CI itself: it
reads the panel's brightness, stores it in process memory, and
writes 0 at the off window. This plan removes the wire from the
sidecar. The sidecar keeps the idle timing and publishes its desire
on the bus, the operator writes that desire as a `spec.override` on
the display-operator's `Display` resource, and the display-operator
actuates it. MQTT stays the media layer's bus; the hardware layer
never reads it.

The hardware half is the display-operator's
[plan 08](https://github.com/liken-sh/display-operator/blob/main/plans/completed/08-a-display-for-every-panel.md).

## The problem

[Plan 17](completed/17-the-idle-screen-powers-the-panel.md) put the
remembered brightness in the sidecar's process memory, and only the
in-process wake restores it. A sidecar restart while the panel is
dark starts a process that has no stored value and reports the panel
lit. At its next off window it reads 0, remembers 0, and every later
wake writes 0. The panel stays dark until a person fixes it.

The sidecar is also the wrong holder of the wire. It owns i2c retry
timing, a wake ladder, and a DDC client, none of which are media
concerns. The display-operator's `Display` resource now gives the
cluster a declarative channel for panel state, with the capture and
restore built in, so the media layer can state what it wants and
stop writing hardware.

## The design

### The sidecar drops the wire

The DDC client, the control-bus environment variable, and the wake
ladder leave the sidecar. The idle claim drops its `control` request
and the constraint that matched it to the screen. The fade, the idle
timing, the bus, and mpv stay exactly as they are.

`Player.spec.control` retires with the claim request. The CRD field
and its plumbing are removed.

### The bus carries the desire

The sidecar already publishes retained panel state on its panel
topic. The topic's meaning changes from the state the sidecar
actuated to the state the sidecar wants: `on` while the player is
awake, `off` when the off window arrives. The sidecar publishes and
never actuates, so the `Unresponsive` state retires with the wire.

### The operator writes the override

The operator already consumes the retained panel topic. On `off` it
applies `spec.override` to the screen's `Display`; on `on` it
deletes the block, and the display-operator restores the panel. The
resolved `offMode` picks the override: `backlight` applies
`{backlight: off}` and `power` applies `{power: off}`.

The operator finds the `Display` by the allocated device: the
claim's `status.allocation` names the device, the device's
`ResourceSlice` entry carries `monitor.liken.sh/id`, and the
`Display` is named by that id. This is the same lookup codec
selection performs.

The override is applied with server-side apply under the operator's
field manager, touching only `spec.override`, so the cluster owner's
resting fields never conflict.

`PlayerStatus.Panel` now folds the `Display`'s observed state
instead of the sidecar's report, so the status keeps reporting what
the hardware last showed.

## What was considered and set aside

**The sidecar writes the `Display` itself.** Set aside because a
media pod would then need Kubernetes credentials and RBAC on a
hardware API. The operator already watches the bus and already holds
a client; the sidecar keeps zero credentials.

**Keeping the wire as a fallback.** Set aside because a fallback
writer is a second writer, which is the fault this design removes.
A cluster without the display-operator's `Display` support simply
keeps its panel lit, the same result as a panel with no control
device today.

## How the work is proved

On `liken-1`, with display-operator plan 08 deployed first:

- After the quiet window, the screen's `Display` shows the override
  and a captured brightness, and the panel reads 0 over DDC.
- A press on the remote clears the override and the panel restores.
- Deleting the idle pod while the panel is dark, then pressing a
  remote after the pod returns, still restores the panel. This is
  the failure plan 17 could not survive.
- The idle claim no longer requests a control device, and the
  sidecar image carries no DDC client.
