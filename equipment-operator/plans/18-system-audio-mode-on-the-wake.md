# 18, System Audio Mode on the wake

Proposed on 2026-10-06, and revised on 2026-10-28 to send the request
on every wake through a receiver, with no field in the spec. Replaces
the CEC part of
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
  off unless a request woke it (CEC 13.15.2). The operator wakes the
  receiver over the network, so today the mode is off after every wake.
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
plan 09 ("The adapter sends nothing on a timer"). The request is a
step of the wake instead: when a person wakes the room through a
receiver, the adapter asks for the mode with the room's input once.
The status reports the mode the bus carries.

## The design

### When the wake sends the request

The wake sends the request whenever the session's `Display` has a
receiver on its HDMI path, and never otherwise. No field in the spec
turns it on or off. A wake through a receiver is a person asking to
play through that receiver: the network block already turns the
receiver on and selects the `Display`'s input, and a receiver that a
request wakes comes up with the mode on (CEC 13.15.2). The request
makes the TV agree with what the network block has already done.

The `Deployment` already decides which receiver is on the path.
`receiverFeeding` (`television_derive.go:225`) writes the `via` of
each `Display` in the `Television`'s `status.displays` when two facts
hold: an audio system on the bus has a physical address above the
`Display`'s in the HDMI tree, and a `Receiver` has an input that names
the `Display`'s machine and monitor. That is the condition for the
request, so the node workload reads `via` from the `Television` it
already watches and needs no permission on `Receiver` objects.

A `Display` with no `via` gets no request:

* A receiver connected to the TV by optical alone is not on the CEC
  bus, because an optical cable carries no CEC. No device holds
  logical address 5, and there is no mode to request. The TV's own
  sound-output setting sends its sound to the receiver.
* A receiver on HDMI ARC behind the TV is on the bus, but the
  `Display` is not below it in the tree, so `via` stays empty. Its
  request would carry `0.0.0.0`, which CEC 13.15.2 maps to the
  receiver's connector for the TV's sound. That case is set aside
  below.

### The network block owns power, input, and volume

The network block reads and sets the receiver's power, input, volume,
and mute, and the CEC side sends none of them except through the
request. The request switches the input and can wake the receiver, so
its operand is the physical address of the session's `Display`, the
input that the network block already selects. It goes out during the
wake, the same moment the network block turns the receiver on.

The receiver maps the operand to an input through its own CEC table,
which is set when its HDMI inputs are assigned. A receiver whose table
maps the `Display`'s address to another input than the network block
selected would switch the room away from the `Display`. Drill 3 checks
that the two agree. If a drill finds a receiver whose table disagrees,
a field to turn the request off is added then, for that case.

The receiver accepts CEC only while its HDMI control is on. A receiver
with HDMI control off does not answer the request, and the result is
`Unconfirmed`. The operator never turns HDMI control on to make the
request land.

### The request

The node workload sends the request in `runWake`
(`cecnode_wake.go:394`), from the adapter that speaks for the session's
`Display`:

* once for each `wokeAt`, after the wake's first Active Source;
* to logical address 5, with the `Display`'s physical address as the
  operand;
* never with `0.0.0.0`, which asks the receiver for the TV's own sound
  and takes the input away from the `Display`;
* never with no operand, which ends the mode for the TV's speakers;
* never on a timer, never for a home press, and never twice in a wake.

A TV that implements the feature asks for the mode's status when it
comes out of standby (CEC 13.15.2), and a TV can also send its own
request as it comes on, which can move the receiver to the TV's sound
input. So the request goes out after the TV's own traffic, under the
same guard that holds the active source through the TV's own switch
as it comes on (`cecnode_source.go`). Drill 1 records what a TV sends
in its first seconds on.

The amplifier directs its first [On] to the TV, and broadcasts it only
when the TV does not Feature Abort it (CEC 13.15.2), so an adapter in
`Control` can miss the first [On]. After the request, the adapter waits
5 seconds for a broadcast [On]. With none, it sends one Give System
Audio Mode Status to address 5. A broadcast [On] that arrives before
the request is the answer to the TV's own request, and does not count
for this one. `SystemAudioApplied` on the `Television`, which the node
workload writes beside `WakeApplied`, states the result:

* `True`, `Confirmed`: a broadcast [On] after the request, or a status
  read of On.
* `False`, `Unconfirmed`: the read said Off, or the receiver did not
  answer. A TV that does not implement the feature Feature Aborts the
  amplifier's [On], and the amplifier then stops (CEC 13.15.2).
* `False`, `Refused`: the receiver Feature Aborted the request, or the
  adapter could not send it.

The condition is absent for a wake whose `Display` has no `via`.

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
```

The `Deployment` writes `status.cec` on the `Receiver` that is the
`via` of a `Display` on the bus, from the device at logical address 5
in the bus's merged `status.devices`. Each field is Reported.
`systemAudioMode` is `On` or `Off`, and absent while no adapter holds a
value. `systemAudioModeAt` is when an adapter last read or heard it.
`status.cec` carries no power and no volume: the network block reports
both, and the bus's power for the receiver is the power at the scan.

`Off` is an ordinary state, not a fault. The receiver turns the mode
off before standby, and a person turns it off when they pick the TV's
speakers. So `Off` posts no Warning, and the `Event` of a change is
Normal.

The node workload keeps the mode current by the repository's rule for
state. The follower handle is the subscription. The baseline is one
Give System Audio Mode Status to address 5 in the join scan, after the
other questions to that device, which is also what CEC 13.15.2 asks of
a source that implements the feature when it joins the bus. After
that, each heard broadcast Set System Audio Mode sets the mode, and so
does the status read after a request. Each adapter writes the mode on
the device in its own entry, in a new `CECDevice.systemAudioMode`
field.

While the mode is on, the TV sends its remote's volume keys to the
receiver over CEC, and the `liken` remote sets the volume over the
network. The receiver holds the one value, and the network block
reports it, so `status` shows volume changes that `liken` did not make.
The manual says so.

### Conformance: Tables 24 and 25

`Answer` treats Set System Audio Mode and System Audio Mode Status as
answers and never sends a Feature Abort for them
(`cec/follower.go:63`). HDMI-CEC 1.3a, CEC Table 24, says that a device
that does not Feature Abort Set System Audio Mode shall be able to send
System Audio Mode Request and User Control Pressed for Volume Up, Volume
Down, and Mute, with User Control Released. CEC Table 25 says that a
device that ever sends the request shall be able to send those keys,
and shall not Feature Abort the two messages. The adapter can send
neither today, so it does not conform.

Every adapter sends the request on a bus with a receiver on the path,
so the duty of Table 25 applies wherever the request can go out:

1. **Build the volume keys in `cec/`.** A sender of User Control
   Pressed and Released to address 5, for Volume Up, Volume Down, and
   Mute. The network block still owns volume, so nothing in the
   operator calls the sender on a wake. It is the capability that
   Table 25 requires, and it is tested against `cectest` and `vivid`.
2. **Feature Abort both messages on a bus with no audio system.** With
   no device at logical address 5, the adapter never sends the
   request, so it does not support the feature, and a directed Set
   System Audio Mode or System Audio Mode Status gets a Feature Abort.
   A broadcast gets none, because a follower ignores a broadcast it
   does not support (CEC 12.3). The receiver
   broadcasts every [On] and [Off] after its first, and directs the
   first to the TV, so this changes little on the wire.

The CEC also restricts the feature to an amplifier and to devices that
have volume control buttons, such as a TV or a set-top box
(CEC 13.15.2). A `liken` machine has them on its remote.
`cec-compliance` checks System Audio messages only on TVs and audio
systems
([`cec-test-audio.cpp`](https://github.com/gjasny/v4l-utils/blob/master/utils/cec-compliance/cec-test-audio.cpp)),
so no tool checks this duty.

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

* **A field that turns the request on.** An earlier draft added
  `spec.cec.bus` and `spec.cec.systemAudioMode: OnWake`, with a
  `represents` link from the bus's device to the `Receiver`. The `via`
  of a `Display` already names the receiver on the path, and a wake
  through a receiver is the person asking for it, so the request needs
  no declaration.
* **A receiver on HDMI ARC behind the TV.** Its request would carry
  `0.0.0.0`, which CEC 13.15.2 maps to the receiver's connector for the
  TV's sound. `via` is empty for such a `Display`, and no receiver in
  the test equipment is on ARC behind the TV, so it waits for one.
* **A `Receiver` with only `cec:`.** Every `Receiver` keeps exactly one
  network block (plan 14). CEC could turn the receiver on with the
  request and off with Standby, but it sets volume only by key presses,
  checked against Report Audio Status as a percentage whose linearity
  the device decides (CEC Table 26).
* **System Audio Mode as a Confirmed setting.** A send on drift would
  switch the receiver's input and fight the TV's speaker menu.
* **The operand `0.0.0.0` for a receiver on the path.** It asks for the
  TV's own sound, which the TV's speaker menu decides. A playback device may send the request
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

1. `status.cec` with the mode from the scan and from heard broadcasts,
   and the Feature Abort of the two messages on a bus with no audio
   system.
2. The volume keys in `cec/`, the request in the wake, and
   `SystemAudioApplied`.

## How it will be proved

The fakes in `cec/cectest` and `kubernetes/apiservertest` prove each
path, and `vivid` proves the messages against the kernel. Six drills
run on a home cluster with an AVR-X1700H, then a WiiM Amp. None has
run.

1. In `Listen`, record each System Audio message, its sender, and
   whether it is directed or broadcast: when the TV's speaker menu
   picks the receiver, when the receiver is turned on at its front
   panel, when it goes to standby, and in the first 10 seconds after
   the TV comes on.
2. Send Give System Audio Mode Status to address 5 with the receiver on
   and in standby, and record the answers.
3. With the receiver on the session's input, send the request with the
   `Display`'s address. Check that the receiver stays on the input that
   the network block selected, that the TV mutes, and count the TV's
   Routing Changes for 20 minutes after.
4. Send the request to the receiver in standby, with the Denon's TV
   Audio Switching on and then off. Record whether it wakes, and which
   input it selects.
5. After a request, set the TV's speaker menu to its own speakers.
   Check that the TV ends the mode and that the operator sends nothing.
6. Check whether the WiiM Amp claims logical address 5 on HDMI ARC.
   WiiM documents its CEC behaviors for the WiiM Ultra only. A WiiM Amp
   on optical alone holds no logical address, and its bus gets no
   request.
