---
name: browser
description: "Put the media browser on a Player's screen in place of the idle screen, and learn its keys, its screens, and how it starts playback. Use when a Player should show the namespace's catalog and start a Play from a remote."
---

This skill is the guide at https://liken.sh/library/docs/guides/browser/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

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
      name: living-room
      namespace: media
    spec:
      idle:
        controller: library.liken.sh/media-browser

`media-operator` resolves the name into `status.idle.controller`, and
this operator compares against that resolved value, never the spec.
The `Player` must be in the same namespace as the libraries it shows.
[Hand the idle screen to another controller](https://liken.sh/media/docs/guides/handing-the-idle-screen-to-another-controller/)
in the `media-operator` manual describes the delegation from its side.

## 2. What the operator runs

The operator creates a pod named `<player>-media-browser`, owned by
the `Player`, so deleting the `Player` deletes the pod. It has two
containers: the catalog agent as a native sidecar, and the browser.
The browser starts after the agent's startup probe passes.

The pod mounts every `Library` of the namespace read-only, whatever
the browser does. It mounts its own catalog claim and its own art
claim, where the browser keeps every piece of art it scaled. It uses
the existing `ResourceClaim` that `media-operator` holds for the
`Player`, so no second claim on the display is created.

    kubectl -n media get player living-room
    kubectl -n media get pod living-room-media-browser
    kubectl -n media logs living-room-media-browser -c browser

The browser's first log line reports the home page's open time:

    media-browser: the home page opened in 38.2 ms

After that, the browser prints one line for each press, each play
request, and each change the bus confirms. A line names a title by
its catalog id and never by its name, and it names a typed character
only as "a character":

    media-browser: press enter (KEY_OK): opened the movie page of movie:tmdb:1001 in media/films
    media-browser: play movie:tmdb:1001 in media/films from the start for person-a: sent a request for 1 file on liken/library/players/media/living-room/play
    media-browser: press enter (KEY_OK): asked to play movie:tmdb:1001
    media-browser: the player went from idle to starting

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
as a fading row. While the `Player`'s owner-mark topic holds a
non-empty payload, equipment owns the room's level and draws its own
indicator, so the browser draws no row. So a remote with a keyboard
types into search, and its home and search buttons work once a
`Keymap` names them. The same keys reach the browser from a keyboard
attached to the screen's machine.

## 4. The keys

| Key | Effect |
|---|---|
| arrows | move focus |
| enter | open the card, open an episode's row, or press a button of a title's page |
| back | close an episode's row, or pop to the screen before this one |
| home | pop to the home page |
| search | open the search wall with the on-screen keyboard |
| a letter or a digit | open the search wall with that character typed |

Up from the top of any wall puts focus on the strip, the row across
the top that holds the clock and the search glass. Right from the last
column of a long wall puts focus on the rail. The rail is the bars at
the right edge, and they jump through the wall by year, decade,
letter, or season.
Enter on the rail's sort button cycles the wall's order.

## 5. The screens

The home page is the bottom of the stack. It draws a banner, a
continue-watching row, what was recently released and recently added,
a few strips chosen for the day, the libraries row, and every genre.
Selecting a library or a genre opens a wall of posters under a heading
with its count. A wall longer than eight rows draws the rail.

The continue-watching row belongs to the people at the screen. The
browser asks who is watching, and every play it requests records those
people. The row then reads the plays that named exactly them. A person
alone sees what they watched alone, and a family sees what the family
watched together. A night with one more person in the room still counts,
as long as that play continued from where the group had stopped. For
each series, set, and franchise those plays touch, the row offers the
next thing in its order, or the thing to resume. The card's second line
says why it is there: "Resume", "Next in" the series, the set, or the
franchise. A card that is next in a series or a set opens that title's
page. A card that is next in a franchise alone opens the franchise page
on that member.

A play that stands at position 0 is not started. The row draws no card
for it, and its series, set, or franchise offers nothing after it, so
the whole container leaves the row. A cleared title writes that play,
and so does a title marked unplayed in Jellyfin. A play that starts
over and stops before its first position report reads the same way.

The answer to who is watching lasts until three hours pass with no
press. The browser keeps it on the bus, retained, so a screen pod that
restarts inside those hours draws the same room and asks nobody.
[The library bus](https://liken.sh/library/docs/reference/bus/#who-is-watching) gives the
message.

A movie's page shows its art, its facts, its people, and the set or
franchise it is part of. A series' page shows its seasons as a wall of
episode stills. Enter on a still opens the episode's own row in the
header, with the buttons a movie's page has except Trailer, and back
closes the row.
A person's page shows their credits and their biography.
A franchise's page draws its story order as one lane, with a line per
universe beside it. Over the first row of each era it draws a heading,
with the era's length beside its name. An era inside a wider one reads
as a smaller line under it. While the wall scrolls inside an era, one
line held over the cards names the eras around it, outer to inner. Left
and right jump an era at a time. The wall moves only when the row in
focus would leave the screen.

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

## 6. Marking a title watched or clearing it

A movie's page and an episode's row end with two buttons. Each one
applies to everyone at the screen, the same people the
continue-watching row is read for, and it takes effect at once, with
no confirmation step.

| Button | Where it shows | Effect |
|---|---|---|
| Mark watched | on a title they have not finished | The title counts as finished. A film leaves the continue-watching row. In a series, a set, or a franchise, the next title takes the card. |
| Clear progress | on a title they started or finished | The title counts as not started, and its whole series, set, or franchise leaves the row. |

The actions of a continue-watching card are on the page the card
opens. A film's card opens the film's page, and an episode's card
opens the series' page on that episode, where enter opens its row.

The browser does not write the progress store. It publishes the mark
on the bus, retained, and the progress role writes it as one play at
the end of the title for watched, or at 0 for a clear. The mark states
the duration of the audience's play of the title where one exists, and
the catalog's running time where none does. A title with neither
sends no watched mark, and the browser logs a line that says so.

The screen shows the mark with no press, once the progress role has
recorded it. Each screen pod's progress agent names every play whose
row changed as the row arrives, and the browser then reads the
progress of the titles on its screen again. So every screen of the
namespace shows a mark pressed on another screen, and a position that
a `Play` records moves the bars and the continue-watching row the same
way. A change to a title the screen does not show reads nothing more.

Neither mark deletes a play. The store keeps the earlier plays, but
the browser reads the newest play of a title, so after a clear the
screen offers no Resume and draws no position for the title. A later
play of the title replaces either mark.
[The library bus](https://liken.sh/library/docs/reference/bus/#a-mark) gives the message.

A play counts as finished when the time left is at or under a twentieth
of the work, and at or under five minutes, whichever leaves less time.
Where the credits marks of the title's file place the credits in the
second half of the work, the play counts as finished from the start of
those credits instead. The browser reads the marks from its catalog.
The jellyfin role writes a title played in Jellyfin by the same rule,
and [Keep progress with Jellyfin](https://liken.sh/library/docs/guides/jellyfin/#3-what-crosses)
states it in full. Where the `Catalog` names a Jellyfin server, the
jellyfin role also sends each mark to Jellyfin.

## 7. Playback

Enter on a title resolves what to play from the browser's own copy of
the catalog. It then publishes the list on the bus, as a play request
on `liken/library/players/{namespace}/{player}/play`. The operator names
the topic on the browser container as `LIBRARY_PLAY_TOPIC`, and
[The library bus](https://liken.sh/library/docs/reference/bus/#the-play-request) gives every field
of the request. The operator turns it into a `Play` for the `Player`,
with each item's path as a claim reference, and `media-operator` runs
it. The `Play` is named after the title, so `kubectl get plays` reads
like a listing. When the `Play` ends, the browser is shown again
on the page it left.

## Every screen shows every library

Nothing scopes a `Player`'s screen to a subset of the namespace's
libraries. A screen that should show fewer libraries needs a namespace
of its own. See
[The namespace is a boundary](https://liken.sh/library/docs/guides/libraries/#the-namespace-is-a-boundary).
