# The wordmark takes the style of an intent mark

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`layout.html` in `crates/transect-frame/src/frame/` gives the wordmark
image in the masthead `class="mark"`, and links `intent/intent.css` on
every page. `intent.css` in `crates/transect-resources/src/intent/`
styles `.mark` for the provenance marks of
`crates/transect-graph/src/edges/provenance.html`: padding, a 1 px
border, a border radius, and `cursor: help`. `layout.css` sets only
`flex: none` on `.mark`. So the logo in the masthead gets a border, a
padding of 4 px, and the help cursor.

## What would confirm it

Open any page in a browser and inspect the wordmark image. The
computed style has the border and the cursor from `intent.css`.

## What a fix has to decide

Nothing beyond giving one of the two its own class name.
