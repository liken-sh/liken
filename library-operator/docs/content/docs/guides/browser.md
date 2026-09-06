---
title: Put the media browser on a screen
weight: 50
---

# Put the media browser on a screen

The media browser is a native Wayland client that draws the namespace's
catalog on a screen, takes a remote's presses, and starts a `Play`.
It takes the place of `media-operator`'s idle screen on a `Player`.
This guide names it on a `Player` and describes what a person sees.

## 1. Hand the idle screen to the operator

A `Player`'s `spec.idle.controller` names the operator that draws its
screen when nothing plays. Set it to this operator's name:

    apiVersion: media.liken.sh/v1alpha1
    kind: Player
    metadata:
      name: living-room-player
      namespace: media
    spec:
      idle:
        controller: library.liken.sh/media-browser

`media-operator` resolves the name into `status.idle.controller`, and
this operator compares against that resolved value, never the spec.
The `Player` must be in the same namespace as the libraries it shows.
[Hand the idle screen to another controller](https://media.liken.sh/docs/guides/handing-the-idle-screen-to-another-controller/)
in the `media-operator` manual describes the delegation from its side.

## 2. What the operator runs

The operator creates a pod named `<player>-media-browser`, owned by
the `Player`, so deleting the `Player` deletes the pod. It has two
containers: the catalog agent as a native sidecar, and the browser.
The browser starts after the agent's startup probe passes.

The pod mounts every `Library` of the namespace read-only, whatever
the browser does. It mounts its own catalog claim, and a `640Mi`
cache for scaled posters. It claims the display through the standing
`ResourceClaim` that `media-operator` holds for the `Player`, so no
second claim on the display is created.

    kubectl -n media get player living-room-player
    kubectl -n media get pod living-room-player-media-browser
    kubectl -n media logs living-room-player-media-browser -c browser

The browser's first log line reports the home page's open time:

    media-browser: the home page opened in 38.2 ms

When the default `MediaPreferences` states `spec.timeZone`, the
browser draws its clock in that zone. Otherwise it runs on UTC.

## 3. How presses reach it

The `Player`'s `status.idle.bus` names the bus topics for the
`Player`'s remotes. The browser subscribes to each remote's events
and its retained focus mark, and answers a press only while the mark
names this `Player`. Starting a `Play` moves the mark to the `Play`'s
`Player`. A `Play` ending moves nothing, because the `Player` it names
is still there, showing its idle screen.

Over the bus, every key a remote sends reaches the browser under the
kernel's name, except volume, mute, and the cycle key. Those three are
handled before they reach the browser, and the browser draws the level
as a fading row. So a remote with a keyboard types into search, and
its home and search buttons work once a `Keymap` names them. The same
keys reach the browser from a keyboard attached to the screen's
machine.

## 4. The keys

| Key | Effect |
|---|---|
| arrows | move focus |
| enter | open the card, or play from a title's page |
| back | pop to the screen before this one |
| home | pop to the home page |
| search | open the search wall with the on-screen keyboard |
| a letter or a digit | open the search wall with that character typed |

Up from the top of any wall puts focus on the strip, the row across
the top that holds the clock and the search glass. Right from the last
column of a long wall puts focus on the rail, the bars at the right
edge that jump through the wall by year, decade, letter, or season.
Enter on the rail's sort button cycles the wall's order.

## 5. The screens

The home page is the bottom of the stack. It draws a banner, what was
recently released and recently added, a few strips chosen for the day,
the libraries row, and every genre. Selecting a library or a genre
opens a wall of posters under a heading with its count. A wall longer
than eight rows draws the rail.

A movie's page shows its art, its facts, its people, and the set or
franchise it is part of. A series' page shows its seasons as a wall
of episode stills. A person's page shows their credits and their
biography. A franchise's page draws its story order as one lane with a
line per universe beside it, and its eras on the rail.

Search is a wall like any other. Every typed character rereads it.
The index is built in memory from titles, original titles, people's
names, episode titles, and descriptions, in that rank.

The on-screen keyboard is a grid over the wall. The arrows move across
it and enter types the focused cell. Down off the bottom row closes
the grid and puts focus on the first hit, which is how a remote with
no keyboard accepts the text. Back over the grid closes it and keeps
the text. Back over the wall with the grid closed clears the text, and
back over an empty search leaves the wall. Up from the first row of
hits puts focus on the strip, and enter there opens the grid again.

## 6. Playback

Enter on a title resolves what to play from the browser's own copy of
the catalog and publishes the list on the bus. The operator turns it
into a `Play` for the `Player`, with each item's path as a claim
reference, and `media-operator` runs it. The `Play` is named after the
title, so `kubectl get plays` reads like a listing. When the `Play`
ends, the browser is presented again on the page it left.

## Every screen shows every library

Nothing scopes a `Player`'s screen to a subset of the namespace's
libraries. A screen that should show fewer libraries needs a namespace
of its own. See
[The namespace is a boundary](/docs/guides/libraries/#the-namespace-is-a-boundary).
