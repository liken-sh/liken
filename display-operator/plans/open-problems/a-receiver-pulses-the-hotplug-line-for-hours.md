# A receiver pulses the hotplug line for hours

On a home cluster, one machine's HDMI port goes into an AV receiver.
For hours at a time, the port reports its monitor disconnected and
connected again, three times in about two seconds, and the bursts
repeat every 30 to 70 seconds. Two spells in three days lasted about
eight hours and about fourteen and a half hours, at 400 to 550
`drm change` uevents an hour.

## What each pulse costs

Each disconnect makes weston disable the output, and each connect
makes it enable the output again with a mode set. Weston prints the
whole mode list on each enable, about 9,000 to 10,000 lines an hour
during a spell. audio-operator reads an ELD and jack change on each
pulse, about 800 lines an hour. The operator's own passes wake on
each uevent and find nothing to write: no slice write and no status
write in a sample hour. So the cost is log volume and CPU, and one
mode set on the port every few seconds.

## When the pulses happen

The first spell came before the CEC bus was logged. The second began
eleven seconds after the TV behind the receiver broadcast CEC
`Standby`, while a compositor restart for a mode change was in
progress. It ended when another source on the receiver became the
active source. Outside the spells,
the port reports a pulse 100 to 300 ms after a CEC routing message
moves the receiver to another input or back: `Set Stream Path`,
`Routing Change`, or `Active Source`.

A compositor start does not cause a pulse by itself, as far as the
logs show. Of 19 weston starts on that machine in three days, 13 had
no uevent in the 45 s after the start. Of the other 6, three fell
inside a spell that was already running, and two came 100 to 300 ms
after a CEC routing message. The last one came before the CEC bus
was logged, so its cause is not known. One of the two with a CEC
message was a rollout: the receiver pulsed the port 18 s after the new
weston started, 136 ms after another source on the receiver sent
`Active Source`.

## What is not known

- Whether the mode set that weston makes after each connect is what
  makes the receiver pulse again. [Plan 24](../completed/24-the-compositor-holds-drm-master.md)
  raised the same question about the kernel's console mode set, and
  the spells continued after weston held DRM master, so the console
  is not the only cause.
- What the receiver's own settings do. Receivers pass a signal through
  in standby, and some let a person turn that off. That setting was
  not read.
- Whether the pulses reach anything a person sees or hears. The TV was
  off during the second spell.

## Options

1. Accept the spells. The operator writes nothing during them, and
   the cost is log lines and mode sets on a screen nobody watches.
2. Find out whether weston's mode set feeds the loop. On a lab
   machine into a receiver in standby, stop the compositor during a
   spell and count the pulses with nothing driving the port. If the
   pulses stop, the loop needs a way to leave the port undriven while
   the receiver is in standby. Weston has no setting for that, and the
   project does not patch weston, so the operator would stop or
   restart the compositor, which a claim on the screen forbids.
3. Cut the log volume. Weston's mode list on each enable is weston's
   own output. The operator could filter the `weston` container's
   repeated mode lists before they reach the log, at the cost of a
   filter between weston and the kubelet.
