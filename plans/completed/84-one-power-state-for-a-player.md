# 84, One power state for a Player

Proposed and built on 2026-10-25. Touches `media-operator`,
`media-screen`, `equipment-operator`, and `display-operator`.

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

## Built

* `spec.power` and `status.power` on the `Player`, the keys in
  `media-operator`, power down and wake, `PowerApplied`, and a `Play`
  that turns its `Player` on (`c6d93664`).
* An `off` desire from before a turn-on no longer darkens the panel
  (`10ca75fc`).
* A home press sends the TV Inactive Source before it claims the input
  (`01267889`), and no longer reads the TV's power first (`8ef0a424`).
* For 10 s after a wake's first claim, the TV's own Set Stream Path
  does not end the wake's guard, so the guard takes the input back from
  a streaming player the TV switched to as it came on (`c5991148`).

## Drills

* On `liken-1`, the single-monitor `Player` `lab-portable`: `kubectl`
  off and on, a `Play` on a `Player` that is off, off during a film, an
  operator restart while off, and remote keys sent over the bus. The
  panel reported power off and on over DDC/CI each time. Three turn-ons
  after `10ca75fc` applied no stray `backlight Off`.
* On a home cluster, a `Player` with a receiver and a TV: the power key
  stopped the film and put the TV and the receiver in standby, and a
  home key woke both. The wake took 15 s while the TV booted, and the
  TV switched to a streaming player during it, which led to
  `8ef0a424` and `c5991148`. The drill has not run again on those two
  builds.

## Left open

* `equipment-operator/plans/open-problems/a-tv-turned-off-by-its-own-remote-reaches-no-record.md`
* `media-operator/plans/open-problems/the-home-ask-on-the-commands-topic-switches-no-equipment.md`
* `display-operator/plans/open-problems/a-tv-behind-a-receiver-answers-no-ddc.md`
