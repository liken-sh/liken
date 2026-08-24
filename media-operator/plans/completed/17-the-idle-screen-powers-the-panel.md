# The idle screen powers the panel

Plan 17. The idle pod holds the panel's control device and writes DDC/CI
itself, so the panel goes dark after a longer quiet stretch and comes
back the moment a person presses a control. This is the hardware half of
[plan 09](../09-the-idle-screen.md)'s sleep, built on the fade of
[plan 16](16-the-idle-screen-goes-dark.md). It answers the open problem
this repository carried as `how-the-idle-screen-asks-for-power.md`.

## The problem

Plan 16 fades the pixels and leaves the panel lit. A screen that shows
black all night still burns its backlight. Plan 09 deferred the hardware
half because the seam was undecided: how a standing pod raises and drops
a power request across one long-lived draw claim.

Every shape that moved the request through the Kubernetes API failed on
verified facts. A claim is never prepared without a consuming pod, and a
pod's claim list is immutable, so a power claim costs a pod schedule per
wake. A claim's spec is immutable from creation, so a parameter cannot
change over time. The kubelet never redelivers a changed claim to a DRA
driver, and `resourceclaims` has no node-scoped field selector, so a
driver that watched for annotations would watch the whole cluster. The
compositor offers no path either: `kiosk-shell` contains no idle, DPMS,
or power code, and every DRM write from outside the master returns
`EACCES`.

## The design

### The seam already exists

The display-operator publishes a `-control` device for every connector
whose panel answers DDC/CI. Its manual states the purpose: a claim for a
pod that drives the panel itself while it runs. A prepared control claim
delivers `/dev/i2c-N` and `DISPLAY_CONTROL_BUS` and the display-operator
performs no write for it. So the process that already owns the sleep
timer and the bus, the idle command sidecar, takes the wire and writes
the panel. The operators exchange nothing. The wake is one on-node i2c
write on the same event that lifts the shade.

### The `Player` opts in with a control device

`PlayerSpec` gains `control`, a `PlayerDevice` beside `display` and
`render`, naming the cluster's control class. A panel that refuses
DDC/CI publishes no control device, so the field is an explicit opt-in
and a `Player` that states none keeps the fade alone. The idle claim
adds a `control` request, and a claim-level `matchAttribute` constraint
on `monitor.liken.sh/id` ties it to the draw request, so the wire and
the screen are the same panel. The claim types gain `constraints` for
this. The sidecar container names the request, and the display-operator's
CDI edit delivers the node and `DISPLAY_CONTROL_BUS` with no wiring on
the media side.

### The policy: a second window and a mode

The `idle` block of plan 16 gains the two hardware fields, on the
`Player` and on the `MediaPreferences` default, resolved field by field
like the fade:

```yaml
idle:
  fadeAfterSeconds: 600
  offAfterSeconds: 1800
  offMode: backlight
```

Both windows measure the same quiet stretch. The screen fades at
`fadeAfterSeconds` and the panel goes dark at `offAfterSeconds`. Zero or
absent `offAfterSeconds` means the panel never goes dark, which is the
built-in default, because darkening hardware is opt-in twice: the
control device and the window. The sidecar clamps `offAfterSeconds` to
at least the fade, so the panel never goes dark behind a still-lit
image.

`offMode` is what the sidecar writes at the window:

* `backlight`, the default: read the panel's brightness, remember it,
  and write 0. The panel stays in a state that always answers DDC, so
  the wake cannot strand. The wake writes the remembered value back,
  or 100 when none was read.
* `power`: write DPM off (`0xD6 04`). Deeper, and on some panels the
  DDC/CI scaler goes down with it and only a signal restart or a human
  brings it back. State `power` only for a panel the drill proved wakes
  over DDC.

### The wake writes pixels first

A press or a status that leaves `Idle` wakes the screen as plan 16
built. With a control device the same wake also writes the panel up,
after the shade lift is sent, because pixels need no hardware and a lit
panel showing black beats a dark one. A wake write that fails retries on
a bounded ladder, twenty tries a second apart, then stops and reports.
Never an unbounded loop.

### The actual panel state reports in `Player` status

The sidecar holds no API credentials, so it publishes the panel's state
as a retained message on `liken/media/players/<ns>/<name>/panel`, and
the operator folds it into a new `PlayerStatus.Panel` field on its pass:
`On`, `BacklightOff`, `Off`, or `Unresponsive`. The condition doctrine
holds: the status reports what the sidecar actuated and read back, not
what a person hopes the glass shows.

### One writer per wire

The display-operator writes DDC on the same bus at prepare and
unprepare when a claim states `brightness` or `power`, and nothing
arbitrates two userspace writers on one i2c wire. The rule, stated in
both manuals: on a screen whose control device the idle pod holds, no
claim states `power`. A `Play` claim may still state `brightness`, the
way the studio `Player` states 100 today; that write lands at the
`Play`'s prepare and points the same direction as the sidecar's wake,
so the two do not fight, and the sidecar's restore uses the value it
last read.

### The DDC code is the sidecar's own

The sidecar gains a small `ddc.go` that speaks exactly the two VCP
codes it writes, `0x10` brightness and `0xD6` power mode, with the
timing the DDC/CI standard sets: 50 ms after a write, 40 ms before a
reply read. A set is not followed by a readback, on purpose: the read
is itself a wake stimulus on some panels, so a verify after the sleep
write would light the panel it just put down. The drill's own
`ddcutil` readback proves the state instead. The code is modeled on
the display-operator's, and it lives here because the pod that holds
the wire owns the writes on it.

## What was considered and set aside

**A power claim taken and released.** Set aside on the verified facts
above: every wake would cost a pod schedule, seconds of black screen
after a button press.

**A desired-power field the display-operator reads.** Set aside because
the display-operator reads claims and the card and nothing else, and a
media-side read would make it a media-aware component with a
cluster-wide watch.

**An annotation on the draw claim in the display-operator's
vocabulary.** Legal, and the best of the API shapes, but it costs the
driver a cluster-wide claim watch, a hand-rolled watch loop, and a
second parameter channel beside the immutable one. Kept as the fallback
if the control device fails in practice.

**Compositor or kernel DPMS.** Impossible without patching `weston`,
and the project does not patch upstream projects.

## How the work is proved

The timers and the wake ladder are proved by Go tests with the fade
fixture, and the DDC dialogue by tests against a scripted i2c stand-in.

The drill runs on `liken-1`, on the studio `Player` and its BOE panel,
with short windows. Set `offAfterSeconds: 120` and `offMode: backlight`.
After two quiet minutes, `ddcutil getvcp 10` from the display-operator
pod reads 0 and `Player` status reads `BacklightOff`. Publish a press on
the pad's events topic, and the brightness reads back restored and the
status returns to `On`. Start a `Play` while the panel is dark, and the
panel lights before the film's first frame matters.

The `power` mode gates on the metal drill a person runs at the panel:
whether the BOE still answers DDC in DPM off, and whether a compositor
restart wakes it. Until that drill passes, every `Player` states
`backlight` or nothing.
