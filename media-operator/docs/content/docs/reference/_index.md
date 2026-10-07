---
title: Reference
weight: 20
---

# Reference

The reference describes the five resources and how their pods talk
to each other:

* [Players](/docs/reference/players/) are the units of equipment, and
  [Plays](/docs/reference/plays/) are the runs of media on them.
* [Remotes](/docs/reference/remotes/) are the controllers, and
  [Keymaps](/docs/reference/keymaps/) map their buttons.
* [MediaPreferences](/docs/reference/mediapreferences/) holds the
  cluster's default languages.
* [The media bus](/docs/reference/bus/) is the MQTT contract for
  everything that happens while media plays: reports, commands,
  button presses, and state.
* [The media API](/docs/reference/api/) captures what a `Player`
  shows and plays right now, as its screen, its sound, or both in
  one stream.
* [Render node capabilities](/docs/reference/capabilities/) describes
  the `media.liken.sh` devices that state what each GPU can decode,
  encode, and scale.

Each resource page lists the resource's fields, then its topics and
payloads under "On the bus". `MediaPreferences` has no topics: the
operator reads its values into a `Play`'s pod when it creates the
pod. The bus page gives the rules for the whole topic tree.
