# 18, System Audio Mode on the wake

Proposed on 2026-10-06. Replaces the CEC part of
[plan 13](rejected/13-every-setting-a-receiver-exposes.md#cec), and
builds phase 4 of [plan 09](completed/09-cec.md). Not built.

## The problem

A room's receiver is the audio system of its CEC bus, at logical
address 5. While System Audio Mode is on, the TV mutes its own
speakers and sends the volume keys of its remote to the receiver
(HDMI-CEC 1.3a, CEC 13.15.2). While it is off, the TV remote's volume
keys change the TV's own volume, which the room does not hear when
the sound plays on the receiver. A person who wakes the room with a
`liken` remote then has to pick the receiver in the TV's speaker menu.

The bus does not report the mode, and the operator never asks for it:

* The join scan asks the audio system for its power once
  (`cec/scan.go:59`). After that, only a Report Power Status sent to
  the adapter changes it (`cec/directory.go:197`), and the inference
  from heard messages covers the TV alone (`cec/directory.go:150`). So
  the receiver's power in `status.devices` is the power at the scan.
* A broadcast Set System Audio Mode writes a log line
  (`cec/describe.go:24`, `cecnode_heard.go:42`), and no field holds it.
  Nothing sends Give System Audio Mode Status or Give Audio Status.
* Plan 09's `CECBus` example gives each device a `represents` field,
  and `CECDevice` has none (`cecbus.go:118`). No object says which
  `Receiver` is the bus's audio system.
* The node workload's role reaches `CECBus`, `Television`, and
  `Display` objects, and no `Receiver` (`deploy/rbac.yaml:157`).

## System Audio Mode is not a setting

A setting is a value the operator converges to
([plan 14](14-one-settings-model-for-every-receiver.md)). System Audio
Mode is a state that the TV, the receiver, and the person all change,
and a request for it changes more than the mode:

* **The request switches the receiver's input.** System Audio Mode
  Request carries the physical address of the source to play. The
  amplifier comes out of standby if necessary, switches to the input for
  that address, and answers with Set System Audio Mode [On]
  (CEC 13.15.2, CEC Table 22).
* **The receiver turns it off.** It broadcasts Set System Audio Mode
  [Off] just before standby, and it comes out of standby with the mode
  off unless a request woke it (CEC 13.15.2).
* **The TV and the receiver change it on their own.** The TV ends the
  mode with a request that has no operand (CEC 13.15.2). A WiiM has a
  setting "Activate TV Speaker When HDMI Is Switched Away"
  ([WiiM FAQ](https://faq.wiimhome.com/en/support/solutions/articles/72000637914-introduction-to-wiim-devices-hdmi-cec-behaviors)).
  Sony documents that the receiver may not turn on with the TV when the
  TV's last sound output was its own speakers
  ([Sony help guide](https://helpguide.sony.net/ha/strdh79/v1/en/contents/TP0001553128.html)).

So a request sent again on drift would switch the receiver's input
and wake it against a person who picked the TV's speakers. It would
also be traffic on a timer, which made a TV switch its own input in
plan 09 ("The adapter sends nothing on a timer"). The block declares a
policy for the wake instead: when a person wakes the room, ask for the
mode with the room's input. The status reports the mode the bus
carries. The policy is outside plan 14's four tiers, because it is a
step of an action the person asked for, not a value to hold.

## The design

### The block

```yaml
spec:
  denon:
    address: receiver.example
  cec:
    bus: den
    systemAudioMode: OnWake
```

`bus` names the `CECBus`, which has at most one audio system.
`systemAudioMode` is an enum with one value, `OnWake`, so a later value
can join it, as plan 09 chose for `spec.mode`. When the field is
absent, the operator never sends System Audio Mode Request. `cec:` is
not a network block: the CEL rule that requires exactly one of
`denon:` and `wiim:` stays (`deploy/receivers-crd.yaml:80`).

### The network block owns power, input, and volume

The network block reads and sets the receiver's power, input, volume,
and mute, and `cec:` sends none of them. The request does switch the
input and can wake the receiver, so the wake sends it only where it
agrees with the network block. Its operand is the physical address of
the session's `Display`, the input the session already selects over
the network. It goes out during a wake, the same moment the network
block turns the receiver on.

The receiver accepts CEC only while its HDMI control is on. That switch
is the common `status.settings.hdmi.control` of plan 14, and `false`
gives the reason `ControlOff` below. The operator never turns the
switch on to make the request land; a person declares
`spec.settings.hdmi.control`.

### The policy reaches the node workload through the `Television`

The `Deployment` already writes `Television.status.session` for each
wake (plan 09, "What phase 3 found"). It adds one field:

```yaml
status:
  session:
    player: media/den
    display: acm-0001-receiver
    awake: true
    wokeAt: "2026-10-06T18:04:05.123Z"
    systemAudioMode: OnWake
```

The `Deployment` writes the field when the `Receiver` that names the
bus declares `OnWake`, its `SystemAudioReady` condition is `True`, and
it is the `via` of the session's `Display` in `status.displays`. So the
node workload needs no permission on `Receiver` objects, and it reads
the policy from the same object and the same watch as the wake.

### The request

The node workload sends the request in `runWake`
(`cecnode_wake.go:386`), from the adapter that speaks for the session's
`Display`:

* once for each `wokeAt`, after the wake's first Active Source;
* to logical address 5, with the `Display`'s physical address as the
  operand;
* never with `0.0.0.0`, which asks the receiver for the TV's own sound
  and takes the input away from the `Display`;
* never with no operand, which ends the mode for the TV's speakers;
* never on a timer, never for a home press, and never twice in a wake.

The amplifier directs its first [On] to the TV, and broadcasts it only
when the TV does not Feature Abort it (CEC 13.15.2), so an adapter in
`Control` can miss the first [On]. After the request, the adapter waits
5 seconds for a broadcast [On]. With none, it sends one Give System
Audio Mode Status to address 5. `SystemAudioApplied` on the
`Television`, which the node workload writes beside `WakeApplied`,
states the result:

* `True`, `Confirmed`: a broadcast [On] or a status read of On.
* `False`, `Unconfirmed`: the read said Off, or the receiver did not
  answer. A TV that does not implement the feature Feature Aborts the
  amplifier's [On], and the amplifier then stops (CEC 13.15.2).
* `False`, `Refused`: the receiver Feature Aborted the request, or the
  adapter could not send it.

### `status.cec`

```yaml
status:
  cec:
    bus: den
    logicalAddress: 5
    physicalAddress: 1.0.0.0
    osdName: AVR
    vendor: 0005cd
    cecVersion: "1.4"
    systemAudioMode: "On"
    systemAudioModeAt: "2026-10-06T18:04:07Z"
  conditions:
  - type: SystemAudioReady
```

The `Deployment` derives `status.cec` from the device at logical
address 5 in the bus's merged `status.devices`, and each field is
Reported. `systemAudioMode` is `On` or `Off`, and absent while no
adapter holds a value. `systemAudioModeAt` is when an adapter last read
or heard it. `status.cec` carries no power and no volume: the network
block reports both, and the bus's power for the receiver is the power
at the scan.

The node workload keeps the mode current by the repository's rule for
state. The follower handle is the subscription. The baseline is one
Give System Audio Mode Status to address 5 in the join scan, after the
other questions to that device. After that, each heard broadcast Set
System Audio Mode sets the mode, and so does the status read after a
request. Each adapter writes the mode on the device in its own entry,
in a new `CECDevice.systemAudioMode` field.

`SystemAudioReady` belongs to the `Deployment`. It is `True` when the
bus is in `Control`, an audio system is on it, and `represents` names
this `Receiver`. It is `False` with the reason `NotOnBus` when no device
holds logical address 5, `ControlOff` when
`status.settings.hdmi.control` is `false`, and `NotInCharge` when
another `Receiver` names the same bus and comes first by name.

### `represents`

`CECDevice` gains `represents`, a kind and a name. The `Deployment`
sets it when it merges the devices: the `Television` in charge for
logical address 0, and for logical address 5 the first `Receiver` by
name whose `spec.cec.bus` names the bus. Plan 09's example shows this
link, and the code never built it. `receiverFeeding`
(`television_derive.go:225`) keeps its rule for `via`, because a
`Receiver` with no `cec:` block can still carry the picture.

### The conformance gap in Table 24

`Answer` treats Set System Audio Mode and System Audio Mode Status as
answers and never sends a Feature Abort for them
(`cec/follower.go:63`). HDMI-CEC 1.3a, CEC Table 24, says that a device
that does not Feature Abort Set System Audio Mode shall be able to send
System Audio Mode Request and User Control Pressed for Volume Up, Volume
Down, and Mute. CEC Table 25 says that a device that ever sends the
request shall be able to send those keys, and shall not Feature Abort
the two messages. The adapter can send neither today, so it does not
conform.

1. **Feature Abort both while no policy applies.** `Answer` takes a
   flag that is true while the `Television` session carries
   `systemAudioMode`. Without the flag, the adapter does not support
   the feature, which conforms. With it, the adapter meets Table 25 for
   the two messages.
2. **Build the volume keys in `cec/`.** A sender of User Control
   Pressed and Released to address 5. Nothing would call it, because
   the network block owns volume.
3. **Keep `Answer` as it is**, and record the gap in `cec/AGENTS.md`.

The recommendation is option 1 now. It closes the gap on every bus with
no policy. On a bus with `OnWake`, the duty to be able to send the
volume keys remains. `cec-compliance` checks System Audio messages only
on TVs and audio systems
([`cec-test-audio.cpp`](https://github.com/gjasny/v4l-utils/blob/master/utils/cec-compliance/cec-test-audio.cpp)),
so no tool checks this duty. Option 2 closes it if a drill finds a TV
that sends volume keys to the adapter.

## Corrections to plan 13's CEC section

* Plan 13 made System Audio Mode a setting. It is a request that
  switches the receiver's input, so it is a step of the wake.
* "Makes the TV send its sound to the receiver" is wrong. The mode
  mutes the TV's speakers and sends its volume keys to the receiver
  (CEC 13.15.2). ARC or an optical cable carries the TV's sound, and
  ARC is in HDMI 1.4, not in 1.3a.
* "Only CEC reaches it" is half true. HDMI control, ARC, and Denon's TV
  Audio Switching are its preconditions, and the network reaches each.
* "The receiver does not originate CEC" is wrong as written. An
  amplifier sends Set System Audio Mode and Request Active Source
  (CEC 13.15.2), and plan 09 recorded the receiver sending Routing
  Information. It sends no Image View On, so it cannot wake the TV.
* `status.cec.power` from the bus is the power at the scan, and volume
  over CEC is relative: Set Audio Volume Level is a CEC 2.0 message
  (`CEC_MSG_SET_AUDIO_VOLUME_LEVEL` in `linux/cec.h`).
* The node workload has no permission on `Receiver` objects.

## Considered and set aside

* **A `Receiver` with only `cec:`.** Every `Receiver` keeps exactly one
  network block (plan 14). CEC could turn the receiver on with the
  request and off with Standby, but it sets volume only by key presses,
  checked against Report Audio Status as a percentage whose linearity
  the device decides (CEC Table 26).
* **System Audio Mode as a Confirmed setting.** A send on drift would
  switch the receiver's input and fight the TV's speaker menu.
* **The operand `0.0.0.0`.** It asks for the TV's own sound, which the
  TV's speaker menu decides. A playback device may send the request
  for its own address: libCEC's `AudioEnable` does
  ([`CECAudioSystem.cpp`](https://github.com/Pulse-Eight/libcec/blob/master/src/libcec/devices/CECAudioSystem.cpp)),
  and [libCEC PR 208](https://github.com/Pulse-Eight/libcec/pull/208)
  uses it to wake receivers.
* **ARC, Short Audio Descriptors, and audio status.** ARC and the
  descriptor requests pass between the TV and the audio system only,
  and `Answer` Feature Aborts the requests. Report Audio Status is
  directed only (CEC Table 22), so an adapter in `Control` does not
  hear the reports to the TV.

## Phases

1. `represents`, `status.cec` with the mode from the scan and from heard
   broadcasts, `SystemAudioReady`, and option 1 for Table 24.
2. The policy: the `Television` session field, the request in the wake,
   and `SystemAudioApplied`.

## How it will be proved

The fakes in `cec/cectest` and `kubernetes/apiservertest` prove each
path, and `vivid` proves the messages against the kernel. Six drills
run on a home cluster with an AVR-X1700H, then a WiiM Amp. None has
run.

1. In `Listen`, record each System Audio message, its sender, and
   whether it is directed or broadcast: when the TV's speaker menu
   picks the receiver, when the receiver is turned on at its front
   panel, and when it goes to standby.
2. Send Give System Audio Mode Status to address 5 with the receiver on
   and in standby, and record the answers.
3. With the receiver on the session's input, send the request with the
   `Display`'s address. Check that the TV mutes, and count the TV's
   Routing Changes for 20 minutes after.
4. Send the request to the receiver in standby, with the Denon's TV
   Audio Switching on and then off. Record whether it wakes, and which
   input it selects.
5. After a request, set the TV's speaker menu to its own speakers.
   Check that the TV ends the mode and that the operator sends nothing.
6. Check whether the WiiM Amp claims logical address 5. WiiM documents
   its CEC behaviors for the WiiM Ultra only.
