# 84, One power state for a Player

Proposed on 2026-10-25. A `Player` that is wired through a `Receiver`
has no single record of whether it is on. Five records each hold part
of that state, different events update each one, and the records can
disagree with each other and with the equipment. The work touches
`media-screen`, `media-operator`, `equipment-operator`, and
`display-operator`.

## The problem

A person turns a `Player` on and off with a remote, and a program
should be able to do the same through the API. Today the operation
that a key starts depends on records that do not agree:

| Record | Writer | What changes it |
|---|---|---|
| The panel desire, retained on the `Player`'s `panel` topic | the idle screen client | the fade and off windows, a press on a dark screen, the `wake` and `sleep` asks |
| `awake` and `active` in the `Receiver`'s `status.session` | `media-operator` | the panel desire, and whether a `Play` stands |
| `awake` in the `Television`'s `status.session` | `equipment-operator` | a wake, a `standbyAt`, and both session flags turning false |
| The `Receiver`'s `spec.power` | `equipment-operator` | a power ask only |
| The TV, the receiver, and the panel | people and other devices | the TV's own remote, another source's Standby, a receiver that follows the TV over HDMI-CEC |

Two faults on a home cluster came from this split.

1. A power press turned the TV and the receiver off and left the panel
   desire `on`, so the `Receiver` session stayed awake. For the
   `Player`'s `offAfterSeconds`, 1800 s by default, a home press
   wrote a show ask to equipment that was off, and nothing happened.
   After the off window, any key turned the room on. So whether a key
   turned the room on depended on a timer.
2. An Android TV records the last Active Source it heard and ignores
   another one for the same address. It heard the wake's Active Source
   while it booted, opened its own home screen, and then ignored every
   home press. `equipment-operator` now sends Inactive Source before
   the claim of a home press.

## What is built

A power press now moves the panel desire with the ask it publishes
(`media-screen/src/screen/power.rs`). A press that turns the `Player`
off publishes `off`, lowers the shade, and states the `off` desire, so
the session's `awake` flag turns false with the equipment. A press that
turns the `Player` on wakes the screen and states `on`, and the flag
that turns true wakes the TV and the receiver. The toggle follows the
desire, so a stale read of the TV's power no longer decides a press. A
`Keymap` row that maps a power button to `KEY_SLEEP` makes that button
turn the `Player` off and never on.

## What is left to design

* **The `Player`'s power in the API.** A program can turn a `Player`
  on or off only by publishing on MQTT. A field on the `Player`, with a
  condition that reports what the equipment did, would give `kubectl`
  and the remote one operation. The question is which component owns
  the state: the idle client, which owns the desire now, or
  `media-operator`.
* **Release and power down.** A `Player` shares its TV and receiver
  with other sources, such as a streaming stick on another input. The
  off window releases the equipment: the adapter sends Inactive Source
  and leaves the TV on. The power button powers the equipment down,
  which also stops the other source. A `Player` should state which of
  the two its off means. The code calls the equipment "the room", and
  that word hides the difference.
* **Events from outside liken.** The TV's own remote, another source's
  Standby, and a receiver's front panel change the equipment. Some of
  these move the desire through the `sleep` ask, and some change no
  record. A TV that announces nothing leaves `status.power` stale.
* **The `commands` topic.** A `home` ask on the `Player`'s `commands`
  topic reaches the client as a home press, but writes no show ask on
  the `Receiver`, so it does not switch the TV or the receiver the way
  the remote's home key does.
* **`Receiver.spec.power`.** A `Play` that turns the receiver on does
  not update `spec.power`, so a `kubectl` edit of the field can do
  nothing.
* **The panel on a TV.** `display-operator` turns a panel off with a
  DDC/CI write. A TV behind a receiver does not answer DDC/CI, so the
  `off` desire there only draws a black picture, and the HDMI signal
  continues.
* **A home press while the TV boots.** A home press reads the TV's
  power first and sends nothing when the TV does not answer. On a home
  cluster, a press about 30 s after a wake got no answer while the TV
  ran its own device discovery, so that press switched nothing, and
  the next one worked.
