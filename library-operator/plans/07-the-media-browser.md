# The media browser

Plan 07. The client's second view: libraries on the screen, and the
structure each kind gives. At the end of this plan a person in a room
presses a button, sees the libraries, walks into one, and reaches a movie
or an episode. Nothing plays yet.

## The problem

A catalog nobody can see is a database. The media browser draws it on a
television, for a person ten feet away, driven by a remote with a
handful of buttons, and it draws every kind without a built-in notion of
any kind's shape. This plan is the smallest media browser that lets a
person find a title. Rows and recommendations come later.

## The contract

**Views.** The client has an idle view, plan 05's screen, and a browsing
view. A press on a remote that names this `Player` opens the browsing
view. Back from the top level, or the idle timer, returns to the idle
view. The client publishes its view on the bus so the `Player` status
can show it.

**Libraries.** The first screen lists every `Library` the client can
see. In this plan that is every library in the catalog, because the
resource that binds screens to libraries is an open problem. Each entry
shows the library's name, kind, and count.

**Kinds give structure.** The media browser reads a kind's structure
from the catalog and draws one of a small set of views.

- A movies library is one list of titles, sorted by the scanner's sort
  key, drawn as a wall of posters with the focused title larger and its
  name beneath it. The head-to-head drew a wall of five thousand at 60
  frames a second with a bounded decode cache, and those numbers are the
  floor.
- A series library is a list of series, then that series' seasons, then
  that season's episodes, each level a list with its own art.
- A later kind brings its own levels. The media browser draws lists and
  walls it is given, and has no code per kind.

**Navigation.** Up, down, left, right, select, and back arrive as
commands on the bus, mapped by `media-operator` from whatever controller
is bound. The media browser owns focus, because there is no keyboard or
pointer for the toolkit to own it.

**Freshness.** The media browser reads from its local file and re-reads
what the update stream names, so a title that arrives on the volume
appears on the wall while the wall is open.

**Art.** Posters and backdrops are read from the volume at the paths the
catalog stores, decoded and scaled to the size they are drawn at, and
cached with a bound. The library's volume is mounted read-only into the
idle pod for that, through the mounts field from plan 06.

## The local harness

`local/browse` runs the media browser in a window against the
workstation's catalog and takes key presses from the keyboard, mapped to
the same command names used on the bus. The scripted timeline and
capture flags let an agent see a frame without a screen.

## What was set aside

Search, filters, and rows like "recently added" and "continue watching".
They belong to the open problem on what a screen shows and to the
watch-state plan. A person can find a movie without them.

A web view. A web page has no place on a ten-foot screen driven by six
buttons.

## Proof

On `liken-1`: from the idle view, a press on the room's remote opens the
libraries. The movies library opens as a wall with the right count, focus
moves with the arrows, and the focused title's name is right. The series
library opens to series, then seasons, then episodes, checked against
one series by hand. Back returns through each level to the idle view.
The media browser's resident memory on the box during a full scroll of
the wall is recorded beside the head-to-head's 115 MB.
