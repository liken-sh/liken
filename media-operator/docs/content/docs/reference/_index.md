---
title: Reference
weight: 20
---

# Reference

The reference describes the five resources this operator serves and
the message bus their running pieces meet on. [Players](/docs/reference/players/)
and [Plays](/docs/reference/plays/) are the equipment and the runs on
it. [Remotes](/docs/reference/remotes/) and
[Keymaps](/docs/reference/keymaps/) are the controllers and their
button tables.
[MediaPreferences](/docs/reference/mediapreferences/) holds the
cluster's language defaults. [The media bus](/docs/reference/bus/)
is the MQTT contract that carries what happens while a run is live:
reports, commands, button events, and state.

Each resource page gives the resource's fields, then its topics and
payloads in an "On the bus" section. `MediaPreferences` alone has no
topics: its values resolve into a `Play`'s pod when the operator
creates it. The bus page gives the rules the whole topic tree
follows.
