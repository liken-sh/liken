# The mark

`liken`'s icon is a patch of lichen, drawn as hexagonal tiles.

![The liken mark](liken.svg)

The pun holds at more than one level.

A lichen is not one organism. It is a fungus and a photosynthetic
partner (an alga or a cyanobacterium) living so closely that the
pair is named and classified as a single thing. That is what `liken`
is: the Linux kernel and `k3s`, each its own upstream project,
assembled so tightly that a machine boots the pair as one system.

Lichens are also pioneers. They are among the first living things to
take hold on bare rock, enduring drought, heat, and bare mineral where
nothing else will grow, and they are what begins to turn rock into
soil. `liken` starts from the same emptiness: a blank machine, bare
metal, nothing installed.

And a lichen is frugal by nature, thriving on almost nothing. `liken`
is built to run a real Kubernetes cluster inside a gigabyte of memory,
on hardware other systems would call too small.

## The tiles

Many crustose lichens, the kind that grow flat against rock, crack
their surface into small polygonal plates as they age and as repeated
wetting and drying shrinks the crust. Each plate is called an
*areole*, and a thallus built this way is *areolate*: a natural mosaic
that looks like cracked mud, dried paint, or a field of little
islands. The icon is that mosaic, one areole to a tile.

Drawing the areoles as hexagons is a second small joke. The hexagon
is the shape the Kubernetes world draws itself in, from Helm's logo
to the backdrops of community talks, so the same picture reads as
lichen on rock to a botanist and as something Kubernetes-shaped to
anyone from that world.

One tile is orange rather than green. Some of the most common rock
lichens, the *Xanthoria*, are a vivid orange, and the single warm
tile gives the mark a focal point.

The tiles grow smaller toward one edge. That part is invention, not
biology: real areoles do not reliably shrink toward the margin (the
cracking tends to start in the older center). The gradient is there
to suggest a colony still spreading into bare rock, even though real
lichens do not grow that way.

## The colors

The greens are sampled from crustose lichens on stone, deep moss
through pale sage, with the one orange tile from *Xanthoria*.

| Swatch | Hex | Name |
| --- | --- | --- |
| Deep moss | `#4a5d3a` | darkest green |
| Mid sage | `#6e8352` | the body green |
| Light sage | `#93a877` | |
| Pale sage | `#b4c49a` | lightest green |
| Xanthoria | `#e0872f` | the one warm tile |

There is no background. The mark is transparent so it works on light
and dark surfaces alike, and every tile is drawn in flat color with no
gradients or effects, so it stays legible shrunk to a favicon and
would print cleanly in one ink.

## The files

`liken.svg` is the source of truth. Everything else is derived from it
by `make`, and the derived files are committed too so that anyone can
grab a favicon or an avatar without a rasterizer installed:

* `liken.svg` — the master, for any use at any size.
* `favicon.ico` — 16, 32, and 48 pixel raster, for the browser tab.
  The website also serves the SVG itself, which modern browsers prefer
  and render sharp at any size; this is the fallback for those that
  cannot.
* `liken.png` — a 1024-pixel transparent export, sized for a GitHub
  organization avatar or anywhere else that wants a raster.

Rebuilding them needs `rsvg-convert` (from librsvg) and ImageMagick.
Edit `liken.svg`, run `make`, and commit what changes.

## Sources

The biology here is standard lichenology, drawn from:

* Irwin M. Brodo, Sylvia Duran Sharnoff, and Stephen Sharnoff,
  *Lichens of North America* (Yale University Press, 2001) — the
  standard field reference for the symbiosis and for growth forms.
* [British Lichen Society: Lichen
  Morphology](https://britishlichensociety.org.uk/learning/lichen-morphology)
  — areoles and the areolate crustose thallus.
* [Crustose lichen](https://en.wikipedia.org/wiki/Crustose_lichen) and
  [Lichen](https://en.wikipedia.org/wiki/Lichen), Wikipedia — the
  mycobiont/photobiont symbiosis and the pioneer role in primary
  succession, both with citations to the primary literature.
