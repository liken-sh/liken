# The presentation contract

Plan 07-c, the third slice of [plan 07](07-the-player-draws-its-own-display.md).
It gives a `Play` a way to declare how each item should look, and the
display renders that declaration. When this slice lands, a `Play`
carries `spec.items`, each with a `presentation` block, the display
fills each field from the `Play` before the file, and a `series` item shows
its series, season, and episode.

## The problem

07-a and 07-b draw the display from what `mpv` reads out of the file.
That is enough for a scrubber and the track choosers, but it cannot
tune the display by media type, cannot correct a wrong title, and
cannot carry the hierarchy a `series` item needs, because none of that is
in the file. A `Play` needs a way to declare it, and the display needs
a rule for when to trust the declaration over the file. This slice adds
both, for the fields that are text. The art fields wait for 07-d.

## Items replace the URI list

A `Play` carries `spec.items`, and each item is a `uri` and an optional
`presentation` block. This replaces `spec.uris`. A bare `uri` with no
`presentation` is the short form. The parallel-list shape, a
`presentation` list beside `uris`, was set aside: two lists that must
stay index-aligned can drift, and one list of items that each carry
their own `uri` cannot.

The pod still plays the whole list as one gapless playlist, because the
pod holds the display and sink claims for the session. So the item is
an entry in a list, not its own resource: a per-item resource would
turn each item into its own pod and release the claims between items,
which is a black screen and a re-negotiated sink at every boundary.

## The presentation block carries what mpv cannot read

The block carries only what `mpv` cannot already read from the file.
This slice defines its text fields:

* `type`, one of `video`, `music`, or `image`, and `hint`, one of
  `movie`, `series`, or `album`. `mpv` cannot infer these, and the display
  must not guess them from a file name.
* The `series` hierarchy: the series name, the season number, the episode number,
  and the episode title.
* A `title` override, for the item's name when the file's tag is wrong
  or absent.
* A `year`, the release year, and a `date`, the air date of an episode.

The art and trickplay fields are named in the parent design and added
in 07-d and 07-e, so the schema this slice writes leaves room for them.

## The display resolves each field in three tiers

The display fills each field from the first source that has it: the
`presentation` block, then an `mpv` property, then nothing. A title is
`presentation.title` if the `Play` gave one, else `media-title` from
the file, else absent. A field that resolves to nothing does not draw,
with no placeholder.

The `Play` comes first on purpose. A library that feeds `liken`
supplies full, correct metadata, and the display trusts it over the
container's tags. The tags are the fallback for a loose file no library
described. There is no fourth tier that parses a file name, because
that parsing is the library's job.

## The blocks travel in the pod, and the sidecar swaps them

The operator writes every item's block into the pod when it creates the
pod, because the blocks are known then, the same as the playlist. The
command sidecar already watches `mpv`'s `playlist-pos`, so it holds the
current item on its own. It hands that item's block to the display over
the IPC socket, and on an advance it hands the next. The block travels
as text, so it needs no decode.

A live queue that changes the list while it plays is the case this does
not cover, because a pod cannot gain an item without a restart. The
live queue adds a channel from the operator when it arrives. Nothing
changes the list mid-run today, so that channel is not built now.

## The header tunes by type

`header.lua`, the top-left region, arrives in this slice and draws from
the resolved fields. A `movie` shows the title. A `series` item shows the
series name, and a line beneath it with the season, the episode number, and
the episode title. The header draws text only here; the `logo` art that
replaces the title for a `movie` waits for 07-d.

The `image` type shows the photo, which is `mpv`'s own video track, and
draws no scrubber. Its `left` and `right` move across the items, which
is the first use of the multi-item list for navigation.

## Set aside for this slice

* **The art and the trickplay.** The block leaves room for them, and
  they arrive in 07-d and 07-e with the bridge decode.
* **The music layout.** `music` selects `mpv`'s default cover-art
  framing for now. The composed music experience is 07-f.
* **The common-ancestor mount.** The resolver change that art URIs need
  arrives with the art in 07-d.

## How it will be proved

On `liken-1`, with a studio monitor as the `Player` and a paired
DualSense. A `Play` runs with two items, a `movie` and a `series` episode,
each with a `presentation` block.

The drill checks each claim:

* The `series` item shows its series, season, and episode from the block, not
  from the file. The header tunes by type.
* An item whose block gives a `title` shows that title, and an item with
  no block shows the file's own title. The three-tier resolution holds.
* The `Play` advances from the first item to the second, and the header
  swaps to the second item's presentation. The block travels per item.
* A field the block leaves empty and the file lacks draws nothing, with
  no placeholder.
