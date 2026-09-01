# 23, Presses reach a delegate

Plan 22 handed the idle screen to a named controller and left the bus
off `status.idle` until a client needed it. The library layer's browser
is that client: its plan 07 drives the browser from the room's remotes.
This plan publishes the bus facts a delegate's client reads, forwards
the navigation presses to it, and takes the one request it makes.

## What a delegate's client cannot learn

The idle command pod holds the keymaps, the focus gate, and the shade.
It reads every press of the unit's controllers and answers the ones
that are its own: a wake, back as sleep, volume and mute, and
cycle-focus. The navigation actions, up, down, left, right, and select,
reach no one while the unit plays nothing, because the stock client
draws no list.

A delegate's client draws one, and it has no keymap, no focus mark,
and no shade of its own. It must not grow them. The command pod exists
so that one process per unit holds all three, and a client that
reproduced them would answer a press twice.

The client also cannot learn the broker's address or the topic base.
Both are this operator's configuration, and the delegate's operator
never reads it.

## The status

`status.idle` gains a `bus` block. It is present whenever the idle
command pod stands, which is under every controller but
`media.liken.sh/none`:

    status:
      idle:
        controller: library.liken.sh/media-browser
        claim: studio-lg-idle-devices
        requests: [draw, render]
        bus:
          address: bus.liken-system.svc:1883
          commandsTopic: liken/media/players/default/studio-lg/commands
          screenTopic: liken/media/players/default/studio-lg/screen

`address` is the broker, as `host:port`. `commandsTopic` is where the
presses arrive and where the client asks for sleep. `screenTopic`
carries the command pod's moments, `sleep`, `wake`, `focus`, and
`present`, as plans 12 and 22 describe them. The status topic and the
volume topic stay off the block until a delegate draws what they
carry.

## The forward

Under a delegate, the idle command pod publishes each navigation press
on the `Player`'s commands topic, as `{"action":"up"}` and the like.
That is the shape a translator publishes on a `Play`'s commands topic,
so a client reads one vocabulary on both trees. The gates are the ones
every other press already reads: the unit plays nothing, the remote's
mark names this `Player`, and the press is a down edge. A press on a
sleeping screen is a wake and nothing more, so the first press after
the shade comes down reaches no client. A binding whose keymap repeats
it publishes again while the control is held, on the clock the volume
repeat already runs.

`back` is forwarded too, under a delegate, and it no longer brings the
shade down there. The client has levels, and only the client knows
whether back has anywhere to go. Under this operator's own controller
nothing changes: back is sleep, and the navigation presses reach no
one.

The pod learns the controller from a variable on its container, the
resolved name the status shows, and compares it to the operator's two
constants the way the operator does.

## The one request a client makes

A client at its top level, with nothing to go back to, asks for sleep:
`{"action":"sleep"}` on the commands topic. The command pod reads it
beside `re-present` and brings the shade down when the unit plays
nothing and the screen is awake. The stock client sends none, because
the command pod sleeps it on back directly.

So the commands topic under `players/` carries two verbs, and both are
display plumbing rather than the media vocabulary: the operator writes
`re-present`, a client writes `sleep`, and a controller sends neither.

## Considered and set aside

* **The client subscribes to the remotes' events topics and holds a
  keymap.** Every client would reproduce the focus gate and the shade,
  and plan 22 put them in one pod per unit on purpose.
* **The command pod forwards navigation under every controller.** The
  stock client would then answer back with a sleep request, a second
  hop for a behavior that works today.
* **The topic base on the status, for the client to derive its
  topics.** The kinds under `players/` are this operator's contract.
  Naming each topic keeps the client out of it.
* **The status topic and the volume topic on the block.** No delegate
  draws them yet. They join the block when one does.

## How the work is proved

On `liken-1`, with `lab-portable` delegated to the library layer's
browser and both of its controllers paired:

1. `status.idle.bus` names the broker and the two topics, and the
   delegate's pod carries them.
2. From the browser, the arrows move focus, select descends, back
   climbs, and a held arrow repeats. Both the pad and the X6 do it, and
   only the controller whose mark names `lab-portable`.
3. Back at the browser's top level brings the shade down. The next
   press wakes the screen and moves nothing.
4. A `Play` on `lab-portable` covers the browser, and its end
   re-presents the browser where it was.

Drilled on `liken-1` on 2026-09-01, in release 2026.09.01-005, with the
library layer's browser at its release 2026.09.01-004. The status
carried the broker and both topics, and the delegate's pod carried
them as variables. From the X6, every arrow, select, and back arrived
on the commands topic as the press it named, a held arrow repeated, and
the wall moved with them. Back at the browser's top level arrived as
the client's `sleep` request, followed within the same second by the
command pod's `sleep` moment; the next press arrived as `wake` and
nothing else. A one-off `Play` covered the browser, and its deletion
published `re-present`, then `present`, and the browser drew again
where it was. The idle command pod stayed at 2 MiB resident.
