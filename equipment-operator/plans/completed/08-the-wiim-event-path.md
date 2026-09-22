# The WiiM's event path

Plan 08. Built and drilled on the house cluster on 2026-09-22. The
driver subscribes to the amps' UPnP events for volume, mute, and the
transport state. The poll stays at ten seconds for the settings and the
identity that push does not reach, and it carries the evented fields
when a subscription is not live. Plan 06 left this path open as "UPnP
GENA eventing in place of polling".

## The problem

The WiiM driver polls. `Run` reads the whole command set every ten
seconds and folds the answers into its state. Two costs come with
that, and a third follows from them.

The first is latency on the level. A change made at the device, from
the app, or by a hand on the amp reaches `status.wiim` only when the
next poll runs, so the operator can report a level up to ten seconds
stale. The driver hides this for its own writes by taking the level it
just sent before the poll confirms it, but a change that is not the
driver's has no such shortcut.

The second is load. The poll is not one request; it is about ten in a
row, and it runs every ten seconds. That is a steady stream the amp's
small HTTP server carries whether or not anything changed.

The third follows. When the media side repeats a held key, the
driver's command stream and the poll's request stream share the one
small server. A request the amp drops under that load fails the
poll's first read, `getStatusEx`, and one failed poll used to be
enough to call the receiver unreachable. The hysteresis in the driver
now keeps one miss from flapping the condition, but the load that
causes the miss remains.

The amp already answers a push interface that removes all three. The
design below uses it, keeps polling only where push does not reach,
and keeps polling as the fallback when push cannot be established.

## The design

### The interface the amp offers

A WiiM is a UPnP MediaRenderer. Its description is on port 49152 and
names five services. Three of them support GENA eventing:

| Service | Event path | Carries |
| --- | --- | --- |
| RenderingControl:1 | `/upnp/event/rendercontrol1` | `Volume`, `Mute` |
| AVTransport:1 | `/upnp/event/rendertransport1` | `TransportState`, the track URI, the track metadata |
| ConnectionManager:1 | `/upnp/event/renderconnmgr1` | the static protocol list |

`PlayQueue:1` and `QPlay:1` event too, and this plan does not use
them.

A live WiiM Amp confirms the interface. A `SUBSCRIBE` with a callback
URL answers `200` with a `SID` and `TIMEOUT: Second-1801`, and the amp
then pushes a `NOTIFY` with sequence `0` that carries the full state:
`Volume`, `Mute`, and the transport state. A later change pushes a
`NOTIFY` with the sequence advanced that carries only the fields that
moved. The amp pushes a change whoever made it, so the app, the
device's own remote, and this operator all arrive the same way.

### The subscription

The driver builds the callback before it subscribes, because the amp
connects back to it:

* The callback is an HTTP listener on an ephemeral port. The host it
  advertises is the local address the amp can reach, found by dialing
  the amp and reading the connection's own source address. The
  address is discovered, never declared, so no home address enters
  the repository.
* The driver sends `SUBSCRIBE` with `CALLBACK`, `NT: upnp:event`, and
  `TIMEOUT: Second:1800`, once for RenderingControl and once for
  AVTransport. It keeps the `SID` each answer carries.
* The listener serves every subscription the driver holds. The `SID`
  on an inbound `NOTIFY` names the service it belongs to, so one
  listener and one path carry both.
* The subscription expires. The driver re-subscribes with the `SID`
  before the timeout it was granted, and it sends `UNSUBSCRIBE` when
  the client stops, so a stopped operator leaves no subscription
  behind at the amp.

### Folding an event

A `NOTIFY` body is a `propertyset` whose `LastChange` property holds
the real document escaped inside it. The driver unescapes the inner
document, parses the fields, and folds them into the same `Status`
the poll fills. The fold goes through the same `publish` path, so the
equipment state, the session's listener, and `status.wiim` do not
learn that the update did not come from a poll.

A `NOTIFY` with sequence `0` is the full state of a fresh
subscription, so it replaces the evented families. A later sequence
carries a delta, so it merges only the fields it names and leaves the
rest as they stood.

The event families carry:

* Volume and mute, from RenderingControl, in the device's whole steps
  from 0 to 100. This is the level the operator exists to hold.
* The transport state, from AVTransport. The current track stays with
  the poll. The event carries the track metadata as double-escaped
  DIDL-Lite, and the poll's `getMetaInfo` carries the sample rate, the
  bit depth, and the bit rate that the DIDL does not, so one read
  serves the track and the event serves the state.

### What still polls

Push does not reach the WiiM's own settings: the equalizer, the
output mode, the subwoofer, the timers, the Bluetooth pairing, the
presets, the input, and the device's identity and network from
`getStatusEx`. Those change slowly, so the poll stays, at ten seconds,
and fills every value the events do not carry.

The poll reads the same command set it reads today, so it still fills
the evented fields when no subscription is live. While a subscription
is live, the poll does not apply its own reads of the evented fields.
An event can arrive while a poll is in flight, and the poll's read of
`Volume` or `TransportState` was taken before that event; writing the
older value would undo a change the amp already pushed. The poll
copies the evented fields from the state the events maintain just
before it reports, so a poll never undoes an event.

### Reachability and the fallback

Reachability keeps its own path and does not hang on the event
stream, because the amp pushes only when something changes and sends
no heartbeat. The poll's `getStatusEx`, every ten seconds, is the
probe, and the three-miss hysteresis already in the driver decides
when to call the receiver unreachable.

A subscription that cannot be established or renewed counts as a
failed command and a miss. When the amp refuses the subscription, or
the callback address is one the amp cannot reach, the driver keeps
the poll-only behavior it has today, and a metric counts the
fallback. The state path is the same in both cases, so nothing else
changes.

### What this changes in the code

* `wiim/events.go` is new: the callback listener, the `SUBSCRIBE` and
  `UNSUBSCRIBE` cycle, the renewal, the `NOTIFY` parse, and the fold.
* `wiim/client.go` keeps `Run` as the entry point. `Run` starts the
  callback listener and the subscription cycle beside the poll loop,
  which stays at ten seconds. Before the poll reports, it copies the
  evented fields from the event-maintained state, so a poll never
  undoes an event. The `publish` and `notify` path, the setters, and
  the `Status` type do not change.
* `wiim/events_test.go` is new, on a fake GENA server the way
  `client_test.go` is on a fake amp. It pins the subscribe, the
  renewal, the full-state replace, the delta merge, the unknown-`SID`
  drop, and the fallback.
* The controller does not change. The driver owns its protocol, so
  `reconcile.go` and the `equipment.Driver` contract stay as they
  are.
* `wiim/AGENTS.md` moves GENA from "three other interfaces" to the
  built path and records the observed `NOTIFY` shapes.
* The callback port needs no Service and no RBAC. The operator runs
  on the host network from plan 05, so the amp dials the node's own
  address, and the port must be free on the node and open in the host
  firewall.

## What was considered and set aside

* **A WebSocket.** No WiiM document names one, and the forum's answer
  to the question is GENA. Port 8080 is closed on the model in hand.
  GENA is the supported push path and it is proven here.
* **Long polling.** No endpoint holds a request open. The API answers
  each GET and returns.
* **Polling faster.** It answers the latency and nothing else, and it
  doubles the load that causes the dropped request. The effect path
  answers both.
* **Hysteresis alone.** It stops the flap and leaves the stale level
  and the load. It is kept as the reachability rule, not as the
  answer.
* **The PlayQueue and QPlay events.** They carry grouping and queue
  state this operator does not report. A later plan that drives
  grouping can take them up.
* **A slower poll for the settings families.** The poll stays at ten
  seconds, and the events carry the fields that change between polls.
  Splitting the poll would cut the amp's request load, and it is a
  separate change with its own drill.
* **One callback listener shared by every receiver.** Per-client
  listeners are fewer moving parts. A shared one is a refactor for a
  cluster where the port count matters.
* **Reachability from the event stream alone.** The amp sends no
  heartbeat, so silence is not absence. The probe stays.

## The phases

1. The volume path. Subscribe to RenderingControl, fold `Volume` and
   `Mute` through the existing path, and keep the poll for the other
   families. Prove a push moves the session and the topic with no
   poll, and that a poll does not undo it.
2. The transport path. Subscribe to AVTransport and fold the
   transport state and the track, so the player and metadata reads
   leave the evented fields.
3. Stop for review.

## Verification

The fake GENA server in `wiim/events_test.go` pins the subscribe, the
renewal, the `NOTIFY` parse, the full-state replace, the delta merge,
and the fallback to polling.

The drill runs on the house cluster, against a WiiM Amp, from a
development build at a pinned commit:

The drill ran on the house cluster against three WiiM amps, from
development build `2026.09.19-002-dev-028-ab218213`:

* The driver subscribed to all three amps, and the log recorded no
  subscription failure for them.
* A volume change made on the master bedroom amp itself, through the
  device's own API and not through the operator, reached the room's
  topic in the same second, and `status.wiim.playback.volume`
  followed. The poll was still at ten seconds, so a poll cannot
  account for the latency.
* The subscription client opened a connection for each request. A
  second `SUBSCRIBE` on the connection the device answered on failed
  with EOF, and the transport state would not have evented without
  this.
* Every receiver reported `Reachable: True` with the ledger and the
  settings families still filled from the poll.

## What this leaves for later

* Grouping and multiroom volume over the PlayQueue service.
* Whether an input switch and a Bluetooth change raise an
  AVTransport event, or need a read of their own. A drill answers
  this.
* The settings families the amp will not confirm, which stay [an
  open problem](open-problems/the-wiim-settings-the-amp-will-not-confirm.md).
