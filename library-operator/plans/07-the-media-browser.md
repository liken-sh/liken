# The media browser

Plan 07. The screen pod's browser takes the room's remotes. At the end
of this plan a person in a room presses a button, sees the libraries,
walks into one, and reaches a movie or an episode. Nothing plays yet.

## The problem

Plan 06 put the browser on the screen, and it takes no input there.
The browser already has its levels: the libraries, a movies library
as a wall of posters, and a series library as series, seasons, and
episodes, each level a fact of the kind's table and none of it code
per kind. It moves focus on a handful of key names, `up`, `down`,
`left`, `right`, `enter`, and `escape`, from a keyboard on a
workstation and from the scripted timeline in a headless run. A
remote in a room reaches none of that.

The remotes speak the bus. `media-operator`'s idle command pod stands
for every unit, whatever draws its screen, and holds the unit's
keymaps, the focus mark of each controller, and the shade. Its plan 23
does two things for a delegate: it publishes the broker and the two
topics on `status.idle.bus`, and it forwards each navigation press on
the `Player`'s commands topic while the unit plays nothing, the mark
names the unit, and the screen is awake. The browser has no keymap,
no focus mark, and no shade of its own, and this plan gives it none.

## The contract

**What the browser reads.** Three variables, set on the browser
container from `status.idle.bus`: `MEDIA_BUS_ADDRESS`,
`MEDIA_PLAYER_COMMANDS_TOPIC`, and `MEDIA_PLAYER_SCREEN_TOPIC`. They
carry the names `media-operator`'s own client reads, so the two
clients of one contract are wired the same way. A `Player` whose
status has no `bus` block, which is a `Player` under an older
`media-operator`, gets a pod without them. The browser then opens no
connection and takes the keyboard alone, and the pod template rolls
when the block appears.

**Presses.** The commands topic carries `{"action":"up"}` and the
like. `up`, `down`, `left`, and `right` move focus as the keyboard
arrows do, `select` is `enter`, and `back` is `escape`. Any other
action is ignored. A press is a key by another route: one path takes
the keyboard, the script, and the bus, so a press from a remote
exercises exactly what the tests exercise.

**The shade.** The screen topic carries the command pod's moments.
`sleep` draws a black frame and holds it. `wake` draws the level the
browser left, at the same focus. `present` maps a fresh surface, the
port of `media-operator`'s own client: the compositor has no seat, so
a surface a film covered stays hidden until a new one is mapped.
`focus` changes nothing, because the browser draws no identity block
to pulse.

**Back at the top.** Back below the libraries climbs one level, as it
does from the keyboard. Back at the libraries publishes
`{"action":"sleep"}` on the commands topic, and the command pod brings
the shade down. The browser never sleeps itself, because the shade is
the command pod's alone.

**Freshness and art.** As plan 06 left them: the browser re-reads what
the update stream names, so a title that lands on the volume appears
on the open wall, and posters are read from the mounted volumes,
decoded at the size they are drawn at, and cached with a bound. The
workstation measured 130.9 MiB resident during a full scroll of the
wall, beside the head-to-head's 115 MB.

## The local harness

`local/browse` already takes the keyboard under the same names. The
three variables are read from the environment when set, so a run
against a local broker is the same binary with them exported, and no
second script is needed.

## What was set aside

Search, filters, and rows like "recently added" and "continue
watching". They belong to the open problem on what a screen shows and
to the watch-state plan. A person can find a movie without them.

A web view. A web page has no place on a ten-foot screen driven by six
buttons.

A keymap in the browser, and a subscription to the remotes' own
topics. Every client would then reproduce the focus gate and the
shade, and `media-operator`'s plan 23 sets that aside for the same
reason.

A built-in idle view beside the browsing view. An earlier draft of this
plan had both, with a press opening the browser and the idle timer
returning to a clock. The browser is the delegated unit's idle screen
now, and the shade is what the timer brings down. A screensaver drawn
from the libraries' art is welcome later work, and not this plan's.

Publishing the browser's view on the bus for the `Player` status. No
consumer reads it.

## Proof

On `liken-1`, on `lab-portable` with both of its controllers paired:
from the X6 and from the pad, the arrows move focus, select opens the
movies library as a wall with the right count, a held arrow repeats,
and the focused title's name is right. The series library opens to
series, then seasons, then episodes, checked against one series by
hand. Back climbs each level, back at the libraries darkens the
screen, and the next press wakes it and moves nothing. A `Play`
created on `lab-portable` covers the browser, and its end re-presents
the browser at the same focus. The browser's resident memory on the
box during a full scroll of the wall is recorded beside the
workstation's 130.9 MiB.
