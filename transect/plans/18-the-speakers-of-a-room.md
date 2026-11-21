# 18, The speakers of a room

Proposed on 2026-11-21. Nothing is built.

This plan draws a speaker for each channel in the room of a `Player`,
and the title of what plays on its screen. It builds on the roles, the
readings `through` a reference, and the wires of
[plan 15](completed/15-the-elevations-of-a-player.md). It waits on a decision
about two new fields of other operators, which "The decision" below
states.

## The problem

The room of a `Player` has no speaker after plan 15, because its
`Sink` is the machine's sound output and not a speaker. A home theater
has a speaker for each channel, and "Who draws a pattern" in
[00-design.md](00-design.md) says that the sketch draws each one in its
place, with an outline and no fill, because a speaker is not an object.
The screen shows the name of the `Play`, such as
`theater-a-tale-of-two-cities-k2x8p`, where the reader wants the title.

No column of the cluster gives either fact today. A `Play` states the
title of each item in `spec.items[].presentation`, and the item that
plays is `status.item`, so the title is at a list index that another
field gives, which a JSONPath cannot select. A `Sink`'s `Layout` is
what the receiver accepts, from its ELD, and on a home cluster a
receiver with five speakers and a subwoofer reports eight channels.
The speakers that a receiver has are in fields of each driver.

## The decision

**Should `media-operator` add `status.title` to a `Play`, and
`equipment-operator` add `status.speakers` to a `Receiver`, each with a
printer column?**

The recommendation is yes, for both:

- **`status.title` on a `Play`**: the title of the item at
  `status.item` as the screen shows it, absent while nothing plays,
  with the printer column `Title` at priority 1. A person who runs
  `kubectl get plays -o wide` sees what plays.
- **`status.speakers` on a `Receiver`**: the channels that the
  receiver has a speaker for, in PipeWire's channel names, with the
  printer column `Speakers` at priority 1. Each driver writes it from
  what its receiver reports. A Denon reports a level for each channel
  that has a speaker in `status.denon.channelVolumes`, and whether a
  subwoofer is set up in `status.denon.audio.subwoofer`. A WiiM reports
  whether a subwoofer is plugged in, in
  `status.wiim.audio.subwoofer.plugged`. PipeWire's names are the names
  of the `Sink`'s `Layout`, so a reader can compare the two.

How a driver's channel names map to PipeWire's is not settled. A Denon
names its center `C`, which is PipeWire's `FC`. A Denon names its
surround speakers `SL` and `SR`, and PipeWire has both `SL` and `SR`,
the side speakers of a 7.1 layout, and `RL` and `RR`, the rear
speakers of a 5.1 layout. The recommendation is `SL` and `SR`, which
keeps the Denon's names, and `equipment-operator`'s plan for the field
decides. A `Sink` on a home cluster reports both pairs in its `Layout`,
so it does not settle the question.

Each change updates its operator's manual. Without the fields, this
plan builds nothing, because the knowledge would read a path of
`transect`'s own into each driver's status.

## The design

### A role with a part for each value

A role with `each` names a printer column of the object that its
reference names, and draws a part for each value that the column's path
selects. `places` lists the cells that the role can fill, and a value
goes in the cell whose name is the value in lower case:

```yaml
speakers: {reference: status.receiver.name, shape: speaker, each: Speakers, places: [fl, fr, fc, lfe, sl, sr]}
```

A sketch names either the role's places, so that each value stands in
its own cell, or the role's name, so that the values stand side by
side in one area. The room does the first, and the signal path the
second. `each` takes a map with `column` and `through`, as `reads`
does.

- A value is a part with no object: an outline with no fill, with the
  column's name as its label and the value as its text. It is dimmed
  when the object it was read from is at rest.
- A place with no value leaves its cell blank, so a room with two
  speakers draws the front left and right and nothing else.
- A value with no place is named under the sketch: "Speakers of
  Receiver theater-avr also lists RL, RR, and the sketch has no place
  for them."
- An expected role with no value draws each place dashed, with
  "nothing".

`transect` will leave out a document, with an `Invalid` condition,
when a role has `each` and no `places` or `places` and no `each`; when
a place is not a valid name, is a role's name, or is another role's
place; and when a sketch names both a role and one of its places.

### The knowledge of the `Player`

Plan 15's knowledge changes in these lines:

```yaml
  references:
    - {path: status.play, kind: Play, group: media.liken.sh, label: plays}
  roles:
    display:  {reference: status.screen.monitor, shape: screen, expected: true, reads: [Connector, Mode], shows: [Activity, [{column: Title, through: status.play}, Idle]]}
    speakers: {reference: status.receiver.name, shape: speaker, each: Speakers, places: [fl, fr, fc, lfe, sl, sr]}
  elevations:
    - name: the room
      question: What is this player made of?
      sketch: |
        fl       display  display   display   fr
        .        fc       fc        fc        .
        lfe      sink     receiver  receiver  .
        sl       .        remote    .         sr
        machine  idle     bus       claim     .
    - name: the signal path
      question: Where do picture and sound go?
      sketch: |
        machine  receiver  display
        sink     receiver  speakers
      wires:
        - {from: machine, to: display, via: receiver, carries: picture}
        - {from: sink, to: receiver, carries: sound}
        - {from: receiver, to: speakers, carries: sound}
```

The new reference adds the `Play` to the card. The screen shows the
title while a `Play` runs, linked to the `Play`, and the idle screen
when `status.play` is empty. The room has the front speakers beside
the screen and the center speaker under it, the subwoofer beside the
sink and the receiver, and the surround speakers on each side of the
remote, where a viewer holds it. The speakers are not expected, so a
`Player` with no receiver draws none, and a receiver with two speakers
draws two.

### What plan 17 gives the room

Every speaker is the library's `speaker`, in an area of the size that
the room gives it, and a role states no size. A `speaker` turns when
its area spans more columns than rows, so the center speaker under the
screen lies on its side. The subwoofer and the surround speakers stand
in areas of their own size. The screen is a surface, which shows the
title in its middle on at most two lines. The wire from the receiver
to the speakers in the signal path ends on a bar along the edge of the
speakers' area, with a short wire to the port of each speaker, so the
signal path needs a port on the left edge of the library's `speaker`,
and plan 17's definition of the speaker states its ports.

## What this plan leaves out

- **The trim of each channel and the level of the subwoofer.** Each is
  one value for each channel, which a column of one list does not give.
- **A speaker that the panel has.** A `Player` with no receiver plays
  its sound through the panel, and no field states the panel's
  speakers.

## The alternatives

- **The speakers from the `Sink`'s `Layout`.** The room would draw the
  channels that the receiver accepts, which can be more than it has.
- **The speakers from `status.denon.channelVolumes`.** It is one
  driver's field, its channels are the keys of a map, and a WiiM has no
  such field.
- **The title from `spec.items[0].presentation.title`.** It is wrong
  for a `Play` of several items after the first ends, and an episode
  states its series and episode title in other fields.
- **A map from each value to a cell.** The value in lower case names
  the cell, so `places` only says which cells the role fills, and a
  misspelled role is still a fault.

## How the work is proved

Unit tests cover each fault above, and a role with `each` whose values
have places, lack them, or are none, and one that a sketch names by its
name. The fixture's shelf gets a role with `each`, and the golden pages
of plan 15 cover it. The operators' tests cover `status.title` and
`status.speakers`. On a workstation, the room of each `Player` is read
against `kubectl get plays,receivers -o wide`.
