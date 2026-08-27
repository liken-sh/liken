# A Display for every panel

The operator reports a panel's controls as two booleans and delivers
the raw i2c wire to a consumer pod that writes DDC/CI itself. This
plan adds a `Display` resource for every monitor, under
`display.liken.sh/v1alpha1`. Its `status` reports what the panel
declares and what the operator last observed. Its `spec` declares the
settings the panel rests at, and a `spec.override` block holds a
temporary state that a machine writer sets and later lifts. The
operator becomes the only process that writes the wire, and the
control device retires.

The consumer half is the media-operator's
[plan 18](https://github.com/liken-sh/media-operator/blob/main/plans/completed/18-blanking-moves-to-the-display.md).

## The problem

The panel's resting brightness is stored in the memory of one
process. The media-operator's
[plan 17](https://github.com/liken-sh/media-operator/blob/main/plans/completed/17-the-idle-screen-powers-the-panel.md)
built the idle sidecar: at the off window it reads brightness over
DDC, stores the value in process memory, and writes 0. Only the
in-process wake writes the value back. A sidecar restart while the
panel is dark starts a process that has no stored value and reports
the panel lit. At its next off window it reads 0, stores 0, and every
later wake writes 0. The panel stays dark until a person fixes it by
hand.

The probe reads two hard-coded codes, `0x10` and `0xD6`, and
publishes two booleans, `controlsBrightness` and `controlsPower`. A
DDC/CI panel declares its full feature list in the MCCS capability
string. A drill on `liken-1` (2026-08-27) read both panels' strings.
The LG WQHD carries brightness, contrast, color presets, video
gains, input source, audio volume, audio mute, and power. The BOE
portable carries brightness, contrast, sharpness, color presets,
video gains, input source among seven inputs, OSD language, OSD
button lock, and power. None of that is visible in the cluster, and
no channel exists to set any of it.

The wire handover exists because a claim cannot carry a live
request. A claim's spec is immutable and the kubelet never
redelivers a changed claim, so [plan 07](completed/07-sharing-the-screen.md)
gave the standing consumer the wire. The one-writer rule that
protects the wire is a documented convention, not a mechanism.

## The design

### One resource per monitor

The operator creates a cluster-scoped `Display` for every monitor it
probes, named by the monitor id it already publishes as
`monitor.liken.sh/id`. The resource is hardware truth: the operator
owns it, creates it, and writes all of `status`. A panel is physical
and belongs to no namespace, so the resource is cluster-scoped, like
a `Node`.

`status` reports:

- `node` and `connector`, so a reader can place the panel.
- `capabilities`, parsed from the capability string. Each entry
  names a control in plain words with its legal values:
  `brightness: {max: 100}`, `input: [HDMI-1, HDMI-2, DP-1, DP-2]`,
  `power: [on, off]`. Only the MCCS common core is published:
  brightness, contrast, sharpness, color preset, video gains, input
  source, audio volume, audio mute, and power. Manufacturer-specific
  codes are not published until a real need names one.
- `observed`, the last value the operator read for each control.
- `captured`, the values the operator saved before it obeyed an
  override.
- Conditions: `Connected` reports whether the connector has the
  panel, and `Responsive` reports whether the panel answers DDC/CI,
  with reason `NoDDCReply` when it does not. Some panels gate DDC/CI
  behind an OSD setting, and the condition message says so.

`observed` is last-known, never live. The probe cache exists so a
steady-state reconcile pass sends zero bytes on the i2c wire, and a
DDC read is itself a wake stimulus on some panels. So the operator
updates `observed` when it actuates, probes, or captures, and at no
other time. A person at the OSD buttons diverges from `observed`
until the operator next touches the panel.

A `Display` persists when its panel disconnects, because it holds
the captured state, and `Connected` reports the absence.

### The resting layer

`spec` declares the settings the panel rests at: any control the
capability list carries, such as `brightness`, `input`, or
`audioVolume`. The operator validates a declared value against the
capability list and reconciles divergence back to the declaration,
by the same write-on-divergence rule the static host entries follow.
An empty `spec` writes nothing, per the parameters-only rule: the
operator never invents a value.

### The override

`spec.override` is a temporary layer above the resting layer. A
machine writer adds the block, the operator obeys it, and the writer
later deletes the block. The precedent is `kubectl cordon`:
`node.spec.unschedulable` is a reversible override a controller or a
person sets and clears, layered over the node's definition.

The override carries `backlight: off` or `power: off`. The rules:

- An override present wins over the resting layer.
- Before the operator obeys a `backlight: off` override, it reads
  the panel's brightness and writes the value to `status.captured`.
  The status write commits before the wire write. If the status
  write fails, the operator does not blank. This ordering is the
  fix for the lost-brightness failure above: the memory is durable
  before the panel goes dark.
- When the override clears, the operator restores: to the resting
  declaration when `spec` states one, otherwise to the captured
  value. It clears `status.captured` after the restore reads back.
- The restore reconciles until the readback matches, with backoff on
  the operator's context. This replaces the consumer's wake ladder,
  because some panels answer DDC slowly while they wake.

Writers stay disjoint through server-side apply: the media-operator
applies only `spec.override` under its own field manager, the
cluster owner writes the resting fields, and a conflict is an error,
not a silent overwrite.

An override has no TTL. If the writer crashes while an override
stands, the panel stays dark until the writer returns or a person
deletes the block. A stuck-dark panel is visible in
`kubectl get display`, and the field manager names the writer. The
alternative, a TTL the operator enforces, trades that visible fault
for a panel that lights mid-idle whenever the writer is merely slow.

If the cluster's state store is rebuilt while an override stands,
the captured value is lost, and the operator refuses to invent
brightness for a panel it finds at 0. The escape is one visible
step: declare `spec.brightness` once.

### What retires

The `Display` becomes the one channel for panel state, so the older
channels retire in order:

1. This plan ships the `Display` with the full design above. No
   writer sets overrides yet, so nothing contends with the claim
   parameters during the rollout.
2. The media-operator switches to overrides, stops requesting the
   control device, and drops its DDC client
   ([plan 18](https://github.com/liken-sh/media-operator/blob/main/plans/completed/18-blanking-moves-to-the-display.md)).
3. The `brightness` and `power` claim parameters, the power record
   file, and the `-control` device retire. A claim parameter and an
   override that write the same VCP code would be the two-writer
   problem again, one layer up. A per-play brightness becomes an
   override the consumer's operator sets around the play and lifts
   after it; no consumer needs that today, so this plan does not
   build it.

Step 3 lands in this repository but only after step 2 is deployed,
by the two-rollout rule.

## What was considered and set aside

**A TTL on the override.** Set aside above: it converts a visible
stuck-dark fault into an invisible mid-idle wake race.

**The captured value in a node-local file.** The power record file
already works this way. Set aside because the file dies with the
pod, nobody can list it, and this resource exists to make panel
state visible. The capture belongs in `status`, which survives
operator restarts, pod moves, and node reboots.

**Keeping the claim parameters beside the override.** Set aside
because two channels for one knob recreate the contention plan 07
removed. The claim parameters also cannot restore: a claim ends and
`releasePower` writes standby from a one-slot record, which is the
debt the `captured` field now records in the open.

**The override on a media-operator resource the display-operator
watches.** Set aside because it points the dependency the wrong way:
a hardware operator would then read a media API. The media-operator
already watches hardware truth; the override on the `Display` keeps
that direction.

## How the work is proved

On `liken-1`:

- `kubectl get display` lists both panels, and each `status`
  carries the capability list the 2026-08-27 drill read by hand.
- Adding `spec.override: {backlight: off}` to the BOE's `Display`
  darkens the panel, and `status.captured` holds the prior
  brightness before the panel darkens.
- Deleting the override restores the panel to the captured value.
- With `spec.brightness` declared, the same cycle restores to the
  declaration instead.
- Deleting the operator pod while an override stands, then deleting
  the override after the pod returns, still restores the panel.
  This is the failure plan 17 could not survive.
- A panel that refuses DDC/CI reports `Responsive: False` with
  reason `NoDDCReply`.
