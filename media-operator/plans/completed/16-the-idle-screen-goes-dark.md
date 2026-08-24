# The idle screen goes dark

Plan 16. After a quiet stretch, the idle screen fades to black, and any
press on the unit's remotes brings it back. This is the software half of
[plan 09](../09-the-idle-screen.md)'s sleep: the pixels go dark and the
panel stays lit. The hardware half is
[plan 17](17-the-idle-screen-powers-the-panel.md).

## The problem

The idle screen draws the clock, the unit's name, and its parts at full
brightness for as long as the `Player` stands. A screen in a bedroom or
a studio glows all night at a person who asked it for nothing. Plan 09
names the answer, a panel that follows demand, but the panel-power seam
is not decided. The fade needs no seam at all: the idle pod already
draws every pixel on the screen, so it can draw black.

## The design

### The policy is a `Player` field with a household default

`PlayerSpec` gains `idle`, and `MediaPreferencesSpec` gains the same
block as the household default:

```yaml
idle:
  fadeAfterSeconds: 600
```

Resolution reads the `Player`'s block first and the default
`MediaPreferences` second, field by field, the way languages resolve. A
cluster that states neither fades after 600 seconds. An explicit `0`
disables the automatic fade, so a kiosk that must never dim on its own
states `0` and keeps the screen. The `back` toggle below works at `0`
too, because a deliberate press is not a quiet stretch. The block will
later carry the hardware half's fields, so the fade lands in its final
home.

### The sidecar owns the timer

The idle command sidecar is the one process with the bus connection and
a clock, so the timer runs there and the display only draws. The
operator resolves the policy into `IDLE_FADE_AFTER_SECONDS` on the
sidecar container, and it joins the events topics of the `Player`'s
remotes into `IDLE_REMOTE_EVENTS_TOPICS`, newline-separated the way
`IDLE_PLAYER_COMPONENTS` joins names. Plan 13's template hash rolls the
idle pod when either value changes, so a policy edit takes effect with
no new channel.

The sidecar subscribes to the events topics beside the two topics it
holds today. The timer follows three rules:

* The timer arms only while the last status names the activity `Idle`.
  A `Play` never sleeps the screen: while one starts or runs, the timer
  is off.
* Any press on a remote's events topic resets the timer, and wakes the
  screen if it sleeps. A press is the person, so the press is the wake
  signal, exactly as plan 09 states: the wake rides the bus. Only the
  press edge counts: a key event with value 1, or a hat event away from
  center. A release follows every press on the topic, and a release
  that counted would wake the screen the press just put to sleep.
* A status whose activity leaves `Idle` wakes the screen, so a `Play`
  started from another room lifts the black before the mark's starting
  ramp draws.

A person can also put the screen to sleep by hand. The sidecar reads
the retained `Keymap` from the bus and translates each event, the same
translation the playback sidecar applies, so a press has a name. A
press named `back`, while the activity is `Idle` and the screen is
awake, sleeps the screen at once. Any press wakes a sleeping screen,
`back` included, so `back` toggles the screen from either side.

On expiry the sidecar sends the `player-sleep` script message, and on
wake it sends `player-wake`, over the same IPC dialog the status rides.

### The display draws the shade

A new module, `shade.lua`, draws one full-canvas black rectangle over
everything in the idle branch. `player-sleep` eases its alpha from
clear to opaque over four seconds, and `player-wake` eases it back in
under half a second, because going dark may be slow but a person who
pressed a button is waiting. The ease runs on a thirty-frame timer the
way `energy.lua` runs its ramps, and the timer stops when the ease
lands, so a sleeping screen spends the one-second clock tick and no
more. The shade draws last, so it covers the clock, the mark, and the
identity block.

The preview gains an `s` key that toggles sleep and wake through the
same handlers the script messages call, so a workstation shows the fade
without the ten-minute wait. The timer itself is Go and is proved by
tests, not by the preview.

## What was considered and set aside

**The timer in the display script.** `mpv`'s Lua can run timers, but
the wake signal is on the bus, and only the sidecar holds the bus. A
Lua timer would need the sidecar to forward every remote press into the
script, a second contract for the same information. The sidecar already
folds bus messages into script messages, so the timer sits beside the
fold.

**`offAfterSeconds` in this plan.** The block could carry the hardware
half's field now, inert. Set aside because the field's meaning depends
on the answer [plan 17](17-the-idle-screen-powers-the-panel.md)
records, and an API field that actuates nothing contradicts how the
operators report themselves. The field lands with the slice that
actuates it.

**Waking on remote presence changes.** A controller that connects is a
person nearby. Set aside because a controller that disconnects is not,
and the presence topic carries both edges. The events topic carries
only presses, so it is the honest signal.

## How the work is proved

The timer is proved by Go tests with an injected clock: it arms on
`Idle`, resets on a press, expires into `player-sleep`, and wakes on a
press and on a status that leaves `Idle`. The fade is proved on a
workstation with the preview's `s` key.

The drill runs on `liken-1`. Set `fadeAfterSeconds: 60` on the studio
`Player`. After a quiet minute the screen fades to black. A press on
the pad brings it back fast. A press on the pad's `back` button fades
it at once, and a second press brings it back. Start a `Play` from `kubectl` while the
screen sleeps, and the screen wakes and the film plays. End the `Play`,
and the idle surface returns and a fresh quiet minute fades it again.
Measured on the metal: the fade, the wake, and the wake on a `Play`,
eyewitnessed on the studio panel.
