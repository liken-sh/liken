---
title: Put the media browser on a screen
weight: 50
description: "Put the media browser on a Player's screen in place of the idle screen, and learn its keys, its screens, and how it starts playback. Use when a Player should show the namespace's catalog and start a Play from a remote."
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

The operator also gives the browser the `Player`'s language
preferences: `audioLanguages`, `subtitleLanguages`, `subtitles`, and
`sdh`, each from the `Player` where the `Player` states it and from the
default `MediaPreferences` where it does not. That is the order in which
`media-operator` resolves them for a `Play`. The browser reads them from
the variable `LIBRARY_MEDIA_PREFERENCES` and marks the tracks a play
takes on a movie's page (section 5). A change to them replaces the pod.

    kubectl -n media get pod living-room-media-browser \
      -o jsonpath='{.spec.containers[?(@.name=="browser")].env[?(@.name=="LIBRARY_MEDIA_PREFERENCES")].value}'

## 3. How presses reach it

The `Player`'s `status.idle.bus` names the bus topics for the
`Player`'s remotes. The browser subscribes to each remote's events
and its retained focus mark, and answers a press only while the mark
names this `Player`. Starting a `Play` moves the mark to the `Play`'s
`Player`. A `Play` ending moves nothing, because the `Player` it names
is still there, showing its idle screen.

When nothing plays and no press arrives for the `Player`'s
`fadeAfterSeconds`, the browser draws a black shade over the catalog.
The press that lifts the shade also acts on the screen under it, so
the first press of down moves focus and the first press of select
opens the card in focus. Back and backspace only lift the shade. The
browser never lowers the shade on back, but the stock idle
screen does, and one rule for every screen keeps a press of back from
undoing the wake there. When three hours have passed with no press
and the cluster holds a `Person`, that first press raises the person
picker instead, because the answer to who is watching has lapsed.
When the panel is off, because
`offAfterSeconds` ran out or the `Player` turned off, the first press
only lights the screen, because nobody could see what it would act
on. A remote that sends no key for the press that wakes it, such as a
T6, lifts the shade and does nothing else on that press, as
[When a remote wakes from sleep](https://liken.sh/bluetooth/docs/guides/pair-a-controller/#when-a-remote-wakes-from-sleep)
in the `bluetooth-operator` manual describes.

Over the bus, every key a remote sends reaches the browser under the
kernel's name, except the cycle key and the power keys.
`media-operator` reads the volume and mute keys from the same topic and
changes the level, and the browser binds nothing to them. The browser
draws the level as a fading row. The level arrives on the `Player`'s volume topic. When
its payload carries `"indicator": "receiver"`, the receiver draws the
level on the TV, so the browser draws no row. A payload with no
`indicator` field means the browser draws the row. So a remote with a keyboard
types into search, and its home and search buttons work once a
`Keymap` names them. The same keys reach the browser from a keyboard
attached to the screen's machine.

## 4. The keys

| Key | Effect |
|---|---|
| arrows | move focus |
| enter | open the card, open an episode's row, or press a button or a mark of a title's page |
| back | close an episode's row, or pop to the screen before this one. On a row the page opened on, pop to the screen before this one |
| home | pop to the home page |
| search | open the search wall with the on-screen keyboard |
| a letter or a digit | open the search wall with that character typed |

A green outline marks focus. On every screen, the strip, and the
person picker, the outline travels to the next card, button, or mark
on each press, and the row scrolls under it on the same press. While
focus rests, the outline brightens slowly every few seconds, so you
can find it from the couch. When a press opens a page or goes back,
the outline starts on the new page's focus with no travel. While the
loading screen covers a title's page, the outline is hidden.

Up from the top of any wall puts focus on the strip, the row across
the top that holds the clock and the search glass. On a title's page,
a shade darkens the top of the frame under the strip: lightly over the
backdrop, and fully once the page scrolls its text and faces up under
the strip, so the clock and the glass draw over a darker ground. Right from the last
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

When you choose people while focus is at the top of the home page, on
the banner or the first row, focus moves to their continue-watching
row when it comes in. Further down the page, focus stays where it is.

A home card that names one episode opens the series' page with that
episode's row already open, and focus on its first button, Resume or
Play. The continue-watching row, the recently added row, and the banner
all open an episode this way, so the play is one press from the card.
Behind the row, the wall stands at that episode's season, with the
episode's still selected and scrolled into view, as if the person had
moved to the still and pressed select. Down from the row reaches the
marks, and down again lands on that still. Back from that row returns
to the home page in one press, because the wall under it was never on
the screen. Once focus leaves the row for the wall, back closes a row as
it does on any series page. A card that
names a series and no episode opens the page on its wall.

A play that stands at position 0 is not started. The row draws no card
for it, and its series, set, or franchise offers nothing after it, so
the whole container leaves the row. A cleared title writes that play,
and so does a title marked unplayed in Jellyfin. A play that starts
over and stops before its first position report reads the same way.

The answer to who is watching lasts until three hours pass with no
press. The browser keeps it on the bus, retained, so a screen pod that
restarts inside those hours draws the same room and asks nobody.
[The library bus](/docs/reference/bus/#who-is-watching) gives the
message.

Every film poster shows who at the screen has seen the film, so you can
pick a film that nobody has to watch again, or one that everyone has
started. This covers the walls, the rows of the home page, the strips of
a title's page, and the franchise page. The bar under the poster shows
how far everyone at the screen got. Each person's share is their
furthest play of the film, alone or with anyone, and a play past the
finished point counts as the whole film. A clear takes back that
person's earlier plays, so only the plays after it count. The bar shows the least of
those shares, so a film that one person has not started draws no bar.
When two or more people are at the screen, a face for each of them who
has a play of the film draws along the foot of the poster. A person with
no picture draws as the first letter of their name. A series poster
draws neither.

A movie's page shows its art, its facts, its people, and the set or
franchise it is part of. Under the plot, the playback row holds Play,
or Resume and Start over for a film the people at the screen are in the
middle of, and Trailer where the film has a trailer file. Every button
of the row starts a play. Beside the row, the page gives the time a
play from the first button would end at the clock's reading, as in
`ends 10:42 pm`: the end of the rest of the film for Resume, and of the
whole film for Play. A row too wide for the text column to hold the
time after it leaves the time out.

Under the playback row, a bar as wide as the text column shows how far
the people at the screen reached. Under the bar, the status line gives
where they stand, and the marks of
[section 6](#6-marking-a-title-watched-or-clearing-it) follow it:

| Where they stand | Status line |
|---|---|
| Not started | `Not started · 1h 39m` |
| In the middle of the film | `0:13:07 watched · 1h 26m remaining` |
| Finished | A check, then `Watched` |

The position has hours in a title an hour or longer, as in `0:13:07`,
and minutes and seconds in a shorter one, as in `21:40`. Down from the
playback row puts focus on the first mark, left and right move between
the marks, and up returns to the playback row. Down from the marks
moves on to the set strip, the franchise strips, or the cast. While a
mark has focus, the bar is twice as tall and the status is brighter.

The end of a movie's page names the studios, and then the Files
section shows each video file of the film, so you can see which copy a
play takes and what it plays with. A film with two copies, such as a
4K remux and a 1080p encode, shows them side by side. Each file shows a
short name, its quality tag from the file name, such as `Bluray-1080p`,
or its resolution, such as `4K`, its container, and its size, as in
`MKV · 14.3 GB`. Under the name are the frame, the codec, the dynamic
range, and the frame rate, then a row for each sound track and each subtitle track. A subtitle row counts the
track's lines and names its role: `SDH`, `forced`, or `commentary`. The
subtitle files beside the video that mpv loads are rows too, and a track
with no lines is left out.

A dot marks the file a play takes, and in it the sound track and the
subtitle track the play takes. The file is the first video file of the
film by path, the same one the play request names. The sound track is
the first in the order of the `Player`'s `audioLanguages`, by the
default flag among tracks of one language. The subtitle track is the
one `media-operator` chooses by role, under `subtitles: on` or `auto`,
as
[How the subtitle track is chosen](https://liken.sh/media/docs/reference/mediapreferences/#how-the-subtitle-track-is-chosen)
describes.
With `subtitles: off`, or with no mode stated, no subtitle row has a dot.
A `Play` that states its own preferences can take other tracks than the
page marks, because the page reads the `Player`'s preferences before any
`Play` exists.

A series' page shows its seasons as a wall of episode stills. A series
whose folder holds no file with an episode number in its name, as
[Scanning](/docs/guides/scanning/) describes, shows a line that says so
in place of the wall. Enter on
a still opens the episode's own row in the header, level with the
ratings line. The row holds the playback buttons of a movie's page
except Trailer, the time a play from its first button would end beside
them, and the bar and the status line under it. Down from
the row puts focus on the marks, down from the marks returns to the
still, and back closes the row.

Pick up here follows the playback buttons on an episode's row. It marks
every earlier episode of the series watched and clears every later
episode, for the people at the screen, and plays the episode from the
beginning, as Play does, with the same next episode after it. The
earlier episodes are every episode before this one in season and
episode order, across seasons, and the later episodes are every episode
after it. The press marks the earlier ones the people at the screen
have not finished, a partly watched one included. It clears the later
ones they started or finished, as Clear progress on each row would, and
leaves the later ones they never started. It leaves the specials of
season 0 alone. The row shows the button only where the press changes
at least one episode: an earlier one is still to mark, or a later one
holds progress. So a special does not show it, and neither does an
episode with every earlier episode finished and no later one started.
Its glyph is a bar with a triangle that points away from it.

A person's page shows when the person worked, what they did, and what
the people at the screen have seen of it. Under the name, a line gives
the dates of the life, the credits and the years they span, and how
many of them everyone at the screen has seen, as in
`born 1970 · 31 credits from 1985 to now · you've seen 9`. A credit
with no release date is counted after the others, as in
`, and 3 undated`. The biography follows, cut to 3 lines.

Under the head is the career axis, a timeline of every credit with a
release date. Each credit is a dot at its release year, and the credits
of one year stack. An open ring is a credit nobody at the screen has
seen, a ring with its left half filled is one that some of them have
seen, and a full dot is one that everyone at the screen has seen. A
person has seen a film when they finished it, and a series when they
finished every episode the library holds. When any one year has more
than 5 credits, the axis draws one bar per year instead: the bright
part at the bottom is what everyone has seen, and the lighter part over
it what only some have seen. The axis starts at the birth when the
first credit came within 25 years of it, and is labeled "born" there.
Otherwise the line fades in over the 3 years before the first credit.
It ends now when the person has a credit in the last 2 years. A death
inside the span is a tick labeled with the year and the age, and the
line is dimmer after it. A series still running is a bar from its
first year to now. The brighter part of the line spans the years of
the posters the row shows.

Under the axis is one row of posters, oldest at the left and the
credits with no release date at the right end. The posters take the
height that the head and the axis leave, and the row holds as many as
fit across: six on a 16:9 screen, and more on a wider screen, such as
eight on a 2560 by 1080 screen. Under each poster, the
first line is the character the person played, or the job, such as
`Director`, and the second is the year and the age, as in
`1986 · age 15`. A work released after the death reads
`1996 · 380 years after death`. With no birth date, or with a birth
date that comes after one of the credits, no card shows an age.

The page opens on the work whose page you came from, or on the newest
credit. Left and right move one poster. The dot of the poster in focus
draws in green and keeps its shape, and in the bar mode a short green
stroke under the bar marks its year. Up moves focus to the axis, on
that poster's year, and a label over the year gives its count, as in
`2009 · 11 credits · 3 seen`. Left and right on the axis move to the
previous or next year with credits, and the row shows that year's
credits from its left edge. Down or enter returns to the posters: to the
poster you left when you made no move on the axis, and to the year's
first credit when you did. Up from the axis moves to the browser's
strip. A person with fewer than 5 credits with a date, or with credits
over less than 5 years, has no axis, and up from the row moves to the
strip.

A franchise's page draws its story order as one lane, with a line per
universe beside it. Over the first row of each era it draws a heading,
with the era's length beside its name. An era inside a wider one reads
as a smaller line under it. While the wall scrolls inside an era, one
line held over the cards names the eras around it, outer to inner. Left
and right jump an era at a time. The wall moves only when the row in
focus would leave the screen.

Search is a wall like any other. Every typed character rereads it.
The heading over the wall reads the text and the count of hits, such
as `live · 365`. It reads `Search` while the field is empty and `No
titles match` when the text matches nothing.
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

The status line of a movie's page and of an episode's row holds the
marks that apply to the title, after the status. Each mark applies to everyone at the
screen, the same people the continue-watching row is read for, and it
takes effect at once, with no confirmation step.

| Mark | Where it shows | Effect |
|---|---|---|
| Mark watched | on a title they have not finished | The title counts as finished. A film leaves the continue-watching row. In a series, a set, or a franchise, the next title takes the card. |
| Clear progress | on a title they started or finished | The title counts as not started, and its whole series, set, or franchise leaves the row. |

The actions of a continue-watching card are on the page the card
opens. A film's card opens the film's page, and an episode's card
opens the series' page with that episode's row open.

Pick up here on an episode's row, in [section 5](#5-the-screens), marks
every earlier episode watched and clears every later one in one press.
The browser publishes one mark that lists those episodes, each with its
own mark, and the progress role writes a row for each of them. An
earlier episode with no duration in the audience's play or in the
catalog is left out, as a single watched mark on it sends nothing. A
later episode the press cleared never takes the series'
continue-watching card from the episodes the press marked watched, or
from the play of the picked episode.

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
a `Play` records moves the bars, the status lines, and the
continue-watching row the same way. A change to a title the screen
does not show reads nothing more.

Neither mark deletes a play. The store keeps the earlier plays, but
the browser reads the newest play of a title, so after a clear the
screen offers no Resume and draws no position for the title. A later
play of the title replaces either mark.
[The library bus](/docs/reference/bus/#a-mark) gives the message, and
[a list of episodes](/docs/reference/bus/#a-list-of-episodes) gives the
form Pick up here publishes.

A play counts as finished when the time left is at or under a twentieth
of the work, and at or under five minutes, whichever leaves less time.
Where the credits marks of the title's file place the credits in the
second half of the work, the play counts as finished from the start of
those credits instead. The browser reads the marks from its catalog,
the [estimated marks](/docs/guides/enrichment/#estimated-marks) among
them.
The jellyfin role writes a title played in Jellyfin by the same rule,
and [Keep progress with Jellyfin](/docs/guides/jellyfin/#3-what-crosses)
states it in full. Where the `Catalog` names a Jellyfin server, the
jellyfin role also sends each mark to Jellyfin.

## 7. Playback

Enter on a title resolves what to play from the browser's own copy of
the catalog. It then publishes the list on the bus, as a play request
on `liken/library/players/{namespace}/{player}/play`. The operator names
the topic on the browser container as `LIBRARY_PLAY_TOPIC`, and
[The library bus](/docs/reference/bus/#the-play-request) gives every field
of the request. The operator turns it into a `Play` for the `Player`,
with each item's path as a claim reference, and `media-operator` runs
it. The `Play` is named after the title, so `kubectl get plays` reads
like a listing. When the `Play` ends, the browser is shown again
on the page it left.

## Every screen shows every library

Nothing scopes a `Player`'s screen to a subset of the namespace's
libraries. A screen that should show fewer libraries needs a namespace
of its own. See
[The namespace is a boundary](/docs/guides/libraries/#the-namespace-is-a-boundary).
