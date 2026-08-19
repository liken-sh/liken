# 05, Choosing the codec

Planned on 2026-08-19. The mechanism is proven by hand on liken-1;
this plan makes it inventory and API.

## The problem

WirePlumber picks the A2DP codec when a speaker connects, and
nothing else has a say. The inventory publishes the negotiated
codec on each speaker device, but not the set the speaker supports,
so a person reading the slice cannot know a choice exists. A
consumer has no way to state one.

The choice is real. On 2026-08-19 the lab's b06+ speaker chopped
under radio interference on aptX and played clean on SBC. aptX
sends at a fixed rate near 352 kbps and leaves gaps when the air is
busy. SBC shrinks its bitpool under the same pressure and keeps the
stream whole at a lower quality. Which loss a person prefers is not
the operator's decision.

## What the graph already offers

PipeWire's bluez5 device answers `PropInfo` for the property
`bluetoothAudioCodec`. The entry lists the codecs the speaker and
this image both support, as integer ids with display labels, and
names the current one as the default. The lab speaker answers
aptX, SBC, SBC-XQ, and aptX-LL. Writing the property with
`set-param` renegotiates the transport in about a second.

Two facts from the hand drill shape the design:

* The switch destroys the sink node and builds a new one with a
  new id under the same name. Anything that cached the id is
  wrong after a switch; anything that holds the name is fine.
* WirePlumber re-links a playing stream to the new node on its
  own. The movie that was playing through node 88 kept playing
  through node 99. A switch under a live consumer is safe.
* The new node starts at the stock 40 percent volume, not the old
  node's level. The pod's WirePlumber runs with persistent storage
  off, because nothing durable backs it, so nothing restores the
  volume across the rebuild.

## The design

Two halves, inventory and selection, in one release.

### The `codecs` attribute

Every speaker device that has a sink node publishes `codecs`: the
`PropInfo` list, space-joined, in the graph's own vocabulary. The
existing `codec` attribute already publishes the negotiated codec
as `api.bluez5.codec` spells it, lowercase with underscores, and
the list must speak the same language so a reader can find the
current codec in it. `PropInfo` labels spell for humans (`aptX`,
`SBC-XQ`), so the operator lowercases each label and turns dashes
into underscores. The transform is total and needs no table that
could drift from PipeWire's.

The list joins the space-separated-string convention that
`lpcmBitDepths` set: the attribute language has no array type, so
a list is one string and a selector asks with `.contains()`.

### Selection through the claim

A consumer states a codec in its `ResourceClaim`, in the channel
DRA built for exactly this, per-request driver parameters:

    spec:
      devices:
        config:
          - opaque:
              driver: audio.liken.sh
              parameters:
                codec: sbc

The scheduler passes an opaque block through unread, so the first
code that sees it is this driver's prepare call. Prepare already
reads the claim from the API server. The new steps:

* Parse the parameters. An unknown key fails the prepare, because
  a silently ignored typo would play the wrong codec without a
  word.
* A `codec` on a device that is not a Bluetooth speaker fails the
  prepare. A sound card has no air codec.
* A codec outside the device's published list fails the prepare,
  and the error names the list.
* When the requested codec is not the current one, write the
  property with the integer id `PropInfo` gave, then poll the
  graph until the node with the speaker's name reports the
  requested codec, bounded at ten seconds. Deliver `PIPEWIRE_NODE`
  only after the new node exists, because the old node died with
  the old codec.
* Deliver the sink at unity. A speaker allocates to one claim at a
  time, so any level a prepare finds is a leftover, from an
  earlier tenant or from a hand-run tool, and never the arriving
  consumer's choice. Every prepare of a speaker writes channel
  volumes of 1.0 on the node it delivers, switch or no switch.
  Loudness belongs to the consumer's own stream volume.

A claim with no config block changes nothing: WirePlumber picks,
as it always did.

Unprepare restores nothing. A speaker allocates to one claim at a
time, so no other consumer holds the old codec, and a renegotiation
on teardown would sound for nobody. The choice persists on the
device until the next claim states one or the speaker reconnects,
which hands the pick back to WirePlumber.

### Every sink is born at unity

The volume reset uncovered a wrong default that predates this
plan. WirePlumber gives a sink with no stored volume
`device.routes.default-sink-volume`, which ships at 40 percent, a
desktop safety default. This pod stores no volumes, so every sink
on every machine starts there, and every stage below unity
multiplies the samples down before the codec sees them. The
`declare` container now writes `device.routes.default-sink-volume
= 1.0` on every machine, radio or not. Loudness belongs to the
consumer's own stream volume and to the hardware behind the jack,
not to a hidden multiplier in the middle.

### What this rejects

A device per codec was the other shape: publish `b06+ on SBC` and
`b06+ on aptX` as separate devices and let the class pick. It
multiplies every speaker by its codec count, taints have to fan
out with it, and two claims could hold one radio through two
faces of it. A codec is a property of one stream on one device,
not a device.

## The drill

On liken-1, with the movie manifest from milestone 60's demo:

1. Run the movie with no config block. The slice's `codec` says
   what WirePlumber picked.
2. Run it again with `codec: sbc`, then again with `codec: aptx`.
   The only edit between runs is the claim's parameters, and the
   slice follows.
3. Run it with `codec: ldac`. The pod must not start, and the
   claim's events must name the published list.
