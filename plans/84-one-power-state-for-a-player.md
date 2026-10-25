# 84, One power state for a Player

Proposed on 2026-10-25. Touches `media-operator`, `media-screen`,
`equipment-operator`, and `display-operator`.

## The problem

Nothing in `liken` records whether a `Player` is on. The idle screen,
the `Receiver` session, the `Television` session, and the `Receiver`'s
`spec.power` each hold part of the answer, and they drift apart. On a
home cluster this showed up as a home key that did nothing for 30
minutes after a power-off. A program also has no way to turn a
`Player` on or off except over MQTT.

## The design

1. **The `Player` holds its power.** `spec.power` is `On` or `Off`, and
   an absent field is `On`. `media-operator` owns the state. A remote
   key and `kubectl` write the same field, and the last write wins.
   `media-operator` writes it with server-side apply under its own field
   manager, so a GitOps copy of the `Player` leaves the field out.
2. **Each piece of hardware follows it.** Off turns everything down:
   the panel through `display-operator`, the TV over HDMI-CEC through
   `equipment-operator`, and the receiver over its network API. On
   turns it all back on and claims the TV's input. A machine with one
   monitor and a machine with a receiver and a TV behave the same way.
3. **The keys.** A power key turns the `Player` off, `KEY_POWER` and
   `KEY_POWER2` toggle, and any other key on a `Player` that is off
   turns it on. `media-operator` reads the keys, so the idle screen
   draws and no longer decides power.
4. **The timers are a soft off.** After `fadeAfterSeconds` the screen
   draws a shade. After `offAfterSeconds` the panel goes dark and the
   `Player` lets go of the TV's input, but `spec.power` stays `On` and
   the equipment stays on. The next key lights the screen again.
5. **The status.** The `PowerApplied` condition says what the `Player`
   asked of its hardware for the last change of `spec.power`.

## Choices

* Off powers the equipment down, even when another source shares it.
  A `Player` that shares a TV with something people watch on their own
  could later choose to release the input instead.
* A TV on HDMI-CEC with no `Receiver` between it and the machine is out
  of scope. The `Television` follows a `Player` through the `Receiver`
  session today.

## Built before this plan

* A home press sends the TV Inactive Source before it claims the input,
  because an Android TV ignores a second Active Source for the address
  it already holds (`equipment-operator`).
* A power press moves the panel desire with it, so the `Receiver`
  session's `awake` flag turns false with the equipment
  (`media-screen`). This plan moves that decision into
  `media-operator`.

## Still open

* Changes made outside `liken`, such as the TV's own remote, reach no
  record when the TV announces nothing on HDMI-CEC.
* A `home` ask on the `Player`'s `commands` topic does not switch the
  TV or the receiver the way the remote's home key does.
* `display-operator` turns a panel off over DDC/CI, and a TV behind a
  receiver does not answer DDC/CI, so its panel only goes black.
* A home press reads the TV's power first and sends nothing when the TV
  does not answer. A press about 30 s after a wake got no answer while
  the TV ran its own device discovery.
