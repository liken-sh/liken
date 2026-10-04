# 77, Each device owns its volume

Proposed on 2026-10-04. A remote's volume key becomes a command that
goes to the device that sets the room's level, and that device's
operator becomes the only program that writes the level. The work
touches `liken`, `media-operator`, `media-screen`, `equipment-operator`,
and `audio-operator`.

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

### Why this is a design flaw

The `players/{namespace}/{name}/volume` topic is one retained value
with two writers, and the two readers give it two meanings. The pod
treats it as state and adds to it. The receiver session treats each
change as a command and reads only its direction.
`equipment-operator/plans/00-design.md` names the exception that
allows this: "One rule bends. The volume topic's rule is that no
observer writes back what it saw." All three failures come from that
exception, so a fix inside the current design would keep the cause.

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

Each device that can set a room's level is the only owner of that
level. Its operator takes commands, applies them in the device's own
units, and publishes what the device reports. Everything else sends
commands and draws the published state.

### The bus is a `liken` feature

Today `media-operator` deploys the MQTT broker as its own `bus`
`Deployment`, and `equipment-operator` connects to it to write into the
media tree. Under this design, `audio-operator` connects too, so the
broker is shared infrastructure, not part of the media domain.

The broker becomes the optional `liken` feature `bus`, of the kind
`FeatureWorkload`, the kind that `flux` uses
(`liken/cluster/features.go`). A cluster that declares the feature
gets one broker at one stable address. Each operator owns its own
topic root: `liken/media`, `liken/equipment`, and `liken/audio`. No
operator writes under another operator's root.

A component that installs into a cluster with no `bus` feature runs
without the bus, as it does today when the broker is not deployed.

The two open problems about the broker move from
`media-operator/plans/open-problems/` to `liken/plans/open-problems/`:
`the-broker-is-not-configurable.md` and `the-bus-authorizes-nothing.md`.

### Each device publishes its volume and takes commands

A `Receiver` and a `Sink` each get two topics:

| Topic | Writer | Retained | Payload |
|---|---|---|---|
| `liken/equipment/receivers/{name}/volume` | `equipment-operator` | yes | the receiver's state |
| `liken/equipment/receivers/{name}/volume/commands` | any client | no | one command |
| `liken/audio/sinks/{name}/volume` | `audio-operator` | yes | the sink's state |
| `liken/audio/sinks/{name}/volume/commands` | any client | no | one command |

A command is a direction or a mute change, never a level:

```json
{"step": "up"}
{"step": "down"}
{"mute": "toggle"}
{"mute": true}
```

A command can also name its source, such as
`"source": "remote/media/den-remote"`, so that the operator's
log line can say which control asked.

The state is the device's report, normalized and in the device's own
units:

```json
{"level": 0.63, "value": -34.5, "unit": "dB", "muted": false}
```

* `level` is the device's volume as a fraction of its `max`, from 0.0
  to 1.0. A screen draws its bar from this field.
* `value` and `unit` are the volume as the device states it. A Denon
  states dB, and a WiiM or a `Sink` states a percentage. A screen can
  label the bar with them.
* `muted` is the device's own mute.

The units are in the state message and not in separate metadata,
because a device's units never change while it runs.

### How a device operator applies commands

The operator holds a **target** in the device's own units. The target
starts at the device's reported volume.

1. A `step` command moves the target one `step`, up or down, and
   holds it between 0 and `max`.
2. The operator sends the target to the device as an absolute value,
   such as `MV445` to a Denon. The sends are paced, so a burst of
   commands produces one send for each pacing interval, and the last
   target wins.
3. Each report from the device updates the published state. A report
   does not move the target while sends are pending, because the
   report can be older than the last send.
4. When the device reports the target, the operator drops the target.

A report that arrives while no target is pending is a change made at
the device: a turn of the receiver's knob, or a press of a Bluetooth
speaker's own button. The operator publishes it, and the next command
steps from it. The operator never sends the device back to an earlier
level.

The Denon protocol document gives no command spacing that we could
confirm. A search summary quotes "50 ms or more" between commands, so
the first pacing interval for a Denon is 50 ms, and step 2 measures it.

### What the CRDs declare

The CRDs keep declarations that a person writes once. They do not
carry the live level.

* `Receiver.spec.volume` keeps `max` and `step`, in the receiver's own
  scale.
* `Sink.spec.volume` gets the same shape: `max`, `step`, and the
  resting `level` that the integer `spec.volume` holds today. This
  breaks the `Sink` schema.
* A command can never move the target above `max`. A person at the
  device can, as today.
* `audio-operator` stops writing the declared level back when the
  device drifts from it. It applies the declared level when the spec
  changes and when the device reconnects, and it follows the device
  at all other times. `audio-operator/plans/completed/07-sinks-and-sources.md`
  states the write-back rule that this reverses.

Two facts ruled out driving the live level through `spec`. First,
`audio-operator` waits for 1.5 s of quiet before it reconciles, and up
to 10 s during a burst (`settleWindow` and `settleLimit` in
`audio-operator/main.go`). A held key would move a speaker once every
10 s. Second, a held key would write etcd several times a second,
into a field that a person or a GitOps repository owns.

### `media-operator` chooses the device

`media-operator` stays in the control plane. For each unit, it chooses
which devices set the level:

* The unit's `Receiver`, when one serves the unit and is `Reachable`.
* Otherwise, every `Sink` in the unit's `status.sinks`.

It writes the chosen devices' topics into the `Player`'s status, into
the environment of the playback pod, and into `status.idle.bus`. A
`Receiver` that becomes unreachable is a control-plane event: the
operator chooses the `Sink`s, and the pods receive the new topics.

A press never goes through `media-operator`.
`media-operator/plans/00-design.md` rejected that path because "an
operator restart would disconnect every remote in the cluster". From
the lease settings in `media-operator/leader.go`, a rollout leaves no
leader for about 11 s and a crash for 30 to 41 s.

### Pods send commands and draw state

The pod that handles a key sends one command for each press and for
each repeat to every chosen device. That pod is the playback pod's
command sidecar during a film, and the idle client between films. The
pod never reads a level before it sends.

A screen draws the volume indicator when a state message arrives live.
A retained message that the broker delivers at subscribe sets the
level and draws nothing. A turn of the receiver's knob is a live
message, so it shows the indicator too.

When a unit has several `Sink`s, each one steps in its own units and
publishes its own `level`. The indicator draws the first `Sink` in
`spec.sinks` order.

`mpv` always plays at unity. A unit with sinks always has
`audio-operator`, because the sinks are its devices, so no unit needs
a software gain in `mpv`.

### What is removed

* The `volume` and `volume/owner` topics under `liken/media/players`,
  the owner mark, and its Last Will.
* The receiver session's volume logic in `equipment-operator`:
  `press`, `nextPosition`, `report`, `adopt`, and the 0 to 100
  mapping in `level.go`.
* The read-modify-write in the sidecar and in `media-screen`.
* `volumeStep` in `media-operator/keybindings.go` and `STEP` in
  `media-screen/src/volume.rs`. The device's `step` replaces both.
* `amount` on the `volume` action, `--volume` and `--mute` on `mpv`,
  and the volume seed in `media-operator`.
* `Play.spec.volume`. It wrote a level to the whole unit, and nothing
  in the repository sets it. A person who wants a level writes it to
  the device's spec.

The delegate contract in the guide for handing the idle screen to
another controller changes in one release, with no compatibility
period. The only two clusters that run `liken` are ours.

## The flow, by example

### A held key on a unit with a Denon

The receiver is at -34.5 dB, its `max` is 72 on its own scale, and its
`step` is 0.5.

1. The person holds volume down. The remote's pod publishes
   `KEY_VOLUMEDOWN` events, about one every 42 ms.
2. The playback pod's sidecar sends `{"step": "down"}` to
   `liken/equipment/receivers/den/volume/commands` for each
   event.
3. `equipment-operator` moves its target from -34.5 to -35.0 and
   sends it. The next command, 42 ms later, moves the target to -35.5,
   because the target moves from the target and not from the last
   report.
4. Ten commands move the receiver 5 dB, however slowly the receiver
   answers.
5. Each report updates `liken/equipment/receivers/den/volume`,
   and the screen draws the bar from `level`.

### A turn of the receiver's knob

1. The person turns the knob up two steps. No target is pending.
2. The receiver reports -33.5 dB. `equipment-operator` publishes it.
3. The screen draws the indicator, because the message is live.
4. The next press on the remote steps from -33.5.

### A unit with a Bluetooth speaker

1. The person holds volume up. The sidecar sends `{"step": "up"}` to
   the `Sink`'s commands topic.
2. `audio-operator` moves its target one `step` in percent and writes
   it to the speaker's route. PipeWire sends it to the speaker over
   AVRCP absolute volume.
3. The person then presses the speaker's own volume button. The
   speaker reports the new level, `audio-operator` publishes it, and it
   does not write the old level back.

### The receiver goes away

1. The receiver loses power, and `equipment-operator` reports it
   unreachable.
2. `media-operator` chooses the unit's `Sink`s and writes their topics
   into the pod's environment and `status.idle.bus`.
3. The next press goes to the `Sink`.

### `media-operator` restarts during a film

Nothing changes for volume. The sidecar already holds the device's
topics, and the device operators are separate programs, so a press
still reaches the receiver.

### The broker restarts

The broker loses every retained message. Each device operator
publishes its state again when it reconnects. A command sent while the
broker is down is lost, and the person presses again.

## The steps

Each step ships alone, leaves the system working, and has its own
proof.

### 1. The bus becomes a `liken` feature

Move the broker from `media-operator/deploy/bus.yaml` into a `bus`
feature of kind `FeatureWorkload`. Point `media-operator` and
`equipment-operator` at the feature's address. Move the two open
problems about the broker into `liken/plans/open-problems/`.

Proof: declare the feature on the testbed. The broker starts, and
remotes, playback, and the receiver session work as before.

### 2. The `Receiver` publishes its volume and takes commands

`equipment-operator` publishes the state topic and applies commands
with a target and pacing, as described above. The old session keeps
running beside it, so nothing that uses the media `volume` topic
changes yet.

Proof: unit tests with a fake Denon that answers slowly. Every command
inside one round trip moves the target. A report with no target
pending is followed. No report changes the target while a send is
pending. Then a drill: send 20 `down` commands in one second with
`mosquitto_pub`, and the receiver goes down 10 dB. The drill also
measures the shortest pacing interval at which the Denon applies
every send.

### 3. The `Sink` publishes its volume and takes commands

`audio-operator` gets a bus client, publishes the state topic, and
applies commands. The declared level applies only when the spec
changes and when the device reconnects. The `Sink` gets `max`, `step`,
and `level` under `spec.volume`. This step does not depend on step 2.

Proof: unit tests for the target and for the end of the write-back.
Then the drill from step 2 against a Bluetooth speaker on the testbed,
plus a press of the speaker's own button, which the operator must
publish and not undo.

### 4. `media-operator` chooses the device

`media-operator` writes the chosen devices' topics into the `Player`'s
status, the pod environment, and `status.idle.bus`. The pods do not
read the new fields yet.

Proof: the `Player`'s status names the right topics for a unit with a
receiver and for a unit with sinks only. When the receiver goes
unreachable, the status names the sinks.

### 5. The pods send commands

The sidecar and `media-screen` send commands to the chosen devices and
draw the state they publish.

Proof: hold the key on the testbed. The device operator's log shows
one command for each press and each repeat, one step of the target for
each command, and no step up.

### 6. Remove the old path

Remove everything in "What is removed", with the documentation that
describes it.

Proof: the suites pass, nothing in the code names the removed topics
or fields, and the drill from step 5 runs again.

## Outside this plan

* The `Sink` exclusivity open problem
  (`audio-operator/plans/open-problems/a-sink-can-be-shared-and-this-one-is-not.md`)
  needs a rewrite with what this plan found. Until it is settled, the
  idle pod does not claim sinks, so a unit that has never run a `Play`
  has an empty `status.sinks`. A unit with no `Receiver` then has no
  device for its volume keys until its first `Play`.
* Debounce or rate limits for a remote's repeats, in the `Remote`'s
  pod. Each repeat is one step under this design, so the repeat rate
  sets how fast the volume moves.
* CEC System Audio Mode, which would let a television's remote set the
  receiver's level (`equipment-operator/plans/09-cec.md`).
* Equal loudness across devices. A Denon's scale is in dB and
  PipeWire's gain is linear, so two devices at the same `level` do
  not sound equally loud.
