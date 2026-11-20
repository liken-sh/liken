# media-screen

A screen client is the program that draws on a `Player`'s monitor
while nothing plays. `media-screen` is the Rust crate that does a
screen client's MQTT work, so that the client only has to draw.

The crate connects to the message bus of
[`media-operator`](https://liken.sh/media/) and subscribes to one
`Player`'s topics. It decides when the screen sleeps and which
presses reach the client, and it publishes the client's requests. It
opens no window and depends on no window library, so a client can
draw with any toolkit.

`media-operator` passes the broker address, the topics, and the
timers to the client in environment variables, and the crate reads
them with `Wiring::from_environment`. Each client links the crate by
path, so every client follows the same rules. For example,
`library-operator`'s media browser is a client.

## The modules

- `screen` contains `Screen`, which has the rules. `Screen` opens no
  socket and reads no clock. Each method takes a message and the
  current time, and returns the effects.
- `reader` contains `Reader`, which runs the broker connection and
  the timers, and `Bus`, which a client drains each time its loop
  wakes.
- `wiring` reads the environment variables that `media-operator` sets.
- `volume`, `panel`, and `status` define the payloads of the volume,
  panel, and status topics.
- `metrics` reports the crate's gauges to the `metrics` recorder that
  the client installs.

## What the crate does

- **Sleep.** When nothing plays and no press arrives for
  `spec.idle.fadeAfterSeconds`, the crate tells the client to put the
  screen to sleep. The client draws this as a fade to black. When
  `spec.idle.offAfterSeconds` has passed since the last press, the
  crate publishes the panel request `{"desire": "off"}` on the panel
  topic, and `media-operator` turns the monitor off through its
  `Display`. The screen wakes on a press, when a controller is pointed
  at this `Player`, or when a `Play` starts. The crate then publishes
  `{"desire": "on"}`. `media-operator` also publishes
  `{"action": "wake"}` or `{"action": "sleep"}` on the power topic when
  the `Player` turns on or off. The crate then wakes the screen or
  puts it to sleep at once, but it ignores `sleep` while a `Play` runs.
- **Presses.** Each controller has a focus topic, which says which
  `Player` the controller is pointed at. A press reaches the client
  only when the controller is pointed at this `Player`, so a remote
  pointed at another `Player` has no effect here. The crate drops the
  power keys, because `media-operator` reads them and turns the
  `Player` off and on. On `KEY_CYCLEWINDOWS`, the crate publishes an
  empty message on the controller's focus topic with `/cycle`
  appended, and
  `media-operator` points the controller at the next `Player`. While
  nothing plays, every other key reaches the client under its kernel
  name. A press while the monitor is off only turns the monitor back
  on, and the press does not reach the client.
- **Volume.** `media-operator` is the only publisher on the `Player`'s
  volume topic. It reads the volume keys of each remote, sets the
  level of the `Player`'s `Receiver` or `Sink`, and publishes the level
  as a fraction of the device's `spec.volume.max`, for example
  `{"level": 0.63, "muted": false}`. The crate passes the volume keys
  to the client like any other key, so a client must not act on them.
  The broker keeps the last message as a retained message and sends
  it when the client subscribes. That message has the last known
  level, so the client stores it and draws nothing. A live message
  means a person changed the volume, so the client draws the volume
  bar. `Volume::draws_after` returns false when the message has
  `"indicator": "receiver"`, which means the `Receiver` draws its own
  volume overlay on the TV. It also returns false when the message
  differs from the last level only in that field.

The manual's [bus reference](https://liken.sh/media/docs/reference/bus/)
lists every topic with its writer and its readers.

MIT. See [LICENSE](LICENSE).
