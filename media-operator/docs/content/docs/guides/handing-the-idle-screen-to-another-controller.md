---
title: Hand the idle screen to another controller
weight: 30
description: "Hand a Player's idle screen to another operator through spec.idle.controller, and follow the contract that operator uses to draw the screen and read presses. Use when something other than media-operator should draw the screen while nothing plays."
---

# Hand the idle screen to another controller

By default the media operator draws a `Player`'s idle screen with its
own client. `spec.idle.controller` names what draws it instead. This
guide covers both sides: the field a cluster owner sets, and the
contract an operator follows to take a screen over.

You need:

* The operator and its bus, from the [install](/docs/guides/install/).
* A `Player` with a display, so the operator holds a display claim
  for it.

## Name the controller

`controller` is a domain-qualified name, `<domain>/<name>`. Set it on
one `Player`, or on `MediaPreferences` as the household default. The
`Player` wins where both are set.

    spec:
      idle:
        controller: library.liken.sh/media-browser

Two names belong to the media operator:

* `media.liken.sh/idle-screen` is the default. The operator draws the
  idle screen with the client `spec.idle.image` names.
* `media.liken.sh/none` turns the unit's idle screen off. The operator
  holds no display claim and runs no idle pod for it.

Any other name is a delegate. The operator keeps the display claim,
runs no client of its own, and writes what the
delegate needs to the `Player` status. `spec.idle.image` has no effect
under a delegate. `library.liken.sh/media-browser` is the
[library operator](https://liken.sh/library/)'s media browser, the one
delegate that exists today.

`kubectl get players` shows the resolved name in its `Idle` column.

## Read the status

The delegate acts on `status.idle` and never on `spec.idle`. The spec
may inherit its controller from `MediaPreferences`, and the media
operator resolves the two into one name.

    status:
      idle:
        controller: library.liken.sh/media-browser
        claim: den-idle-devices
        requests: [draw, render]
        fadeAfterSeconds: 600
        offAfterSeconds: 1800
        bus:
          address: bus.liken-system.svc:1883
          statusTopic: liken/media/players/den/den/status
          volumeTopic: liken/media/players/den/den/volume
          commandsTopic: liken/media/players/den/den/commands
          panelTopic: liken/media/players/den/den/panel
          remotes:
            - events: liken/media/remotes/den/den-gamepad/events
              focus: liken/media/remotes/den/den-gamepad/focus

* `controller` is the resolved name. Act when it is yours.
* `claim` is the `ResourceClaim` in the `Player`'s namespace that
  holds the screen. Your pod references it by name.
* `requests` are the request names in the claim. `draw` is the
  shared draw device on the unit's screen. `render` is the GPU render
  node, present when the `Player` has one.
* `fadeAfterSeconds` and `offAfterSeconds` are the resolved quiet
  windows, in seconds. Both are always written. Zero on the first means
  the screen never fades on its own, and zero on the second means the
  panel never goes dark on its own.
* `bus` is the broker and every topic the client reads or writes. The
  section on presses below covers each one, and
  [the media bus](/docs/reference/bus/) gives the rules they follow.

## Build the pod

Run one pod in the `Player`'s namespace. Reference the claim by name,
and give the container one entry per request:

    spec:
      resourceClaims:
        - name: devices
          resourceClaimName: den-idle-devices
      containers:
        - name: screen
          image: example.com/my-idle-screen
          resources:
            claims:
              - name: devices
                request: draw
              - name: devices
                request: render

The draw device delivers `WAYLAND_DISPLAY`, a compositor socket the
display operator opened for this claim alone. Open it and map your
window. The socket says which screen the window belongs on, so the
window needs no app-id and no flag.

The draw device is shared. Your pod and a `Play`'s playback pod hold
the screen at once, and the playback window draws over yours while
media plays.

A window can go away under a running client when the compositor
restarts. Nothing inside the process can open the connection again, so
exit when no window exists for longer than a grace period, and let the
kubelet restart the container. The operator's own client exits with
code 7 in that case, so a person reading a container's last state
finds the same code whichever client the image runs.

## Read the presses

The client that draws a screen also answers its controllers. It holds
the focus gate, the shade, the fade and off windows, the volume
indicator, the cycle request, and the panel desire, in its own
process. The operator runs no pod between the bus and the client.

There are two ways to hold that contract:

* Take the `media-screen` crate from the `liken-sh/liken` repository
  as a git dependency pinned to a release tag. Cargo finds the crate
  by its name inside the repository. It reads the variables below,
  runs every rule, and hands the client what it draws: a press, the
  shade down or up, and a focus. Name topics of your own when you open
  the reader. Every message on one comes back on the same connection,
  so a client reads back the retained state it owns.
* Read the same topics yourself and hold the same gates. The
  [bus reference](/docs/reference/bus/) describes each topic.

Set these variables on your container. Each value comes from
`status.idle`:

* `MEDIA_BUS_ADDRESS`, from `bus.address`.
* `MEDIA_PLAYER_NAME`, the `Player`'s `metadata.name`. It is not the
  friendly name. Every focus mark holds this value, so a client that
  sets it wrong answers no press.
* `MEDIA_PLAYER_STATUS_TOPIC`, from `bus.statusTopic`.
* `MEDIA_PLAYER_VOLUME_TOPIC`, from `bus.volumeTopic`. The media
  operator relays the unit's level there, retained, as
  `{"level": 0.63, "muted": false}`, where the level is the fraction
  of the device's max. Draw the level and send none. The retained
  message the broker delivers when the client subscribes sets the
  level and draws nothing, and each live message draws the indicator.
  The client handles no volume key: the operator reads
  `KEY_VOLUMEUP`, `KEY_VOLUMEDOWN`, `KEY_MUTE`, and `KEY_UNMUTE` from
  the controller's events topic and sets the room's level. A client
  that offers a volume control of its own, such as an on-screen
  slider, publishes `{"step": "up"}`, `{"step": "down"}`,
  `{"mute": "toggle"}`, `{"mute": true}`, or `{"mute": false}`, not
  retained, on this topic plus `/commands`, and the operator treats
  each message the same as a press. The field is empty for a unit with
  no sinks. Set nothing then, and the client draws no level.
* `MEDIA_PLAYER_COMMANDS_TOPIC`, from `bus.commandsTopic`. The playback
  pod publishes `{"action": "play-next"}` there when a person takes the
  up-next offer on the scrubber, for a client that starts what follows.
  It publishes `{"action": "home"}` there when a person presses home
  during a film, just before the `Play` ends. The client reads that
  message as a press of the home key. It publishes
  `{"action": "power"}` there when a person presses power during a
  film, just before the `Play` ends. The client holds that message
  until the status reads `Idle`, and then answers it as a power press:
  the toggle on `bus.powerTopic` in the power mode `room`, and the
  shade in the power mode `screen`. The client drops the message when
  no `Idle` arrives within 10 seconds. It publishes `{"action": "power-off"}`
  in place of `power` for `KEY_SLEEP`, and the client answers it as a
  press of `KEY_SLEEP`: `off` on `bus.powerTopic` in the mode `room`,
  or the shade in the mode `screen`. The
  `media-screen` crate holds this rule. When a `Play` ends, the client's
  own surface is on the screen again without a command from the client, and the
  retained status is the cue. Nothing else arrives, and the client
  publishes nothing back.
* `MEDIA_PLAYER_POWER_TOPIC`, from `bus.powerTopic`. Every unit has
  it, so your pod stays the same when a `Receiver` is wired or
  removed. The `power` field of the retained status says where a
  power press goes, and a `Receiver` that is wired or removed changes
  that field and nothing else. Read the field at each press, not at
  startup:
  * `room`: the unit's screen is wired through a `Receiver`. A power
    press between films publishes `{"action": "toggle"}` on this
    topic, not retained. The media operator writes the ask into the
    `Receiver`, and the equipment operator turns the room off or on.
    `KEY_SLEEP` and
    `KEY_WAKEUP`, the names the kernel gives a TV remote's Power Off
    Function and Power On Function, publish `{"action": "off"}` and
    `{"action": "on"}`, which leave a room already off or on as it
    is. The media operator also publishes `{"action": "wake"}` on the
    topic when a person picks the unit's input in the TV's source
    menu while the screen sleeps, and `{"action": "sleep"}` when the
    TV goes to standby. The client wakes the screen and states the
    `on` desire for the first, and brings the shade down and states
    the `off` desire at once for the second, while the unit plays
    nothing.
  * `screen`: the unit's screen is wired through no `Receiver`. A
    power press reaches the client, which lowers its shade, and the
    client ignores `wake` and `sleep` on the topic.
  * No field: the operator has not matched the unit against the
    `Receiver`s yet. This lasts from the operator's start, or the
    `Player`'s creation, until the operator's first pass reaches the
    unit, which can take seconds on a busy API server. Keep the mode
    you last read. A client that has read no mode uses `room` when
    this variable is set, the rule of an operator that predates the
    field.

  Subscribe to the topic always, because the mode can change while
  the client runs. Do not read the variable's presence as the sign of
  a `Receiver`: the operator sets it for every unit, so a client that
  does treats every unit as `room`. An older `library-operator` media
  browser reads it that way, so upgrade `library-operator` together
  with the media operator. The upgrade replaces the idle pod or
  browser pod of each unit with no `Receiver` once, because the pod's
  environment gains this variable.
* `MEDIA_PLAYER_PANEL_TOPIC`, from `bus.panelTopic`. The client
  publishes `{"desire": "on"}` or `{"desire": "off"}` there, retained.
  The operator turns the desire into an override on the screen's
  `Display`.
* `MEDIA_REMOTE_EVENTS_TOPICS` and `MEDIA_REMOTE_FOCUS_TOPICS`, from
  `bus.remotes`. Join each field with newlines, one line per entry, in
  the order the status lists them, so the two lists stay aligned.
* `IDLE_FADE_AFTER_SECONDS`, from `fadeAfterSeconds`.
* `IDLE_OFF_AFTER_SECONDS`, from `offAfterSeconds`.

A press arrives on a controller's events topic as
`{"key": "KEY_UP", "value": 1}`, the same JSON every reader of the
`Remote`'s events topic gets. A press acts only while the
controller's focus mark names this `Player`, only while the unit plays
nothing, and only while the screen is awake. A press on a sleeping
screen wakes it and does nothing else, except `KEY_SLEEP` in the power
mode `screen`, which leaves the screen asleep. A held control arrives again as
value 2, and a release, value 0, acts on nothing.

A live focus mark that moves to this `Player` wakes the screen. A
repeat of the mark the client already holds wakes nothing, because
the operator can publish the same mark again with no person behind it.
The one repeat that acts is the answer to the client's own cycle
request, on a controller that only this `Player` lists.

The client brings its own shade down. The operator's client does it on
back, and on power in the power mode `screen`. A client with levels
does it on power in that mode, and on back when back has no level left
to return to.

## Expect the claim to change

A `ResourceClaim` is immutable. When the `Player`'s display selector
or its render request changes, the operator replaces the claim. Before
it deletes the claim, it deletes every pod the claim's
`status.reservedFor` names, because a claim in use stays in Terminating
until its holders are gone. Your pod is one of those holders.

So run the pod under something that recreates it. An operator's next
pass does that, and so does a `Deployment`. The replacement stays
`Pending` until the new claim exists, then schedules against it. The
same happens when a `Player` switches to `media.liken.sh/none`: the
claim's holders go, then the claim.

When the `Player` switches away from your name, `status.idle` changes
and your client no longer controls the screen. Remove its pod. The
operator deletes it only when it replaces the claim.

## What stays with the operator

The operator keeps the display claim. It writes the focus mark for
each controller and answers the cycle request. It publishes the
`Player` status and the bus status. It writes the override on the
screen's `Display` from the panel desire your client publishes. A
client that publishes no panel desire leaves the panel lit, because
the operator writes no override without one.

The fade window, the off window, the press gate, the shade, the volume
indicator, and the panel desire are the client's. The room's level is
the operator's: it sets the level from the volume keys and the
`volume/commands` asks, and relays it on the volume topic.
