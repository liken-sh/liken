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

Any other name is a delegate. The operator keeps the display claim and
the idle command pod, runs no client of its own, and writes what the
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

* `controller` is the resolved name. Act when it is yours.
* `claim` is the `ResourceClaim` in the `Player`'s namespace that
  holds the screen. Your pod references it by name.
* `requests` are the request names the claim carries. `draw` is the
  shared draw device on the unit's screen. `render` is the GPU render
  node, present when the `Player` has one.

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

The idle command pod reads key names off the `Remote`s' events
topics and holds its own table of what each key means there, and it
keeps the focus gate and the shade under a delegate, so your client
holds none of them. It joins the bus with the three facts under
`status.idle.bus` instead:

    status:
      idle:
        bus:
          address: bus.liken-system.svc:1883
          commandsTopic: liken/media/players/den/den/commands
          screenTopic: liken/media/players/den/den/screen

* `address` is the broker, as `host:port`.
* `commandsTopic` is where the presses arrive, as
  `{"key": "KEY_UP", "value": 1}`, the same JSON the `Remote`'s
  events topic carried. The keys are the four arrows, `KEY_ENTER` and
  its synonyms `KEY_OK`, `KEY_SELECT`, and `KEY_KPENTER`, and
  `KEY_BACK` and its synonyms `KEY_ESC` and `KEY_EXIT`. Only a press
  from a controller whose mark names this `Player` arrives, only
  while the unit plays nothing, and only while the screen is awake. A
  press on a sleeping screen wakes it and reaches you as nothing. A
  held control arrives again as value 2, and a release, value 0, is
  never forwarded.
* `screenTopic` is where the idle command pod states its moments:
  `sleep` and `wake`, retained, and `focus` and `present`, not. Draw a
  black frame on `sleep`, draw again on `wake`, and map a fresh
  surface on `present`, because a surface a film covered stays hidden
  on a seatless compositor until a new one is mapped.

Your client writes one message. When back is pressed with no level
left to climb, publish `{"action": "sleep"}` on `commandsTopic`, and
the idle command pod brings the shade down the way it does for its own
client. The operator holds the broker and the topic base, so read the
topics from the status and derive none.

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

The idle command pod keeps running under a delegate. It holds the fade
window, the off window, the panel desire, and the press gate for the
unit's controllers, and it publishes what it decides on the `Player`'s
screen topic. A client that reads the bus follows the same topics the
operator's own client reads, described in the
[bus reference](/docs/reference/bus/). A client that reads no bus draws
what it draws, and the operator still darkens the panel on its own
schedule.
