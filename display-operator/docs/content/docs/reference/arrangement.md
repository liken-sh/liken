---
title: The arrangement protocol
weight: 35
toc: true
---

# The arrangement protocol

`liken_arrangement_v1` is a Wayland protocol that lets one client of a
claim place the claim's own windows inside the claim's region. A
video player uses it to step its film back into a frame at the
credits while a panel slides in beside it, all in one region. The
compositor runs every move on each repaint of the output, so a move
lands on the panel's refresh and a request moves the windows in the
next frame. The [layout guide](/docs/guides/layout/) shows how a
`Layout` gives each claim its region.

A screen has two owners. The `Layout` that a `Display` names owns the
screen: the operator gives each claim a region, and no workload can
move a region. Inside its region, a claim's windows belong to one
arranger, the first client of the claim that asks for the protocol.
An arrangement never moves a window outside the region and never
scales one above the region's size. A claim with no arranger shows
every window filling its region, as it did before the protocol.

The protocol's XML is
[`weston/layout/liken-arrangement-v1.xml`](https://github.com/liken-sh/liken/blob/main/weston/layout/liken-arrangement-v1.xml)
in the repository. Generate your client's bindings from it.

## Binding the protocol

The compositor module, `liken-layout.so`, advertises the global
`liken_arranger_manager_v1` on every socket. Bind it on a connection
to the socket the claim gave your container, which is the one
`WAYLAND_DISPLAY` names, and call `get_arranger`. The compositor
answers with one event:

- `granted` when the claim has no arranger. A `region` event follows
  with the region's size in logical pixels, and then one `surfaces`
  event for each app_id the claim's windows carry, with how many
  windows carry it.
- `refused` with `busy` when another client arranges the claim, and
  with `not_a_claim` when the connection did not arrive on a claim's
  socket.

The compositor tells a claim's clients apart by the socket they
arrived on, so any connection to the claim's socket can arrange. The
simplest is the connection your program's own window uses. A toolkit
that owns its connection, such as winit under iced, hands out the
`wl_display` through `HasDisplayHandle`, and `wayland-backend`'s
`Backend::from_foreign_display` binds the global on it with an event
queue of your own, while the toolkit keeps drawing.

The compositor sends `region` again whenever the operator places the
region at another size, and `surfaces` again whenever a count
changes. A count of zero means the last window with that app_id is
gone.

## Naming windows by app_id

An arrangement names windows by their xdg app_id, the string a client
sets with `xdg_toplevel.set_app_id`. Set a distinct app_id on each
window you want to place. mpv takes one with `--wayland-app-id`. A
subsurface has no app_id of its own and moves with the window it
belongs to, so a player's subtitles move with its film.

An entry moves every window of the claim whose app_id it names, so two
windows with one app_id always move together. `status.arrangers` on
the `Display` lists each app_id that two or more of the claim's
windows carry, under `duplicateAppIDs`.

A window that no entry names keeps the default: it fills the region,
shows fully opaque, and stacks below every named window in the order
the windows arrived.

## An arrangement

An arrangement is a list of entries, sent with `entry` and applied
with `commit`. Each entry states:

| Argument | Meaning |
| --- | --- |
| `app_id` | The windows this entry places. |
| `x`, `y`, `width`, `height` | The rectangle, in logical pixels of the region, with the region's top-left corner at 0,0. |
| `opacity` | From 0 to 1. |
| `fit` | `scale` or `resize`, below. |
| `delay_ms` | How long after the commit this entry's move starts, so an arrangement can stagger its entries. |

The order of the entries is the stacking order, first at the bottom.
`commit` takes a `serial` that comes back in `landed`, a `name` that
the operator shows in the `Display`'s status, a `duration_ms`, and a
`curve`: `smoothstep`, `ease_out` for arrivals, `ease_in` for exits,
or `linear`. Each window moves from where it shows now, including a
window partway through an earlier move, to where the new arrangement
puts it. A duration of zero moves the windows at once. The compositor
sends `landed` with the serial in the frame where every entry has
finished. A commit that a later commit replaced sends no `landed`.

The compositor ends the client's connection with a protocol error for
a rectangle that is empty or reaches outside the region, an opacity
outside 0 to 1, a duration or delay above 10000 milliseconds, two
entries of one arrangement with the same app_id, a fit or curve the
enum does not name, and an entry or commit before `granted`.

## The two fits

`scale` scales the window's content into the rectangle and never
sends its client a configure. Use it for video: the client keeps its
buffers and never reallocates while it plays.

`resize` scales the window during the move, then sends its client one
configure at the rectangle's size when the entry lands, and stops
scaling when the client commits a buffer of that size. Use it for a
panel of text, which redraws at its new size to stay sharp.

A window whose own content is a single solid color and that has
subsurfaces is a backdrop, such as the black window mpv draws its
video over. Under `scale`, a backdrop keeps filling the region at its
stacking position, and the rectangle applies to its subsurfaces. So
the area around a film that steps back keeps the backdrop's color,
and the compositor keeps a solid surface under the whole output,
which the display hardware can show as the background with no
composition.

## When the arranger goes

When the arranger is destroyed, or its client disconnects, every
window of the claim moves back to the default over 400 milliseconds.
The claim then has no arranger until a client asks again, and the
next client to call `get_arranger` gets `granted`. A program that
restarts after a crash binds again and states its arrangement again.

The operator posts an `Event` on the `Display` when a claim gains or
loses its arranger. A client that disconnects while a pod that holds
the claim still runs posts the Warning `ArrangerLost`, which is how a
crashed client looks. Destroy the arranger before your program exits
on purpose: a destroyed arranger posts the Normal `ArrangerReleased`.
A commit posts no `Event`. The [`Display` reference](/docs/reference/displays/#arrangers)
gives the fields of `status.arrangers` and every reason.
