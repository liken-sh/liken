# 72, Power and home during a Play

Designed, not built. It spans media-operator's playback sidecar, the
`media-screen` crate, and library-operator's browser. During a `Play`,
the power button stops the `Play` and then does what power does on an
idle `Player`. The home button stops the `Play` and returns to the home
page of whatever screen stands under it, for every remote that has a
home button.

## The problem

A person watching a film presses power on the remote, and nothing
happens. The press reaches three readers, and each one drops it:

- The playback sidecar has no row for power in its table
  (`media-operator/keybindings.go`), so `commandForKey` logs "ignored,
  because the playback table has no row for it".
- `media-screen` acts on power only while the `Player` is idle
  (`media-screen/src/screen.rs`, `Screen::on_press`). The idle or
  browser pod stands under the film the whole time, but it does not
  act.
- media-operator's `ensureInput` skips power keys on purpose
  (`media-operator/ensure.go`).

media-operator plan 24 reserved power and home with no consumer. Home
got one later: during a `Play`, the sidecar publishes
`{"action":"home"}` on the `Player`'s commands topic and ends the play,
and `media-screen` hands `KEY_HOMEPAGE` to the screen under it. Power
never got one.

Home works only for the names that a reader binds, and the readers
disagree:

- The sidecar binds `KEY_HOMEPAGE` and `KEY_WWW`.
- The browser binds only `KEY_HOMEPAGE` (`media-browser/src/browser/keys.rs`).
  A remote whose home button sends `KEY_WWW` gets home during a film
  and nothing in the browser.
- A remote whose home button sends another name, such as `KEY_HOME` or
  `KEY_MENU`, gets nothing anywhere unless its `Keymap` has a row for
  it.

Power is uneven while idle too. With a `Receiver`, `media-screen`
publishes a toggle on the `Player`'s power topic, and equipment-operator
turns the room on or off. With no `Receiver`, the browser lowers the
shade, but the stock idle screen ignores power.

## The design

### Power stops the Play, then acts as it does when idle

The sidecar binds `KEY_POWER`, `KEY_POWER2`, and `KEY_SLEEP`. A power
press during a `Play` publishes `{"action":"power"}` on the `Player`'s
commands topic and ends the play through `exit()`, the same path home
takes (`media-operator/home.go`).

`media-screen` holds the power ask until the `Player`'s status is
`Idle`. The ending publishes `Idle` at once (`media-operator/ending.go`),
so the wait is short. Then `media-screen` runs its idle power branch:

- With a `Receiver`, it publishes the toggle. The room is on during a
  `Play`, so equipment-operator turns it off.
- With no `Receiver`, it hands the press to the screen, which lowers
  the shade.

The order matters. If the toggle went out while the `Play` still ran,
the ending would reach equipment-operator after the room went off, and
the next press would decide from a room that changed under it.

### Power sleeps on every idle screen

The stock idle screen (`media-operator/idle/src/client.rs`) answers
power the way the browser does: it lowers the shade. So power means the
same thing with or without a `Receiver`, and with either screen.

### One name for home

Every reader binds the same home names, and one file declares the
list. The base key table that media-operator compiles for each
`Remote` (`media-operator/basekeys.go`) maps each home name to
`KEY_HOMEPAGE` before it reaches the bus. The sidecar and the browser
then read one name. A remote with an odd home button needs one
`Keymap` row, and that row works in every state.

## What the design must answer

- Which names count as home. `KEY_HOMEPAGE` and `KEY_WWW` are
  certain. `KEY_HOME` is also the Home key of a keyboard, which some
  screens could want for navigation.
- The key name the reported remote sends. The sidecar's log line for a
  failed home press names it, and it decides whether the base table
  alone fixes home, or the remote needs a `Keymap` row.
- What `media-screen` does with a held power ask that no `Idle` status
  follows, for example when the sidecar dies before the ending.
- How this meets media-operator plan 35, which adds TV remotes over
  HDMI-CEC. A TV remote's power button may send a name that none of
  these readers binds.

## Not in this plan

- A power press that turns the room on and starts the last `Play`
  again.
- Any change to equipment-operator's toggle rule (`togglePower` in
  `equipment-operator/session.go`).

## How it will be proved

- Sidecar tests: each power name publishes the ask and ends the play;
  each home name reaches `home()`.
- `media-screen` tests: a held power ask runs the toggle after `Idle`
  with a `Receiver`, and lowers the shade without one; the toggle does
  not go out before `Idle`.
- Base table tests: every home name compiles to `KEY_HOMEPAGE`.
- Browser and idle screen tests: power lowers the shade.
- A drill on a `Player` with a `Receiver`: power during a film turns
  the room off, and the next power press turns it on at the browser's
  home page. Home during a film returns to the browser's home page on
  each remote the cluster has.
