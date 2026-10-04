# media-screen

media-screen is the bus half of a screen client for one Player: the
timers, the focus gate, the shade, the volume level, the cycle request,
and the panel desire. A screen client holds these rules in its own
process.

The crate reads the Player's `volume` topic and hands each level to the
client to draw. `media-operator` is the only writer of that topic: it
reads the volume keys off each remote, sets the level of the unit's
`Receiver` or `Sink`, and relays the level the device reports as
`{"level": 0.63, "muted": false}`, where `level` runs from 0.0 to 1.0 of
the device's `spec.volume.max`. A screen publishes nothing on the topic
and answers no volume key. A retained level sets the level and draws
nothing. A live level draws the indicator, unless it carries
`"indicator": "receiver"`, which a `Receiver` that shows its own volume
overlay on the TV adds, or differs from the last level only in that
field. Such a level is tracked and draws nothing
(`Volume::draws_after`). Two clients link this crate by path, so they
hold the same rules:

- media-operator's idle screen, in `media-operator/idle/`
- library-operator's media browser, in `library-operator/media-browser/`

The crate depends on the broker client and JSON, and on nothing else.
A client draws with whatever toolkit it chose, so the crate names no
window library.

`make test` runs the format check, the lints, and the tests under the
coverage gate.
