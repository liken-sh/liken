# 27, One tile in flight

Proposed. A scrub sends the bridge a queue of tile requests it cannot
keep up with, and the display keeps drawing tiles after the hand lifts.

## The problem

A held left or right on the scrubber feels heavy, and the tile keeps
moving after the release, as if it had inertia. The cause is a queue.

[`trickplay.lua`](../display/trickplay.lua) asks the bridge for a tile
each time the cursor crosses a whole second. A keyboard-class remote
autorepeats at the kernel's rate, about thirty events a second, and
[`scrubber.lua`](../display/scrubber.lua) ramps the scan to three
hundred seconds of film per second of hold. So a hold sends about
thirty requests a second, and at full speed nearly every one names a
new tile.

[`command.go`](../command.go) serves those requests one at a time, in
order, off a channel of sixteen. Each one crops and scales a cell,
writes a file, and replies. A request that names a sheet the bridge
does not hold decodes the whole sheet first. The requests arrive faster
than the bridge answers them, so the channel fills, and every reply
lands on the display and redraws the tile. After the release the bridge
still works through what queued, and the display shows each of those
tiles in turn. That is the inertia. The bridge already answers a
request for the tile it holds without a crop, but it still answers, so
the display still redraws.

## The design

The display keeps one request in flight and sends the newest want when
the reply comes back. The bridge serves the newest request in its queue
and drops the rest.

* **The display.** `trickplay.lua` holds two keys: the request in
  flight and the request wanted. A sync while a request is in flight
  updates the want and sends nothing. The reply clears the in-flight
  key and sends the want when it differs from the reply. So a hold
  sends at most one request per round trip, the last reply is always
  for the newest position, and nothing plays back after the release.
  A request that draws no reply, because the item has no trickplay or
  the sheet is missing, would hold the gate closed forever. So a new
  item and a resize clear the in-flight key, and a timer of one second
  clears it as well.

* **The bridge.** `serveMessages` drains the channel before it serves
  a trickplay request, and keeps the last trickplay request it finds.
  The exit press and the presentation request are still served in
  order. This is the second guard: a display that sends a burst, an
  old display against a new bridge, or the round trip itself, never
  fills the queue.

* **The request key.** The display quantizes to a whole second and the
  bridge maps the second to a tile. The display does not know the tile
  interval, so the second stays the key. The one-in-flight gate makes
  the extra requests within one tile cost one round trip each, not a
  crop, and the bridge answers those from the tile it holds.

The scan speed and the ramp do not change. The tile at full speed
skips cells, which is what a fast scan should show.

## What does not change

The sheet decode stays one sheet held at a time. A scan that crosses
a sheet boundary at full speed still decodes the next sheet, once, and
the one-in-flight gate means it decodes no sheet the scan has already
left.

## The proof

* Unit tests in `command_test.go` for the drain: a channel with three
  trickplay requests and an exit press between them serves the press
  and the last trickplay request only.
* A Lua test of the gate: three syncs during one in-flight request
  send one request, and the reply sends the last want.
* A drill on `liken-1`: hold right through a film at full speed and
  release. The tile stops on the release, and the bridge's log shows
  no crops after the release.
