# media-screen

media-screen is the Rust crate that handles the MQTT side of a screen
client, so that the client only has to draw. A screen client is the
program that draws on a `Player`'s monitor while nothing plays. Two
clients link the crate by path, so they follow the same rules:

- `media-operator`'s idle screen, in `media-operator/idle/`
- `library-operator`'s media browser, in `library-operator/media-browser/`

`media-operator` sets the client's topics and timers in its
environment, and the crate reads them with `Wiring::from_environment`.

## What the crate does

- **Sleep.** When nothing plays and no press arrives for
  `spec.idle.fadeAfterSeconds`, the crate tells the client to put the
  screen to sleep, which the client draws as a fade to black. When
  `spec.idle.offAfterSeconds` has passed since the last press, the
  crate publishes the panel request `{"desire": "off"}` on the panel
  topic, and `media-operator` turns the monitor off through its
  `Display`. A press, a controller pointed at this `Player`, or a
  `Play` that starts wakes the screen, and the crate publishes
  `{"desire": "on"}`. `media-operator` also publishes
  `{"action": "wake"}` or `{"action": "sleep"}` on the power topic when
  the `Player` turns on or off, and the crate wakes the screen or puts
  it to sleep at once. It ignores `sleep` while a `Play` runs.
- **Presses.** A press reaches the client only when the controller's
  focus topic names this `Player`, so a remote pointed at another
  `Player` changes nothing here. The crate drops the power keys,
  because `media-operator` reads them and turns the `Player` off and
  on. On `KEY_CYCLEWINDOWS`, the crate publishes an empty message on
  the controller's focus topic with `/cycle` added, and
  `media-operator` points the controller at the next `Player`. While
  nothing plays, every other key reaches the client under its kernel
  name. A press while the monitor is off only turns it back on and
  does not reach the client.
- **Volume.** `media-operator` is the only publisher on the `Player`'s
  volume topic. It reads the volume keys of each remote, sets the
  level of the `Player`'s `Receiver` or `Sink`, and publishes the level
  as a fraction of the device's `spec.volume.max`, for example
  `{"level": 0.63, "muted": false}`. The crate passes the volume keys
  to the client like any other key, so a client must not act on them.
  A retained level is the last known level, so the client stores it
  and draws nothing. A live level means a person changed the volume,
  so the client draws the volume bar. `Volume::draws_after` returns
  false when the message has `"indicator": "receiver"`, which means
  the `Receiver` draws its own volume overlay on the TV, or when the
  message differs from the last level only in that field.

## Dependencies

The crate depends on `rumqttc`, `serde`, `serde_json`, and `metrics`.
It uses no window library, so a client can draw with any toolkit.

## Tests

`make test` runs `cargo fmt --check`, `cargo clippy`, and the tests
under a line-coverage gate of 99 percent.
