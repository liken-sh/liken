---
title: Hand the idle screen to another controller
weight: 30
---

# Hand the idle screen to another controller

By default the media operator draws a `Player`'s idle screen with its
own client. `spec.idle.controller` names who draws it instead. This
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
under a delegate.

`kubectl get players` shows the resolved name in its `Idle` column.

## Read the status

The delegate acts on `status.idle` and never on `spec.idle`. The spec
may inherit its controller from `MediaPreferences`, and the media
operator is what resolves the two tiers.

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
* `requests` are the request names the claim carries. `draw` is the
  shared draw device on the unit's screen. `render` is the GPU render
  node, present when the `Player` has one.
* `fadeAfterSeconds` and `offAfterSeconds` are the resolved quiet
  windows, in seconds. Both are always written. Zero on the first means
  the screen never fades on its own, and zero on the second means the
  panel never goes dark on its own.
* `bus` is the broker and every topic the client reads or writes. The
  section on presses below covers each one.

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

The draw device delivers two variables into the container:
`WAYLAND_DISPLAY`, the compositor socket, and `DISPLAY_APP_ID`, the
app-id of the allocated output. Open the socket and ask for your
window with that app-id. The compositor places a window on the
claimed output by app-id, so a window without it lands nowhere.

The draw device is shared. Your pod and a `Play`'s playback pod hold
the screen at once, and the playback window draws over yours while
media plays.

A window can go away under a running client, when the compositor
restarts. Nothing inside the process can open the connection again, so
exit when no window exists for longer than a grace and let the kubelet
restart the container. The operator's own client exits with code 7 in
that case, so a person reading a container's last state finds the same
code whichever client the image runs.

## Read the presses

The client that draws a screen also answers its controllers. It holds
the focus gate, the shade, the fade and off windows, the volume step,
the cycle request, and the panel desire, in its own process. The
operator runs no pod between the bus and the client.

There are two ways to hold that contract:

* Take the `media-screen` crate from this repository as a git
  dependency pinned to a release tag. It reads the variables below,
  runs every rule, and hands the client what it draws: a press, the
  shade down or up, a focus, and a fresh surface.
* Read the same topics yourself and hold the same gates. The
  [bus reference](/docs/reference/bus/) describes each topic.

Set these variables on your container. Each value comes from
`status.idle`:

* `MEDIA_BUS_ADDRESS`, from `bus.address`.
* `MEDIA_PLAYER_NAME`, the `Player`'s `metadata.name`. It is not the
  friendly name. Every focus mark holds this value, so a client that
  sets it wrong answers no press.
* `MEDIA_PLAYER_STATUS_TOPIC`, from `bus.statusTopic`.
* `MEDIA_PLAYER_VOLUME_TOPIC`, from `bus.volumeTopic`. It is empty for
  a unit with no sinks. Set nothing then, and the client draws no level
  and steps none.
* `MEDIA_PLAYER_COMMANDS_TOPIC`, from `bus.commandsTopic`. The operator
  publishes `{"action": "re-present"}` there when a `Play` ends, and
  the client maps a fresh surface. Nothing else arrives, and the client
  publishes nothing back.
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
`{"key": "KEY_UP", "value": 1}`, the same JSON the `Remote`'s events
topic carries for every reader. A press acts only while the
controller's focus mark names this `Player`, only while the unit plays
nothing, and only while the screen is awake. A press on a sleeping
screen wakes it and does nothing else. A held control arrives again as
value 2, and a release, value 0, acts on nothing.

The client brings its own shade down. The operator's client does it on
back. A client with levels does it when back has no level left to
climb.

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
and your pod is yours to remove. The operator deletes it only when it
replaces the claim.

## What stays with the operator

The operator keeps the display claim. It writes the focus mark for
each controller and answers the cycle request. It publishes the
`Player` status and the bus status. It publishes `re-present` when a
`Play` ends. It writes the override on the screen's `Display` from the
panel desire your client publishes. A client that publishes no panel
desire leaves the panel lit, because the operator writes no override
without one.

The fade window, the off window, the press gate, the shade, the volume
step, and the panel desire are the client's.
