# The player is not a Home Assistant entity

The choice of MQTT for the bus was made for Home Assistant. Plan 03
puts a `Play`'s status on a topic Home Assistant could read, and stops
there. It publishes none of the discovery configs that would turn the
status into an entity, so a person watching Home Assistant sees
nothing of what plays.

Home Assistant learns a device from a retained config message under
`homeassistant/<component>/<node>/<object>/config`. The message names
the entity's kind, its state topic, and its command topics.
zigbee2mqtt publishes one such config per device and a person's
devices appear with no hand configuration.

Home Assistant's built-in MQTT integration has no `media_player`
component to discover, so the player is built from the components it
does have: a `sensor` for the phase and the position, `button`s for
pause and seek and the rest, and a `number` for volume. A single
custom component that reads these topics is the other path, the way
the `mqtt_media_player` component fills one entity from a set of
topics. Either way, the action vocabulary a `Keymap` already defines
maps onto the commands, and a `Play`'s status maps onto the state.

Two pieces are missing before that entity is real. The first is the
discovery config itself, published retained by the operator when a
resource appears and cleared when it leaves. The second is a subject
for the entity to be. A `Play` is momentary, created to start and
deleted to stop, so an entity bound to a `Play` blinks in and out as
films come and go. A `Player` is the standing thing, the unit of
equipment at one spot, and it is the natural `media_player`. So each
`Player` should publish its own retained status: idle when nothing
plays, and the current `Play`'s state when one does. A `Player` that
publishes its own status is the entity Home Assistant binds to, and
the whole-house layer above reads the same topics for what every room
is doing.

Nothing here is designed yet. Plan 03 leaves the path open: the base
topic is configurable, the data topics stay clear of `homeassistant/`,
the reports are retained, and the availability topic Home Assistant
needs is already published from the sidecar's Last Will. One
prerequisite is outside this repository. Home Assistant sees these
entities only on the broker it already reads, so a tight integration
needs `liken` to publish onto that broker. That is
`the-broker-is-always-in-cluster.md`.
