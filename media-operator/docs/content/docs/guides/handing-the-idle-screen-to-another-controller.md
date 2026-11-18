---
title: Hand the idle screen to another controller
weight: 30
description: "Hand a Player's idle screen to another operator through spec.idle.controller, and follow the contract that operator uses to draw the screen and read presses. Use when something other than media-operator should draw the screen while nothing plays."
---

# Hand the idle screen to another controller

When a `Player` plays nothing, its screen shows an idle screen.
`media-operator` draws it with its own client by default. Set
`spec.idle.controller` to let another program draw it, such as the
media browser of `library-operator`. The first section is for the
cluster owner who sets the field. The rest is for the author of the
program that draws the screen.

You need:

* The operator and its bus, from the [install](/docs/guides/install/).
* A `Player` with a display, so the operator creates a display claim
  for it.

## Name the controller

`controller` is a domain-qualified name, `<domain>/<name>`. Set it on
one `Player`, or on `MediaPreferences` as the default for every
`Player`. When both set it, the `Player` wins.

    spec:
      idle:
        controller: library.liken.sh/media-browser

Two names belong to `media-operator`:

* `media.liken.sh/idle-screen` is the default. The operator draws the
  idle screen with the client image in `spec.idle.image`.
* `media.liken.sh/none` turns the idle screen off. The operator
  creates no display claim and no idle pod for the `Player`.

Any other name is a delegate. The operator still creates the display
claim, but it starts no client of its own. It writes what the
delegate needs into the `Player` status, and the delegate runs its own
pod. `spec.idle.image` has no effect under a delegate.
`library.liken.sh/media-browser` is the media browser of
[`library-operator`](https://liken.sh/library/).

`kubectl get players` shows the resolved name in its `Idle` column.

## Read the status

A delegate reads `status.idle`, not `spec.idle`, because the
controller can come from `MediaPreferences`. The operator resolves
the two and writes the result to the status:

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
          powerTopic: liken/media/players/den/den/power
          commandsTopic: liken/media/players/den/den/commands
          panelTopic: liken/media/players/den/den/panel
          remotes:
            - events: liken/media/remotes/den/den-gamepad/events
              focus: liken/media/remotes/den/den-gamepad/focus

* `controller` is the resolved name. Act only when it is yours.
* `claim` is the `ResourceClaim`, in the `Player`'s namespace, that
  holds the screen. Your pod references it by name.
* `requests` are the request names in the claim. `draw` gives your
  pod a window on the `Player`'s screen, and other pods share it.
  `render` is the GPU render node, and it is present only when the
  `Player` has one.
* `fadeAfterSeconds` is the time with no press before the screen dims,
  and `offAfterSeconds` is the time with no press before the panel
  turns off. The operator always writes both. A value of 0 turns that
  step off.
* `bus` is the MQTT broker address and every topic the client reads or
  writes. [The media bus](/docs/reference/bus/) describes each topic.

## Build the pod

Run one pod in the `Player`'s namespace. Reference the claim by name,
and list one `claims` entry in the container for each request:

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

The `draw` device sets `WAYLAND_DISPLAY` to a compositor socket that
`display-operator` opened for this claim only. Open the socket and map
your window. The socket belongs to this screen, so the window needs no
app-id and no flag to choose a screen.

Your pod and the playback pod of a `Play` both hold the `draw`
device. While media plays, the playback window draws over yours.

When the compositor restarts, a running client loses its window and
cannot reconnect from inside the process. So exit when no window has
existed for longer than a grace period that you choose. Exit with
code 7, as the operator's own client does, so a person who reads a
container's last state sees the same code for every client.

The kubelet counts that exit as a crash. After a second compositor
restart within ten minutes, the kubelet waits 10 seconds before it
starts your container again, and the wait doubles with each crash up
to 5 minutes. The operator's own client avoids this wait. The first
process of its container runs the client as a child. When the client
exits with code 7 because a new compositor replaced the old one, that
process waits for the new compositor's socket and starts the client
again. The container exits when the client exits with any other code,
when the same compositor is still running, or when no compositor
accepts a connection within a time limit.

## Read the presses

The program that draws the screen also handles the controllers. The
operator runs no pod between the bus and the client, so the client
applies these rules itself, in its own process:

* It acts on a press only while the controller's focus message names
  this `Player` and no `Play` runs on it.
* It dims the screen and turns the panel off after the quiet periods
  in `status.idle`.
* It draws the volume indicator.
* It asks `media-operator` to move a controller's focus to its next
  `Player`.
* It publishes whether the panel should be on or off.

There are two ways to follow these rules:

* Use the `media-screen` crate from the `liken-sh/liken` repository as
  a git dependency, pinned to a release tag. Cargo finds the crate by
  its name inside the repository. The crate reads the environment
  variables below, applies every rule, and returns the events the
  client draws: a press to act on, the screen dimming or brightening,
  and a focus change. When you open its reader, you can also pass
  topics that your client owns. The reader subscribes to them on the
  same connection and returns each message, so your client reads back
  the retained messages that it published.
* Read the same topics yourself and apply the same rules. The
  [bus reference](/docs/reference/bus/) describes each topic.

Set these variables on your container. Each value comes from
`status.idle`:

* `MEDIA_BUS_ADDRESS`, from `bus.address`.
* `MEDIA_PLAYER_NAME`, the `Player`'s `metadata.name`. Do not use the
  friendly name. Every focus message holds this value, so a client that
  sets it wrong acts on no press.
* `MEDIA_PLAYER_STATUS_TOPIC`, from `bus.statusTopic`.
* `MEDIA_PLAYER_VOLUME_TOPIC`, from `bus.volumeTopic`. The operator
  publishes the `Player`'s volume on this topic, retained, as
  `{"level": 0.63, "muted": false}`. The level is a fraction of the
  device's maximum. The client never publishes to this topic. The
  retained message that the broker sends on subscribe sets the level
  and draws nothing. Each later message draws the volume indicator.

  While a `Receiver` shows its own volume overlay on the TV, the
  payload has `"indicator": "receiver"`, and the client tracks the
  level and the mute state but draws no bar. A message that differs
  from the one before it only in `indicator` draws nothing.

  The client handles no volume key. The operator reads
  `KEY_VOLUMEUP`, `KEY_VOLUMEDOWN`, `KEY_MUTE`, and `KEY_UNMUTE` from
  the controller's events topic and sets the level of the `Player`'s
  devices. A client with its own volume control, such as an on-screen
  slider, publishes `{"step": "up"}`, `{"step": "down"}`,
  `{"mute": "toggle"}`, `{"mute": true}`, or `{"mute": false}` to this
  topic plus `/commands`, not retained. The operator handles each of
  these messages like a key press. The variable is empty for a `Player`
  with no sinks. Then the client draws no level.
* `MEDIA_PLAYER_COMMANDS_TOPIC`, from `bus.commandsTopic`. The playback
  pod publishes `{"action": "play-next"}` here when a person accepts
  the up-next offer on the scrubber, for a client that starts what
  follows. It publishes `{"action": "home"}` here when a person
  presses home during a film, just before the `Play` ends. The client
  treats that message as a press of the home key, as the `media-screen`
  crate does. When a `Play` ends, the client's screen shows again
  without a command, and the retained status reports the change. The
  client publishes nothing to this topic.
* `MEDIA_PLAYER_POWER_TOPIC`, from `bus.powerTopic`. Every `Player` has
  this topic. The operator owns the `Player`'s power through
  `spec.power`. It publishes `{"action": "sleep"}` when the `Player`
  turns off and `{"action": "wake"}` when it turns on, not retained.
  It publishes the same messages for HDMI-CEC requests from the TV:
  `wake` when a person selects the `Player`'s input in the TV's source
  menu while the screen sleeps, and `sleep` when the TV goes to
  standby. On `wake`, the client brightens the screen and publishes
  `on` to the panel topic. On `sleep`, while the `Player` plays nothing,
  the client dims the screen and publishes `off` to the panel topic at
  once. The client publishes nothing to the power topic. The operator
  turns the panel, the TV, and the receiver off and on itself, so the
  client's panel message only matches what the operator already did.
* `MEDIA_PLAYER_PANEL_TOPIC`, from `bus.panelTopic`. The client
  publishes `{"desire": "on"}` or `{"desire": "off"}` here, retained.
  This is the panel state the client requests. The operator turns it
  into an override on the screen's `Display`.
* `MEDIA_REMOTE_EVENTS_TOPICS` and `MEDIA_REMOTE_FOCUS_TOPICS`, from
  `bus.remotes`. Join each field with newlines, one line per entry,
  in the order of `bus.remotes`, so that the two lists match entry
  for entry.
* `IDLE_FADE_AFTER_SECONDS`, from `fadeAfterSeconds`.
* `IDLE_OFF_AFTER_SECONDS`, from `offAfterSeconds`.

A press arrives on a controller's events topic as
`{"key": "KEY_UP", "value": 1}`. This is the same JSON that every
reader of a `Remote`'s events topic gets. A held key arrives again
with value 2, and a release arrives with value 0, which the client
ignores.

A press on a dim screen wakes it. While the panel is on, the client
also acts on that press after the wake, so a person does not press
twice after each quiet period. The panel stays on while the screen is
dim, until `offAfterSeconds` passes, or forever when it is 0. While
the panel is off, a press only wakes the screen and publishes `on` to
the panel topic, because the person pressed at a dark TV and could not
see what the key would do. The panel is off after `offAfterSeconds`
passes and after the client receives `sleep` on the power topic.

A back key (`KEY_BACK`, `KEY_ESC`, `KEY_EXIT`, or `KEY_BACKSPACE`) on a
dim screen only wakes it, for every client. The operator's own client
dims the screen on back, and without this rule the press of back
would dim the screen again right after the wake. A repeat of a key
that arrives while the screen is dim does nothing, because the person
held the key while the screen dimmed and its first press already
acted. The client receives no repeats of a key whose press only woke
the screen, or whose repeat arrived while the screen was dim, until
the key is released or the same remote sends its next press.

The power keys `KEY_POWER`, `KEY_POWER2`, `KEY_SLEEP`, and
`KEY_WAKEUP` belong to the operator. It reads them from the same
topic and turns the `Player` off and on, so the client does nothing
for them and does not dim the screen. A press of any other key on a
`Player` that is off turns it on, and the client then receives `wake`
on the power topic.

A focus message that moves focus to this `Player` from elsewhere
wakes the screen. A repeat of the current focus message wakes nothing,
because the operator can publish the same message again with no
person pressing a key. One repeat does wake the screen: the operator's
response to the client's own request to move focus, on a controller
that only this `Player` lists.

## Expect the claim to change

A `ResourceClaim` cannot be edited. When the `Player`'s display
selector or its render request changes, the operator replaces the
claim. First it deletes every pod in the claim's `status.reservedFor`,
because a claim that a pod uses stays in `Terminating` until those pods
are gone. Your pod is one of them.

So run the pod under something that creates it again, such as a
`Deployment`. The replacement pod stays `Pending` until the new claim
exists, and then it schedules against it. The same happens when a
`Player` switches to `media.liken.sh/none`: the operator deletes the
pods that use the claim, and then the claim.

When the `Player` switches away from your name, `status.idle` changes
and your client no longer controls the screen. Remove its pod. The
operator deletes it only when it replaces the claim.

## What the operator keeps

The operator creates the display claim. For each controller it writes
the focus message, and it handles each request to move focus. It
publishes the `Player` status and the bus status. It sets the
override on the screen's `Display` from the panel state that your
client publishes. A client that publishes no panel state leaves the
panel on, because the operator sets no override without one.

The client owns the quiet periods, the rules for which presses to act
on, the dimming, the volume indicator, and the panel state it
requests. The operator owns the volume level. It sets the level from
the volume keys and from messages on the `/commands` topic of the
volume topic, and it publishes the level on the volume topic.
