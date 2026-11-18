---
title: The arrangement protocol
weight: 35
toc: true
---

# The arrangement protocol

`liken_arrangement_v1` is a Wayland protocol that lets one client of a
claim place the claim's own windows inside the claim's region. For
example, a video player can shrink its film into a frame when the
credits start and slide a panel in beside it, all inside one region.

The `Layout` that a `Display` names sets each claim's region, and no
workload can move a region. The [layout guide](/docs/guides/layout/)
shows how. Inside the region, the claim's arranger places the windows.
The arranger is the first client of the claim that asks for the
protocol. An arrangement never moves a window outside the region and
never scales one above the region's size. When a claim has no
arranger, each of its windows fills the region.

The protocol's XML is in the repository at
[`weston/layout/liken-arrangement-v1.xml`](https://github.com/liken-sh/liken/blob/main/weston/layout/liken-arrangement-v1.xml).
Generate your client's bindings from it.

## Binding the protocol

The compositor module, `liken-layout.so`, advertises the global
`liken_arranger_manager_v1` on every socket. Bind it on a connection
to the socket the claim gave your container, which is the socket that
`WAYLAND_DISPLAY` names, and call `get_arranger`. The compositor
replies with one event:

- `granted` when the claim has no arranger. A `region` event follows
  with the region's size in logical pixels. Then the compositor sends
  one `surfaces` event for each app_id among the claim's windows, with
  the number of windows that have it.
- `refused` with the reason `busy` when another client already
  arranges the claim, or with the reason `not_a_claim` when the
  connection did not arrive on a claim's socket.

The compositor identifies a claim's clients by the socket they
connected to, so any connection to the claim's socket can arrange. The
simplest choice is the connection that your program's own window uses.
When a toolkit owns that connection, as winit does under iced, you can
get the `wl_display` through `HasDisplayHandle`. Then
`Backend::from_foreign_display` in `wayland-backend` binds the global
on that connection with an event queue of your own, and the toolkit
keeps drawing.

The compositor sends `region` again when the operator places the
region at another size, and `surfaces` again when a count changes. A
count of zero means that the last window with that app_id is gone.

## Naming windows by app_id

An arrangement selects windows by their xdg app_id, the string that a
client sets with `xdg_toplevel.set_app_id`. Set a different app_id on
each window that you want to place separately. mpv sets one with
`--wayland-app-id`. A subsurface has no app_id of its own and moves
with the window it belongs to, so a player's subtitles move with its
film.

An entry moves every window of the claim that has its app_id, so two
windows with the same app_id always move together. `status.arrangers`
on the `Display` lists each app_id that two or more of the claim's
windows have, under `duplicateAppIDs`.

A window that no entry selects keeps the default placement. It fills
the region, is fully opaque, and is stacked below every selected
window, in the order the windows arrived.

## An arrangement

An arrangement is a list of entries. You send each one with `entry`
and apply the list with `commit`. An entry has these arguments:

| Argument | Meaning |
| --- | --- |
| `app_id` | The app_id of the windows that this entry places. |
| `x`, `y`, `width`, `height` | The rectangle, in logical pixels of the region, with the region's top-left corner at 0,0. |
| `opacity` | From 0, transparent, to 1, opaque. |
| `fit` | `scale` or `resize`, below. |
| `delay_ms` | How many milliseconds after the commit this entry's move starts, so an arrangement can stagger its entries. |

The order of the entries is the stacking order, with the first entry at
the bottom. `commit` takes these arguments:

- `serial`, which the compositor returns in the `landed` event.
- `name`, which the operator shows in `status.arrangers[].arrangement`
  on the `Display`.
- `duration_ms`, the length of the move in milliseconds.
- `curve`, one of `smoothstep`, `ease_out` (for arrivals), `ease_in`
  (for exits), or `linear`.

Each window moves from where it is now to where the new arrangement
puts it. That includes a window that is partway through an earlier
move. A duration of zero moves the windows at once. The compositor
sends `landed` with the serial in the frame where every entry has
finished. When a later commit replaces a commit, the replaced commit
sends no `landed`.

The compositor ends the client's connection with a protocol error in
these cases:

- An entry's rectangle is empty or reaches outside the region.
- An opacity is outside 0 to 1.
- A duration or a delay is above 10000 milliseconds.
- Two entries of one arrangement have the same app_id.
- A fit or a curve is a value that the protocol does not define.
- The client sends an entry or a commit before `granted`.

## Fits

`scale` scales the window's content into the rectangle and never sends
a configure to the window's client. Use it for video, because the
client keeps its buffers and does not allocate new ones while it plays.

`resize` scales the window during the move. When the entry finishes,
the compositor sends the client one configure with the rectangle's
size, and it stops scaling when the client commits a buffer of that
size. Use it for a panel of text, which redraws at the new size to
stay sharp.

A backdrop is a window whose own content is a single solid color and
that has subsurfaces, such as the black window that mpv draws its
video over. Under `scale`, a backdrop keeps filling the region at its
place in the stack, and the entry's rectangle applies to its
subsurfaces. So when a film shrinks, the area around it keeps the
backdrop's color.

## When a claim loses its arranger

When the arranger is destroyed or its client disconnects, every window
of the claim moves back to the default placement over 400
milliseconds. The claim then has no arranger until a client asks for
one, and the next client to call `get_arranger` gets `granted`. A
program that restarts after a crash must bind again and send its
arrangement again.

The operator posts an `Event` on the `Display` when a claim gains or
loses its arranger. When a client disconnects while a pod that holds
the claim is still running, the operator posts the Warning
`ArrangerLost`, because that looks like a crashed client. When your
program exits on purpose, destroy the arranger first, so that the
operator posts the Normal `ArrangerReleased` instead. A commit posts
no `Event`. The [`Display` reference](/docs/reference/displays/#arrangers)
lists the fields of `status.arrangers` and every reason.
