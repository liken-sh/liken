# 72, Power and home during a Play

Built on 2026-09-29, except the drill, which is still owed. Power is
built in media-operator's playback sidecar, the `media-screen` crate,
and the stock idle screen. library-operator's browser needed no change:
it already lowers the shade on the three power names. During a `Play`,
the power button stops the `Play` and then does what power does on an
idle `Player`.

Home is not part of the build. The maintainer confirmed that home
during a `Play` works on their remote, so the base key table, the
sidecar's home names, and the browser's home names are unchanged. The
home sections below record the design as it was proposed.

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
disagree. (Home during a `Play` was later confirmed working on the
reported remote, so this part of the problem did not need a change.)

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

The ordering holds in the code. The sidecar publishes the ask on the
`Player`'s commands topic, and then `exit()` calls `endRun`, which
publishes the ending on the `Play`'s status topic. When the operator's
bus reader reads the ending, `answerEnding` publishes the `Player`'s
status from the lists the last pass read, so `Idle` follows at once.
The fallback is an operator that has finished no pass, or a `Play`
created since the last pass: that ending waits for the next pass.
`media-screen` receives the power ask on the `Player`'s commands topic
in `on_command`, the same place it receives the home ask.

A held ask has a deadline of 10 seconds. If no `Idle` status arrives by
then, `media-screen` drops the ask and logs one line that says so. The
deadline is a clock on the reader's clock thread, the thread that runs
the quiet window and the off window, and nothing reads the status again.
A second ask while one waits restarts the deadline, and the room
toggles once. An ask that arrives while the unit is already `Idle` is
answered at once.

The sidecar binds the names in `powerKeys` (`media-operator/ensure.go`),
the list `ensureInput` exempts. `media-screen` declares the same three
names as `POWER` in `media-screen/src/screen/keys.rs`. Go cannot read a
Rust list, so each copy has a comment that names the other.

A CEC remote's power and home buttons reach these readers under the
same names once media-operator plan 35 binds them.

### Power sleeps on every idle screen

The stock idle screen (`media-operator/idle/src/client.rs`) answers
power the way the browser does: it lowers the shade. So power means the
same thing with or without a `Receiver`, and with either screen.

### One name for home

Not built: home during a `Play` was confirmed working on the reported
remote.

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

  Answer: `KEY_HOMEPAGE` and `KEY_WWW`, and `KEY_HOME` stays unbound.
  No change was built, because home during a `Play` already works on
  the reported remote.
- The key name the reported remote sends. The sidecar's log line for a
  failed home press names it, and it decides whether the base table
  alone fixes home, or the remote needs a `Keymap` row.

  Answer: home during a `Play` was confirmed working on that remote, so
  it needs neither.
- What `media-screen` does with a held power ask that no `Idle` status
  follows, for example when the sidecar dies before the ending.

  Answer: it drops the ask after 10 seconds and logs one line that says
  so. A toggle long after the press would turn the room off or on with
  no person behind it.
- How this meets media-operator plan 35, which adds TV remotes over
  HDMI-CEC. A TV remote's power button may send a name that none of
  these readers binds.

  Answer: plan 35 is out of scope. A CEC remote's power and home reach
  these readers through the same names once plan 35 binds them.

## Not in this plan

- A power press that turns the room on and starts the last `Play`
  again.
- Any change to equipment-operator's toggle rule (`togglePower` in
  `equipment-operator/session.go`).
- The browser's `KEY_WWW` gap: the browser binds only `KEY_HOMEPAGE`, so
  a remote whose home button sends `KEY_WWW` gets home during a film
  and nothing in the browser.

## How it will be proved

The tests below are built, except the base table tests, which went with
the home work, and the browser's test, which already existed. The drill
is still owed.

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
