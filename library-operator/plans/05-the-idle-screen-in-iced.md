# The idle screen in Iced

Plan 05. The first plan of this sequence, and the one that builds no
code in this repository. `media-operator` gains a third image, a native
client written in Rust and Iced that draws the idle screen it draws
today in `mpv` and Lua. Every plan after this one draws on a screen that
client owns.

The work is `media-operator`'s, and [plan
20](https://github.com/liken-sh/media-operator/blob/main/plans/20-the-idle-screen-is-its-own-image.md)
in that repository states it. This document states why the library layer
asked for it and what it needs the result to be.

## The problem

The idle screen is an `mpv` with no file. It holds a Wayland surface,
runs a Lua overlay under `display/`, and takes every fact it draws from
an idle-command sidecar that reads the bus and writes into `mpv`'s IPC
socket. That works, and it costs three things the library layer cannot
carry forward.

A video player is holding a window open to draw a clock. It decodes
nothing while it does, and every fact it draws arrives through a socket
protocol another project defines.

A surface it cannot show again. Weston's kiosk-shell reveals a lower
surface only along a code path gated on a seat, and `liken`'s compositor
has none, so the sidecar makes `mpv` destroy and rebuild its surface to
bring the clock back after a film. The client cannot ask for that
itself, because it is `mpv`.

Two toolkits for one television. The media browser this repository
builds is a native client, chosen in a head-to-head that drew this exact
screen. A house that runs a Lua overlay for the clock and a Rust client
for the library has two looks, two layout systems, and two places a
brand colour has to change.

## What the library layer needs

The library layer does not replace the idle screen. A `Player` runs
`media-operator`'s idle image, and this repository adds a second image
for the browser: it lists what the libraries hold, takes a choice, and
starts a `Play` that `media-operator`'s player image plays. The idle
screen is what the television shows before and after that.

So this plan asks for three things, and plan 20 delivers them.

- The idle screen is its own image, `media-operator-idle`, which the
  operator ships and a `Player` can override through `spec.idle.image`.
  The browser needs that field for the plans below, and the override is
  what makes a screen replaceable at all.
- The client reads the bus. Every fact the screen draws is a bus fact or
  a sidecar decision, so a client that draws needs a broker and no
  socket. The browser reads the same topics when its turn comes.
- The look comes from `brand`, through a Rust crate both clients take as
  a dependency. One palette, one mark, one motion, parsed from the
  brand's own files.

## The look is shared, and one half of it cannot be

`brand` carries the crate at `iced/`. It parses `liken.svg` for the
fourteen hexagons of the mark and `liken.css` for the colour tokens, so
a value is never copied and cannot drift. The idle client takes it, and
the media browser in this repository takes it for the same reason.

The playback overlay stays in Lua, because `mpv` draws it over its own
frames and nothing else can. Its palette is written out in
`display/theme.lua` as ASS colours, and Lua reads no Rust crate, so
those values stay in step with the brand by hand. `theme.lua` says which
token each of its colours is, and that comment is where a person looks
when a brand colour changes.

## What the screen shows

The port changes nothing a person sees. Plan 20 holds the table of
numbers: the mark's two sines a hexagon and its ten percent swing, the
1200 and 2500 ms energy ramps, the 4000 and 400 ms shade, the 120 and
500 ms beats of the identity block, the 350 and 600 ms volume fade, and
the four-second hold. Each one is `display/`'s own, and each one is a
test.

## Proof

Plan 20 carries the drill on `liken-1`. This repository's proof is the
plans that follow: plan 06 puts a Corrosion sidecar beside a client that
is already native, and plans 07 and 08 draw a browser with the same
crate, on the same bus, over the same claim.

## What was set aside

The media browser as the idle screen. One image would draw both views,
and the idle screen would then ship from the repository that knows about
libraries. A television with no library still shows a clock, so the idle
screen belongs to the operator that owns the screen.

A Rust crate in this repository holding the palette and the mark, with a
test against `brand` to catch drift. A dependency that cannot drift is
better than a test that reports drift.
