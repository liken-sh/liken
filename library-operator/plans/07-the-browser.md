# The browser

Plan 07. The second face of the client: libraries on the screen, and
the structure each kind gives. At the end of this plan a person in a
room presses a button, sees the libraries, walks into one, and reaches
a film or an episode, with nothing played yet.

## The problem

A catalog nobody can see is a database. The browser has to show it
on a television, from ten feet away, driven by a remote with a
handful of buttons, and it has to do that for every kind without
knowing any kind's shape in advance. It also has to be modest: this
plan is the smallest browser that lets a person find a title, and
the shelves, the rows, and the recommendations come later.

## The contract

**Faces.** The client has a rest face, plan 05's idle screen, and a
browse face. A press on a remote that names this `Player` opens the
browse face; back from the top level, or the idle timer, returns to
rest. The client publishes its face on the bus so the `Player` status
can show it.

**Libraries.** The first screen lists every `Library` the client can
see, which in this plan is every library in the catalog, since the
shelves plan is not built. Each entry shows the library's name and
kind and a count.

**Kinds give structure.** The browser asks the catalog, not the
`Library`, what a kind looks like, and it draws one of a small set of
views:

- A films library is one list of titles, sorted by the sort key the
  scanner wrote, drawn as a wall of posters with the focused title
  larger and its name beneath it. The toolkit spike drew a wall of
  five thousand at 60 frames a second with a bounded decode cache,
  and its numbers are the floor.
- A series library is a list of series, then that series' seasons,
  then that season's episodes, each level a list with its own art.
- A later kind brings its own levels, and the browser's job is to
  draw lists and walls it is told about, not to know kinds.

**Navigation.** Up, down, left, right, select, and back arrive as
commands on the bus, keymapped by `media-operator` from whatever
controller is bound. The browser owns focus and never the toolkit,
because there is no keyboard or pointer.

**Freshness.** The browser reads from its local file and re-reads
what the update stream names, so a title arriving on the share
appears on the wall while the wall is open.

**Art.** Posters and backdrops are read from the share at the paths
the catalog holds, decoded and scaled to the size they are drawn at,
and cached with a bound. The share is mounted into the idle pod for
that, read-only, the same way a `Play`'s pod mounts it. Which mounts
the idle pod gets is one more thing the companions field from plan 06
carries.

## The local harness

`local/browse` runs the browser in a window against the workstation's
catalog and takes key presses from the keyboard, mapped to the same
command names the bus would carry. The harness's scripted timeline
and capture flags let an agent see a frame without a screen.

## What was set aside

Search, filters, and rows like "recently added" and "continue
watching". They are the shelves plan and the watch-state plan, and a
person can find a film without them.

A web view. The client is native for the reasons the design gives,
and a web page has no place on a ten-foot screen driven by six
buttons.

## Proof

On `liken-1`: from the idle face, a press on the room's remote opens
the libraries; the films library opens as a wall with the right count;
focus moves with the arrows and the focused title's name is right; the
series library opens to series, then seasons, then episodes, checked
against one series by hand; back returns through each level and to
rest. The browser's resident memory on the box during a full scroll
of the wall is recorded beside the spike's 115 MB.
