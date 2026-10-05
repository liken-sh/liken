# 77, The media bus stops at `media-operator`

Proposed, built, and drilled on `liken-1` on 2026-10-04, and released
in `2026.10.04-003`. A remote's volume key becomes an ask that
`media-operator` reads from the bus and writes into the `Receiver` or
the `Sink` that sets the room's level. The device's operator applies
the ask and reports the device's level in the object's status, and
`media-operator` relays that report back onto the bus for the screens.
`equipment-operator` stops using the bus for everything, not only for
volume: power, the input asks, the screen asks, and the settings
topic move to the `Receiver` and the `Television` too. The work touches
`media-operator`, `media-screen`, `library-operator`'s browser,
`equipment-operator`, and `audio-operator`.

## The problem

On a home cluster, a unit's sound goes through a Denon receiver.
When a person holds the remote's volume-down key, the volume steps
down a little, stops, sometimes goes up, and then steps down again.
The volume never moves faster than about one step for each round trip
to the receiver.

The `equipment-operator` logs from one held key show three failures.

### Presses are lost

The remote repeats a held key about every 42 ms. Each repeat reaches
the receiver session, and the session moves the receiver one
`spec.volume.step` from the volume in `driver.State()`. That value is
the receiver's last report. The Denon driver does not record a volume
that it sends. It records a volume only when the receiver reports it.
So every press that arrives before the receiver answers computes the
same target:

```
the volume topic went from level 59 to 54; sent volume 45.5; reported after 128 ms
the volume topic went from level 54 to 49; sent volume 45.5; reported after 86 ms
the volume topic went from level 49 to 44; sent volume 45.5; reported after 45 ms
the volume topic went from level 44 to 39; sent volume 45.5; reported after 3 ms
```

Four presses moved the receiver one 0.5 dB step. In four seconds,
about 40 presses moved it from 46 to 40.

### The two writers count in different units

The pod that handles a press reads the last level on the `volume`
topic, adds the keymap's step of 5, and publishes the result. The
session moves the receiver 0.5, which is about 0.7 on the topic's 0
to 100 scale for this receiver's `spec.volume.max` of 72. So the pod's
count moves away from the receiver: in one burst it went from 23 down
to 3 while the receiver stayed at 41.

### The session reads its own report as a press up

When the receiver reports, the session publishes the reported position
on the same topic. The pod's count then jumps back to that position,
and the session compares the next message with the one before it:

```
12:15:25.164  the volume topic went from level 63 to 23; sent volume 44.5
12:15:25.239  the volume topic went from level 23 to 58; sent volume 45.5
```

The pod had counted down to 23. The session's report of 58 arrived,
the pod's next press counted from it, and the session read 23 to 58
as a press up. The receiver went up 1 dB while the key was held down.

### The stopgap

Release `2026.10.04-002` fixed the first and third failures inside the
current design. The receiver session steps each press from the volume
it last sent, and it publishes no report until the receiver reports
that volume, or until one second passes
(`equipment-operator/session_target.go`). The home cluster runs it,
and a held key now moves the receiver smoothly. The second failure,
two step scales, remains, and so does the design that caused all
three.

### Why this is a design flaw

The `players/{namespace}/{name}/volume` topic is one retained value
with four writers: the playback pod's command sidecar, the idle screen
client, `media-operator`'s seed, and the receiver session. The readers
give it two meanings. The pods treat it as state and add to it. The
receiver session treats each change as a command and reads only its
direction. `equipment-operator/plans/00-design.md` names the exception
that allows this: "One rule bends. The volume topic's rule is that no
observer writes back what it saw." All three failures come from that
exception.

The bus also crosses a layer boundary. `equipment-operator` is a
device operator, the same kind as `audio-operator`, but it connects to
the media bus and writes into the media topic tree. `audio-operator`
does not, so a unit with a receiver and a unit with sinks set their
level in two unrelated ways. The broker authorizes nothing
(`media-operator/plans/open-problems/the-broker-is-not-ready-to-share.md`),
so every program on the bus can also drive the receiver.

## How other systems split commands from state

[Homie 5.0.0](https://homieiot.github.io/specification/) (May 2025)
gives each settable property two topics. A controller publishes a
command to the property's `/set` topic, with non-retained messages
only. The device alone writes the property topic, and it publishes the
value it applied. A device that is still moving toward a value can
publish that value as `$target`.

[Sonos](https://docs.sonos.com/docs/volume) offers `setVolume` for a
slider and `setRelativeVolume` for hardware buttons that "lack state
information", and the group coordinator adds the delta to the volume
it holds. The same page tells a controller to throttle volume
commands to one every 100 ms.

Home Assistant's `media_player` base class implements `volume_up` as
a read of the current level, plus a step, and an absolute set. That is
the read-modify-write that failed here. Its `denonavr` integration
overrides it and sends the receiver's own relative `MVUP` and
`MVDOWN` commands.

## The design

Each layer has one writer for each value, and the layers meet only
through Kubernetes objects.

```
  Remote pod ──bus──▶ remotes/{ns}/{name}/events           KEY_VOLUMEDOWN
                         │
                         ▼
                  media-operator        holds the pending target, paces the writes
                    │          ▲
     server-side    │          │  watch
     apply          ▼          │
       Receiver.status.session.volumeAsk     Receiver.status.zones.main
       Sink.status.session.volumeAsk         Sink.status.observed
                    │          ▲
            watch   ▼          │  status write
          equipment-operator, audio-operator ──▶ the device
                         │
  media-operator ──bus──▶ players/{ns}/{name}/volume        retained, for the screens
```

* The bus is part of the media domain. `media-operator`, the pods it
  starts, and the screen clients connect to it. No device operator
  does.
* `media-operator` is the only program that turns a press into a
  level. It reads the presses from the bus, as it already does for the
  input asks, and writes asks into the device objects.
* A device operator is the only program that writes what its device
  reports. It applies each new ask once and never sends the device
  back to an earlier level.
* `media-operator` is the only writer of the Player's `volume` topic.
  It relays the devices' reports there, normalized to 0.0 to 1.0.

### The asks are in `status.session`

`media-operator` already writes `status.session` on a `Receiver`, by
server-side apply under its own field manager, while a `Player` uses
the receiver (`media-operator/receiver.go`). The block names the
`Player`, its input, and the flags `active` and `awake`.
`equipment-operator` acts on a flag each time it changes to true,
does not act on the flags it finds in its first pass after a start,
and never re-asserts them, so a person with the receiver's own remote
can still change the room. That is the contract an ask needs, so the
asks go into the same block.

The asks follow the shape the `Television` already uses for its asks
(`status.session.wokeAt`, `standbyAt`, and `status.screenAsk`): each
ask carries the time it was made, with milliseconds, and each new time
is one ask.

```yaml
status:
  session:
    player: media/den
    input: liken
    active: true
    awake: true
    volumeAsk: {level: 44.5, mute: false, at: "2026-10-04T12:15:25.164Z"}
    powerAsk: {action: toggle, at: "2026-10-04T12:20:01.002Z"}
    inputAsk: {action: ensure, at: "2026-10-04T12:20:07.410Z"}
```

* `volumeAsk` is an absolute level and mute, in the device's own
  units. The operator sends it to the device when `at` changes.
* `powerAsk` is `toggle`, `on`, or `off`, as the power topic carries
  today. `equipment-operator` resolves it against the room's TV and
  receiver with the same rules as `togglePower`.
* `inputAsk` is `ensure` or `show`, as the commands topic carries
  today.

A `Sink` gets a `status.session` with `player` and `volumeAsk` only.
`media-operator` writes it by server-side apply, and `audio-operator`
writes the rest of the status under its own field manager.

The asks are in status and not in spec for three reasons. A GitOps
repository owns the spec of each `Receiver`, and an ask is not a
declaration that belongs in git. A status write changes no
`metadata.generation`, so a held key does not wake the parts of a
device operator that act on a spec change. And an ask is an event
that is applied once, while a spec field is a state that an operator
holds the device to.

### Each writer owns its fields

| Field | Writer |
|---|---|
| `Receiver.spec`, `Sink.spec` | a person or a GitOps repository |
| `Receiver.spec.power` and the settings leaves | `equipment-operator`, as today |
| `Receiver.status.session`, `Sink.status.session` | `media-operator` |
| `Receiver.status`, other than `session` | `equipment-operator` |
| `Sink.status`, other than `session` | `audio-operator` |
| `Television.status.screenAsk` | the CEC node workload, as today |
| `players/{ns}/{name}/volume` | `media-operator` |

No field has two writers. Server-side apply records the field
manager of each field, so a write by the wrong program shows in
`managedFields`.

### How `media-operator` turns presses into asks

`media-operator` holds a **pending target** for each device of each
unit, in the device's own units. This is the logic of the stopgap in
`equipment-operator/session_target.go`, moved one layer up.

1. A `KEY_VOLUMEUP` or `KEY_VOLUMEDOWN` press or repeat, from a remote
   whose focus mark names the unit, moves the target one
   `spec.volume.step` from the pending target. With no target pending,
   it moves from the device's reported level. The target stays between
   0 and `spec.volume.max`.
2. A `KEY_MUTE` press toggles the target's mute, and `KEY_UNMUTE`
   clears it.
3. The first ask goes out at once. While asks are going out,
   `media-operator` writes at most one ask for each pacing interval,
   and the last target wins. Sonos throttles to one command every
   100 ms, so the first interval is 100 ms, and step 3 measures it.
4. `media-operator` publishes the target on the Player's `volume`
   topic as soon as it moves, so the indicator moves at bus speed.
5. When the device reports the target, the target is no longer
   pending. When the device does not report it within one second, as
   when a receiver's own limit is below it, `media-operator` drops the
   target and publishes what the device reports.

A report that arrives while no target is pending is a change made at
the device: a turn of the receiver's knob, or a press of a Bluetooth
speaker's own button. `media-operator` publishes it, and the next
press steps from it.

A program that is not a remote asks for a step on the Player's
`players/{ns}/{name}/volume/commands` topic, with non-retained
messages:

```json
{"step": "up"}
{"step": "down"}
{"mute": "toggle"}
{"mute": true}
```

`media-operator` treats each message the same as a press.

### What the device operators do with an ask

`equipment-operator` sends a new `volumeAsk` to the receiver as an
absolute value, such as `MV445` to a Denon. When asks arrive faster
than the receiver takes them, it sends only the newest. It does not
send an ask it finds in its first pass after a start.

`audio-operator` applies a new `volumeAsk` to the sink's node or to a
Bluetooth speaker's route, the same writes it makes today for
`spec.volume`. The ask does not wait for the settle window. The
window (`settleWindow` and `settleLimit` in `audio-operator/main.go`)
exists so that a burst of control events produces one `ResourceSlice`
write. A volume write changes no `ResourceSlice`.

`audio-operator` also stops writing `spec.volume` back when the
device drifts from it. It applies the declared level when the spec
changes and when the device appears, and follows the device at all
other times. Without this change, `audio-operator` would undo each
ask on its next pass, and the sink would have two writers again.
`audio-operator/plans/completed/07-sinks-and-sources.md` states the
write-back rule that this reverses.

### What the CRDs declare

* `Receiver.spec.volume` keeps `max` and `step`, in the receiver's own
  scale. A Denon counts 0 to 98 in half steps.
* `Sink.spec.volume` changes from an integer to an object: `level`,
  the resting level that the integer holds today, plus `max` and
  `step`, all in percent. `step` defaults to 5 and `max` to 100, the
  values a press uses today. This breaks the `Sink` schema.
* An ask can never move the target above `max`. A person at the
  device can, as today.
* Each device reports its level in its own units:
  `Receiver.status.zones.main.volume` in the receiver's scale, and
  `Sink.status.observed.volume` in percent. `media-operator` divides
  the reported level by `max` to get the normalized level.

### `media-operator` chooses the devices

For each unit, `media-operator` chooses which devices set the level:

* The unit's `Receiver`, when one serves the unit and its `Reachable`
  condition is `True`.
* Otherwise, every `Sink` in the unit's `status.sinks`.

A `Receiver` that becomes unreachable is an event on the watch, and
the next press goes to the sinks. `media-operator` gets a watch on
`Sink` objects and RBAC to apply `sinks/status`. It already watches
`Receiver` objects.

When a unit has several sinks, each one steps in its own units and
reports its own level. The relay publishes the first sink in
`spec.sinks` order.

`mpv` always plays at unity. A unit with sinks always has
`audio-operator`, because the sinks are its devices, so no unit needs
a software gain in `mpv`.

### The screens draw the relay

The Player's `volume` topic keeps its name and its retained message.
Its payload changes to a normalized level:

```json
{"level": 0.63, "muted": false}
```

The screens draw it the way they draw today. A retained message that
the broker delivers at subscribe sets the level and draws nothing. A
live message draws the indicator. A turn of the receiver's knob is a
live message, so it shows the indicator too.

* The idle screen client in `media-screen` and the library browser
  read the topic and draw. Neither one handles a volume key any more.
* The playback pod's command sidecar reads the topic and sends the
  level to the display process in its `volume-changed` message. The
  display draws the bar from that level and not from `mpv`'s
  `volume` property. A unit with a receiver gets the indicator during
  a film, which it does not get today.

### Power, input, and the screen asks leave `equipment-operator`'s bus

* **Power.** The idle screen client and the playback pod keep
  publishing `toggle`, `on`, and `off` on the Player's `power` topic.
  `media-operator` reads the topic and writes each ask into
  `status.session.powerAsk`. `equipment-operator` applies it with the
  rules in `togglePower`, unchanged.
* **Input.** `media-operator` writes `inputAsk` where it publishes
  `input.ensure` and `input.show` on the receiver's commands topic
  today (`media-operator/ensure.go`). An `ensure` on each press would
  be one API write for each press, so `equipment-operator` adds the
  condition `InputSelected`, which is `True` while the receiver
  reports the session's input. `media-operator` writes an `ensure`
  only while the condition is not `True`. A `show` asks for the TV
  too, and a home press is rare, so `media-operator` writes each one.
* **The screen asks.** The CEC node workload writes
  `Television.status.screenAsk`, which names the `Player` and the
  screen. Today `equipment-operator` relays each new ask onto the
  power topic as `wake` or `sleep`. `media-operator` watches the
  `Television` objects and does that relay instead, with the same rule
  for an ask it finds in its first pass after a start: it relays
  nothing for it.
* **Settings.** `spec.settingsTopic` lets a program on the bus set one
  Denon or WiiM setting, and `equipment-operator` then writes the value
  into the spec. The spec is already the declaration of every setting,
  so a person or a program patches the spec instead. No program in this
  repository publishes on the settings topic.
* **WiiM actions.** The commands topic also carries `preset.recall`,
  `bluetooth.*`, and `reboot` for a WiiM (`wiim/commands.go`). No
  program in this repository sends them. They are removed with the
  topic, and "Outside this plan" names them.

### What is removed

* From `equipment-operator`: the MQTT client (`mqtt.go`, `bus.go`),
  `EQUIPMENT_BUS_ADDRESS`, the session's volume logic (`press`,
  `nextPosition`, `report`, `adopt`, the 0 to 100 mapping in
  `level.go`, and `session_target.go`), the owner mark and its Last
  Will, the power topic subscription and publish, the screen relay in
  `television_screen.go`, the unit's bus connection, and the metric
  `receiver_claimed`.
* From the `Receiver` CRD: `spec.settingsTopic`, `spec.commandsTopic`,
  and `volumeTopic` and `powerTopic` in both session blocks.
* From the bus: the `volume/owner` topic and the commands topic of
  each receiver.
* From `media-operator`: the volume seed, `volumeStep` in
  `keybindings.go`, the volume and mute rows of the sidecar's key
  table, `commandvolume.go`'s read-modify-write and owner handling,
  `--volume` and `--mute` on `mpv`, and the `volume`, `mute`, and
  `unmute` actions on a `Play`'s commands topic. The Player's
  `volume/commands` topic replaces the actions.
* From `media-screen`: `STEP` and the volume publish in
  `src/volume.rs` and `src/screen.rs`, and the volume keys in
  `keys::owned`.
* `Play.spec.volume`. It wrote a level to the whole unit, and nothing
  in the repository sets it. A person who wants a level writes the
  sink's `spec.volume.level`.

The delegate contract in the guide for handing the idle screen to
another controller changes in one release, with no compatibility
period: a delegate draws the relay and sends no level. The only two
clusters that run `liken` are ours.

### Alternatives considered

**A shared bus as a `liken` feature.** The first version of this plan
moved the broker into the OS as a feature, and gave every device
operator a state topic and a commands topic. The broker authorizes
nothing, so the OS would ship an open control plane for every
component that later uses it. It would also put an MQTT client into
three operators and copy the target logic into each one.

**The live level in spec.** `media-operator` could write a level into
`spec.volume` and let the device operator hold the device to it. The
device operator would then send the device back after each turn of the
knob, unless it ignored the spec between changes, and a spec field
that is ignored between changes is an ask in the wrong place. A held
key would also write the `metadata.generation` of an object that a
GitOps repository owns, several times a second.

**Presses through the sidecar.** The sidecar and the idle screen client
could keep handling the volume keys and publish steps for
`media-operator` to apply. That adds a bus hop and keeps volume code
in three programs. `media-operator` already reads every remote's
events for the input asks.

### What this costs

`media-operator` is in the path of every volume press, as it already is
for the input asks. From the lease settings in
`media-operator/leader.go`, a rollout leaves no leader for about 11 s
and a crash for 30 to 41 s. During that window the volume keys do
nothing. Playback, navigation, and the screens keep working, because
the pods and the broker do not depend on the leader.
`media-operator/plans/00-design.md` rejected routing every press
through the operator because "an operator restart would disconnect
every remote in the cluster". This plan routes only the volume, power,
and input asks, which already need a device operator to act.

A press also goes through the API server: one server-side apply and one
watch event before the device operator sends it. Nobody has measured
that path on our clusters. Step 3 measures it.

## The flow, by example

### A held key on a unit with a Denon

The receiver is at 45.5 in its own scale, its `max` is 72, and its
`step` is 0.5.

1. The person holds volume down. The remote's pod publishes
   `KEY_VOLUMEDOWN` events, about one every 42 ms.
2. `media-operator` reads the first event, moves the target from 45.5
   to 45.0, publishes `{"level": 0.625}` on the Player's `volume` topic,
   and applies `volumeAsk: {level: 45.0}` to the `Receiver` at once.
3. The next two events arrive inside the 100 ms pacing interval. The
   target moves to 44.5 and then 44.0, because it moves from the
   target and not from the receiver's report. The indicator follows
   each move. At the end of the interval, one apply writes 44.0.
4. `equipment-operator` reads each new `at` from its watch and sends
   `MV45` and then `MV44`.
5. The receiver reports 45.0 while the target is 44.0, so
   `media-operator` publishes nothing for that report. When the receiver
   reports 44.0, the target is no longer pending.

Ten events move the receiver 5 in its own scale, however slowly the
receiver answers.

### A turn of the receiver's knob

1. The person turns the knob up two steps. No target is pending.
2. The receiver reports 46.5. `equipment-operator` writes it into
   `status.zones.main.volume`.
3. `media-operator` reads the change from its watch and publishes
   `{"level": 0.646}` as a live message. The screen draws the
   indicator.
4. The next press steps from 46.5.

### A unit with a Bluetooth speaker

1. The person holds volume up. The unit has no `Receiver`, so
   `media-operator` steps the target of the `Sink` in `status.sinks`,
   5 percent for each step, and applies `volumeAsk` to its
   `status.session`.
2. `audio-operator` reads the new `at` and writes the level to the
   speaker's route. PipeWire sends it to the speaker over AVRCP
   absolute volume.
3. The person then presses the speaker's own volume button. The
   speaker reports the new level, `audio-operator` writes it into
   `status.observed.volume`, and it does not write any earlier level
   back. `media-operator` publishes it.

### The power button

1. The person presses power on the idle screen. The idle screen client
   publishes `{"action": "toggle"}` on the Player's `power` topic, as
   today.
2. `media-operator` applies `powerAsk: {action: toggle}` to the
   `Receiver`.
3. `equipment-operator` reads the new `at`, finds the room on from the
   TV's reported power, and puts the TV and the receiver in standby.

### A person picks the unit's input on the TV

1. The TV sends Set Stream Path for the unit's Display. The CEC node
   workload writes `Television.status.screenAsk` with the `Player` and
   `wake`.
2. `media-operator` reads the new ask from its watch and publishes
   `{"action": "wake"}` on the Player's `power` topic.
3. The idle screen client wakes the screen, as today.

### The receiver goes away

1. The receiver loses power, and `equipment-operator` sets `Reachable`
   to `False`.
2. `media-operator` reads the condition from its watch and chooses the
   unit's sinks.
3. The next press goes to the sinks.

### `equipment-operator` restarts during a held key

1. The new `equipment-operator` reads `status.session.volumeAsk` in
   its first pass and sends nothing for it, because the ask can be
   older than a turn of the knob.
2. `media-operator` sees no report of its target within one second,
   drops the target, and publishes what the receiver reports.
3. The next press steps from that report and makes a new ask.

### `media-operator` restarts during a film

1. The volume, power, and input keys do nothing until a new leader
   holds the lease.
2. The new leader lists the `Receiver`, `Sink`, and `Television`
   objects, holds no pending target, and publishes each unit's
   reported level on its `volume` topic.
3. The next press steps from the reported level.

### The broker restarts

The broker loses every retained message. `media-operator` publishes
each unit's level again when it reconnects. A press made while the
broker is down is lost, and the person presses again. The device
objects are not affected.

## The steps

Each step ships alone, leaves the system working, and has its own
proof.

### 1. The `Receiver` takes asks in `status.session`

`equipment-operator` applies `volumeAsk`, `powerAsk`, and `inputAsk`
when their `at` changes, and sets the `InputSelected` condition. The
CRD adds the three asks to both session blocks and makes `volumeTopic`
optional. A session with no `volumeTopic` sets no level from the bus,
publishes no owner mark, and applies only `volumeAsk`. The bus paths
keep running beside the asks, so nothing changes for a session that
names its topics.

Proof: unit tests with the fake Denon. A new `at` sends once. An ask in
the first pass sends nothing. Asks faster than the receiver answers
send only the newest. Then a drill on the testbed: apply 20
`volumeAsk` writes in one second with `kubectl apply --server-side
--subresource=status`, and the receiver goes down by 20 steps. The
drill also measures the time from the apply to the `MV` command in
the log.

### 2. The `Sink` takes asks in `status.session`

`audio-operator` applies `volumeAsk` without the settle window, writes
its status by server-side apply so that the session block survives its
writes, and stops writing `spec.volume.level` back. `Sink.spec.volume`
becomes `level`, `max`, and `step`. This step does not depend on step 1.

Proof: unit tests for the ask, for the end of the write-back, and for
a status write that leaves `status.session` in place. Then the drill
from step 1 against a Bluetooth speaker on the testbed, plus a press of
the speaker's own button, which `audio-operator` must report and not
undo.

### 3. `media-operator` sets the level

`media-operator` reads the volume keys and the Player's
`volume/commands` topic, holds the pending targets, writes
`volumeAsk`, and relays the normalized level. It stops naming
`volumeTopic` in the session, so `equipment-operator` stops reading the
bus for volume. The sidecar, `media-screen`, and the library browser
stop handling the volume keys and draw the new payload. `mpv` plays at
unity. This step depends on steps 1 and 2, and the components ship in
one release, because the payload changes for every reader at once.

Proof: hold the key on the testbed with a receiver and with a
Bluetooth speaker. `media-operator`'s log shows one target step for each
press and repeat and no step up. `managedFields` shows one writer for
each field in the table above. The drill measures the time from a
press to the device's report, which sets the pacing interval.

### 4. Power, input, and the screen asks go through the objects

`media-operator` writes `powerAsk` and `inputAsk`, and relays
`Television.status.screenAsk`. It stops naming `powerTopic` in the
session and stops publishing on the receiver's commands topic.

Proof: on the testbed, the power button turns the room off and on, a
press after a person changed the receiver's input brings the input
back, the home key shows the unit on the TV, and picking the unit's
input on the TV wakes the screen.

### 5. `equipment-operator` leaves the bus

Remove the MQTT client and everything else under "What is removed"
from `equipment-operator` and the `Receiver` CRD, with the
documentation that describes them.

Proof: the suites pass, nothing in `equipment-operator` imports the MQTT
client or names a topic, its `Deployment` has no
`EQUIPMENT_BUS_ADDRESS`, and the drills from steps 3 and 4 run again.

### 6. Remove the old media path

Remove the rest of "What is removed" from `media-operator`,
`media-screen`, and their documentation.

Proof: the suites pass, nothing names the owner topic, the seed, or
`Play.spec.volume`, and the drill from step 3 runs again.

## Outside this plan

* The `Sink` exclusivity open problem
  (`audio-operator/plans/open-problems/a-sink-can-be-shared-and-this-one-is-not.md`)
  needs a rewrite with what this plan found. Until it is settled, the
  idle pod does not claim sinks, so a unit that has never run a `Play`
  has an empty `status.sinks`. Today a press between films on such a
  unit sets the level that the first film starts at. Under this plan,
  that press sets nothing, and this is the one behavior the plan does
  not keep.
* The WiiM actions on the commands topic: `preset.recall`,
  `bluetooth.*`, and `reboot`. If a program needs them, they become an
  ask in the same block.
* Debounce or rate limits for a remote's repeats, in the `Remote`'s
  pod. Each repeat is one step under this design, so the repeat rate
  sets how fast the volume moves.
* CEC System Audio Mode, which would let a television's remote set the
  receiver's level (`equipment-operator/plans/completed/09-cec.md`).
* Equal loudness across devices. A Denon's scale is in dB and
  PipeWire's gain is linear, so two devices at the same `level` do
  not sound equally loud.
* Access control on the bus
  (`media-operator/plans/open-problems/the-broker-is-not-ready-to-share.md`).
  After this plan, a program on the bus can still ask for a step, a
  mute, or a power change through `media-operator`, but it can no
  longer drive a receiver's settings or send it a level directly.

## After the plan

The drills on `liken-1` used the `lab-portable` speakers and a fake
Denon that answers the driver's protocol with a 60 ms echo. The build
and the drills found three problems that the steps above did not name:

* A suspended PipeWire node keeps printing the level it last ran at,
  so an ask on an idle sink snapped back to 100 about 1.5 s after a
  press. `audio-operator` now reports the level it last wrote to a
  suspended node (`ce68d684`).
* The idle pod and the library browser got the power topic only while
  a `Receiver` was wired, so wiring or removing one replaced the pod
  and blanked the screen for about 18 s. The topic is now always set,
  and the Player's bus status carries `power: room` or
  `power: screen` (`3e18d368`).
* A WiiM `Receiver` can declare no `spec.volume.max`, so a review of
  step 3 made `media-operator` use the driver's own top of 100 for it,
  as the receiver session did before.

`2026.10.04-004` added `spec.volume.indicator` to the `Receiver`. With
`Receiver`, the receiver draws its own volume overlay on the TV, and
the screens track the level without drawing their bar (`ab06ea45`).
