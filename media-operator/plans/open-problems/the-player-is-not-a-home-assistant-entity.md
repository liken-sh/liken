# The player is not a Home Assistant entity

The bus uses MQTT because of Home Assistant. Plan 03
puts a `Play`'s status on a topic Home Assistant could read, and does
no more. It publishes none of the discovery configs that would turn
the status into an entity, so Home Assistant shows nothing of what
plays.

Home Assistant adds a device when it reads a retained config message
under `homeassistant/<component>/<node>/<object>/config`. The message
names the entity's kind, its state topic, and its command topics.
zigbee2mqtt publishes one such config per device, and a person's
devices appear with no manual configuration.

Home Assistant's built-in MQTT integration has no `media_player`
component to discover, so the player is built from the components it
does have: a `sensor` for the phase and the position, `button`s for
pause and seek and the rest, and a `number` for volume. The other
option is a single custom component that reads these topics, the way
the `mqtt_media_player` component fills one entity from a set of
topics. With either option, the action vocabulary a `Keymap` already
defines maps onto the commands, and a `Play`'s status maps onto the
state.

Two parts are missing before that entity exists. The first is the
discovery config itself, which the operator publishes retained when a
resource is created and clears when the resource is deleted. The
second is an object for the entity to represent. A `Play` is
short-lived: it is created to start playback and deleted to stop it.
So an entity bound to a `Play` appears and disappears with each film.
A `Player` is long-lived, the equipment at one location, and it is
the natural `media_player`. So each `Player` should publish its own
retained status: idle when nothing plays, and the current `Play`'s
state when one does. Home Assistant binds its entity to that `Player`
status, and the whole-house layer above reads the same topics to see
what plays in every room.

Nothing here is designed yet. Plan 03 leaves the option open: the base
topic is configurable, the data topics stay outside `homeassistant/`,
the reports are retained, and the sidecar's Last Will already
publishes the availability topic that Home Assistant needs. One
prerequisite is outside this repository. Home Assistant sees these
entities only on the broker it already reads, so a tight integration
needs `liken` to publish onto that broker. That is
`the-broker-is-not-configurable.md`.
