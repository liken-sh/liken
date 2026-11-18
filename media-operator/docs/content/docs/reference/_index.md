---
title: Reference
weight: 20
---

# Reference

These pages describe each resource of `media-operator`, and the
messages that its pods send to each other:

* [Players](/docs/reference/players/) declare the units of equipment,
  and [Plays](/docs/reference/plays/) are the runs of media on them.
* [Remotes](/docs/reference/remotes/) declare the controllers, and
  [Keymaps](/docs/reference/keymaps/) map their buttons to actions.
* [MediaPreferences](/docs/reference/mediapreferences/) sets the
  cluster's defaults: the audio and subtitle languages, the time zone,
  and the idle screen.
* [The media bus](/docs/reference/bus/) describes the MQTT topics that
  the pods use while media plays: reports, commands, button presses,
  and state.
* [The media API](/docs/reference/api/) captures what a `Player` shows
  and plays right now: its screen, its sound, or both in one stream.
* [Render node capabilities](/docs/reference/capabilities/) describes
  the `media.liken.sh` devices that list what each GPU can decode,
  encode, and scale.

Each resource page lists the resource's fields, and then its topics
and payloads under "On the bus". `MediaPreferences` has no topics,
because the operator copies its values into the pods that it creates.
The bus page describes the rules for the whole topic tree.
