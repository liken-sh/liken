# 27, Drop stale trickplay requests

Built, and drilled on `liken-1` and in the house on 2026-09-08 in
release 2026.09.08-001. A scrub sent the bridge a queue of tile
requests it could not keep up with, and the display kept drawing tiles
after the hand lifted.

## The problem

A held left or right on the scrubber feels heavy, and the tile keeps
changing after the release. A queue causes the continued updates.

[`trickplay.lua`](../../display/trickplay.lua) asks the bridge for a tile
each time the cursor crosses a whole second. A keyboard-class remote
autorepeats at the kernel's rate, about thirty events a second, and
[`scrubber.lua`](../../display/scrubber.lua) ramps the scan to three
hundred seconds of film per second of hold. So a hold sends about
thirty requests a second, and at full speed nearly every one names a
new tile.

[`command.go`](../../command.go) serves those requests one at a time, in
order, off a channel of sixteen. Each one crops and scales a cell,
writes a file, and replies. If the bridge does not have a sheet named by
a request, it decodes the whole sheet first. The requests arrive faster
than the bridge answers them, so the channel fills, and every reply
lands on the display and redraws the tile. After the release the bridge
still works through what queued, and the display shows each of those
tiles in turn. The queued work causes the continued updates. The bridge
already answers a request for the tile it has without a crop, but it
still answers, so the display still redraws.

## The design

The display keeps one request in flight. When the reply comes back, it
sends the latest pending request. The bridge serves the newest request
in its queue and drops the rest.

* **The display.** `trickplay.lua` stores the key of the request in
  flight and the latest pending request. A sync while a request is in
  flight updates the pending request and sends nothing. The reply
  clears the in-flight key. It sends the pending request when that
  request differs from the reply. A hold sends at most one request per
  round trip. The last reply is always for the newest position, and the
  display does not replay a sequence of queued tiles after the release.
  A request that draws no reply, because the item has no trickplay or
  the sheet is missing, would hold the gate closed forever. So a new
  item and a resize clear the in-flight key, and a timer of one second
  clears it as well.

* **The bridge.** `serveMessages` drains the channel before it serves
  a trickplay request and keeps the last trickplay request it finds.
  It still serves the exit press and the presentation request in order.
  This is the second guard: a display that sends a burst, an old display
  against a new bridge, or the round trip itself, never fills the queue.

* **The request key.** The display's request key combines a
  whole-second position and the pixel box. The bridge maps the position to a
  tile. The display does not know the tile interval, so it compares
  requests with its own key rather than with a tile number. The
  one-in-flight gate makes extra requests within one tile cost one round
  trip each, not a crop. The bridge answers those requests from the
  tile it holds.

The scan speed and the ramp do not change. The tile at full speed
skips cells, which is what a fast scan should show.

## What does not change

The bridge keeps one decoded sheet at a time. A scan that crosses a
sheet boundary at full speed still decodes the next sheet once. The
one-in-flight gate means it decodes no sheet the scan has already left.

## The proof

* Unit tests in `command_test.go` for the drain: a channel with three
  trickplay requests and an exit press between them serves the press
  and the last trickplay request only.
* A Lua test of the gate: three syncs during one in-flight request
  send one request, and the reply sends the last want.
* A drill on `liken-1`: hold right through a film at full speed and
  release. The tile stops on the release, and the bridge's log shows
  no crops after the release.
