# The media-operator design

The media operator is a media routing and playback layer expressed
as Kubernetes resources. It runs on a
[`liken`](https://liken.sh/) cluster above the hardware operators:
[`display-operator`](https://display.liken.sh),
[`audio-operator`](https://audio.liken.sh), and
[`bluetooth-operator`](https://bluetooth.liken.sh). Those operators
publish each display, speaker, and controller as a claimable DRA
device. This operator declares which devices form a unit, what plays
on that unit right now, and which physical remote drives it. It is a
backend. A frontend comes later, and it reads and writes only these
resources.

Like the hardware operators, this is an optional workload. A cluster
runs fine without it, and it uses no private interface into `liken`.
Claims, `ResourceSlices`, and CDI files are the public contracts it
builds on.

## The problem

The hardware operators already support playback from a hand-written
manifest. The display operator and the audio operator both derive a
`monitor.liken.sh/id` attribute from the same monitor, one out of the
EDID and one out of the PCM's ELD, so a manifest can select a screen
and the speakers built into that screen without naming a connector or
a sound card. A condensed example of one claim; a full manifest needs
several:

```yaml
apiVersion: resource.k8s.io/v1
kind: ResourceClaim
metadata:
  name: living-room-tv
spec:
  devices:
    requests:
      - name: screen
        exactly:
          deviceClassName: display-output
          allocationMode: ExactCount
          count: 1
          selectors:
            - cel:
                expression: >-
                  device.attributes["monitor.liken.sh"].id == "boe-1080"
          # A monitor somebody unplugs taints its device NoExecute.
          # Thirty seconds of dark is a cable being moved; longer
          # than that ends the pod.
          tolerations:
            - key: display.liken.sh/disconnected
              operator: Exists
              effect: NoExecute
              tolerationSeconds: 30
```

A second claim, `living-room-tv-speakers`, selects the same id
through the audio operator, so the sound plays from the speakers
built into that screen. A third, `living-room-gpu`, names the GPU's
render node. The pod references all three by role and mounts the
media:

```yaml
apiVersion: v1
kind: Pod
metadata:
  name: movie
spec:
  restartPolicy: Never          # the pod ends when the film ends
  resourceClaims:
    - {name: screen, resourceClaimName: living-room-tv}
    - {name: audio, resourceClaimName: living-room-tv-speakers}
    - {name: render, resourceClaimName: living-room-gpu}
  volumes:
    - name: movies
      nfs: {server: nas.example.net, path: /movies, readOnly: true}
  containers:
    - name: mpv
      image: mpv
      args: [--fullscreen, --ao=pipewire, /movies/film.mkv]
      resources:
        claims: [{name: screen}, {name: audio}, {name: render}]
      volumeMounts:
        - {name: movies, mountPath: /movies, readOnly: true}
```

The pod names no machine and no device node. The claims deliver every
socket and environment variable, the compositor's and PipeWire's
included, and the scheduler places the pod on the machine that owns
the devices.

The example also shows the cost. Every film starts with this much
YAML again, and a controller adds more: a claim for its input nodes,
a driver pod that maps buttons to `mpv`'s IPC socket, and a shared
volume that puts the socket in both pods. No resource records which
devices belong together in a room; only the manifest's author knows.
This operator writes these manifests itself, driven by four
resources.

## The resources

**`Player`.** One named unit of equipment at one spot, for one
purpose: a lone speaker, a speaker pair, a TV with its speakers, a TV
with a receiver. A room holds several. The living room can hold
`living-room-theater` (the projector and the surround pair),
`living-room-voice` (one speaker for calls), and `living-room-gaming`
(the second TV). The spec selects the unit's devices with CEL
selectors like the example above: a display, audio sinks, a render
node. It states the device parameters the unit should get, such as
the display mode, the audio codec, and the panel brightness. UPnP
names the same unit a `MediaRenderer`, and Home Assistant names it a
`media_player`.

A `Player` runs no pod and holds no claims of its own. A pod exists
only while a `Play` does, so an idle unit's devices stay free for any
other workload. A `Remote` does get a pod of its own, and the Input
section below says why that pod runs whether or not anything plays.

A `Player` may exist that this operator never plays to. A gaming unit
groups a console's TV and controllers so that the equipment has a
name, and no `Play` ever names it.

**`Play`.** One run of media on a `Player`, with a lifecycle
analogous to a `Job`: it runs once to completion and stays for its
status until deleted. Create a `Play` to start it, delete the `Play`
to stop it early, and `kubectl get plays` lists what plays right
now. The spec
names the players and the media: 1..n URIs played in order, a film,
an album, or a season of episodes. The players field is
`spec.players`, a list validated to length one for now, because the
carriage layer below will let one `Play` reach several players.
Adding a second element to a list costs nothing, and renaming a field
is a migration. The status reports what the playback pod observes:
the item that plays now, a phase, a position, a failure.

**`Remote`.** One physical controller: a DualSense, a small RF remote.
The spec selects the device by attribute, names the `Keymap` for its
model, and lists bindings. Each binding names a `Player` and may
override the keymap, so one controller's buttons can map to different
actions on the theater and on the gaming unit. The relation is many
to many: a room's remotes can each bind to each of the room's
players.

**`Keymap`.** One model's table from buttons and axes to named media
actions: on a DualSense, cross means `play-pause`, the bumpers mean
`seek -10s` and `seek +10s`, the option button means `home`. A
`Keymap` is written once per model of controller and shared by every
`Remote` of that model. The right side is a set of named actions that
this operator defines. Raw `mpv` commands stay out of the API, so a
different player program can implement the same actions later.

## The playback pod

A `Play` reconciles into one pod. The operator resolves the URIs,
builds the claims from the `Player` spec, and adds the parts of the
example that never change: the disconnect tolerations, the
constraints that tie paired requests to one monitor, and the
per-container request names that keep each device out of the
containers that must not hold it.

The pod runs one opinionated player image: `mpv` under a thin
supervisor. `mpv` covers video to a display and music to a sink alike,
plays into the PipeWire socket a claim delivers, and serves a JSON IPC
socket the supervisor drives. The supervisor reports the phase and
position to the operator, and only the operator writes
`Play.status`. The reason is trust: the playback pod decodes media
from the network, which makes it the least trusted process in this
system, so it holds no API credentials at all.

The pod ends when the last item ends or the `Play` is deleted. The
operator's next reconcile decides what the display shows then: an
idle screen, or a kiosk.

## Media sources

The `Play` spec names its media as a list of URIs, and the operator
resolves each item's scheme into what the pod needs. The resolver is
the extension point: a new scheme is a new resolver entry, and the
`Play` spec never changes shape.

* `https://` resolves to an argument and nothing else. Any machine
  can stream it and seek in it with range requests. A small HTTP
  server in front of the NAS directories makes this the ordinary path
  for a library.
* `nfs://host/export/path` resolves to an inline NFS volume for
  `host:/export` plus a file path argument. The player reads the
  bytes through the kernel's NFS client.
* `snapcast://` arrives with the carriage layer below and resolves to
  a subscription instead of a file.

Media library management is a separate concern, and this project
does none of it. It keeps no catalog, no metadata, and no artwork,
and it never organizes the files behind the URIs. The URIs come from
whoever creates the `Play`: a person, or a future frontend with a
catalog of its own.

## Input

A controller stays paired between plays, and its radio and a
player's display can be on different machines. Both facts keep the
remote out of the playback pod.

Each `Remote` reconciles into one standing pod, pinned by its device
claim to the machine that owns its radio, running whether or not
anything plays. It reads the controller's input nodes and publishes
the raw button and axis events to a small message bus, likely MQTT.
The bus is the data plane and the operator is not on it: when the
operator restarts, buttons keep working.

Each playback pod runs one receiver container beside `mpv`. It
subscribes to the remotes bound to its player, applies the binding's
keymap, and drives the supervisor's IPC socket. One container holds
0..n subscriptions, because a pod's container set is immutable once
running: nothing can add a container per remote later, and a
new binding in the middle of a film must not restart the playback
pod. The operator updates the subscription list without touching the
pod.

One ambiguity is real: a remote bound to two players while both have
active plays would pause both. The operator arbitrates. It marks
exactly one binding active per remote, publishes the mark, and
receivers ignore events while unmarked. Focus is control-plane state;
presses stay on the data plane.

## One machine owns a player's devices

A `Play` becomes one pod, and DRA schedules that pod onto the machine
that owns every claimed device. So all of a `Player`'s devices must
share a machine: its display, and its sinks through that machine's
sound card or radio. A `Player` whose devices cannot co-locate gets a
status condition that says so before anyone plays to it. Most rooms
already satisfy the rule, because an HTPC beside the equipment owns
all of it. The carriage layer lifts the rule; placement itself has no
special case.

## The carriage layer comes later

Placement puts a player process next to the hardware. Carriage moves
streams between machines, and it has no API of its own: nobody
creates a stream resource. Carriage fulfills a `Play` that names two
players, the same way a pod fulfills a `Play` that names one.

For audio the transport is [snapcast](https://github.com/snapcast/snapcast):
one `snapserver` takes a stream and a `snapclient` beside each speaker
plays it in sync. Snapcast's latency is a configured constant, 1000 ms
by default, so `mpv` can feed the server early and delay its own video
by the same constant. That may make a film's audio playable through
speakers on another machine, at the cost that every pause and seek
takes one buffer length to reach the speakers. Whether lip-sync holds
on real hardware is a drill this repository owes.

Synced video across machines has no transport at a home scale. The
professional world does it with PTP clocks and SMPTE 2110. This
document names the direction and does not design it.

## Considered and set aside

**Always-on player pods.** A pod per `Player`, idle but ready, like a
Sonos box. Set aside because claims are exclusive: an idle theater pod
holds the TV that a console needs, and a pod starts in seconds when
the image is already on the machine.

**A player program per `Play`.** A spec field naming any image to run.
Set aside because it turns the operator into a generic pod launcher,
and Kubernetes is already one. One known `mpv` image lets the
supervisor report an exact phase and position; another player program
can come later and implement the same named actions.

**Actions routed through the operator.** The first input design sent
button presses up to the operator and down to the supervisor. Set
aside because it puts the control plane on the data path: an operator
restart would disconnect every remote in the cluster.

**Remotes as sidecars in the playback pod.** Set aside for two
reasons: a sidecar's input claim forces the playback pod onto the
radio's machine, and a running pod cannot add a container when a new
remote binds.

**Raw `mpv` commands in keymaps.** Set aside because the mapping
would leak the player program into the API forever. Named actions
cost a vocabulary this operator must define, and they keep the API
independent of any player program.

**HTTPS as the only media scheme.** Set aside in favor of a resolver
with `nfs://` beside it. The scheme list maps onto what Kubernetes
volumes can already mount, and flexibility here is cheap.

## Open problems

* **The default focus rule.** When a remote's bound players both start
  plays, which binding does the operator mark active? The most recent
  `Play` is the likely default. Undecided.
* **Cross-machine film audio.** The snapcast delay compensation above
  is unproven. It needs a drill with a measured buffer and a lip-sync
  check on real hardware.
* **Input that starts playback.** A standing remote exists when
  nothing plays, so a button could create a `Play`. That is the first
  sliver of a frontend, and it is deliberately out of scope until the
  frontend is designed.
* **The on-screen display.** `mpv`'s OSD is coarse. A richer overlay
  for volume, seeking, and menus is a later design.
* **The media server.** The HTTP server in front of the NAS is named
  here and owned nowhere. Whether it belongs in this repository or in
  a sibling is undecided.

## The picture

```
  you / a future frontend
        |
        |  create and delete resources, watch status
        v
  +----------------------+      +--------------------------------------+
  | Play                 |----->| Player "living-room-theater"         |
  |  players: [theater]  |      |  zone: living-room                   |
  |  uris: [nfs://...]   |      |  devices:                            |
  |  status:             |      |    display: hdmi TV     (selector)   |
  |    phase: Playing    |      |    sinks:   surround    (selector)   |
  |    position: 1:32:30 |      |    render:  gpu node                 |
  +----------------------+      |  params: mode, codec, brightness     |
                                |  (runs no pod of its own)            |
                                +--------------------------------------+
  +-----------+     +---------------------------------+
  | Keymap    |<----| Remote "living-room-dualsense"  |
  | dualsense |     |  device: (selector)             |
  +-----------+     |  bindings:                      |
                    |    - player: ...theater         |
                    +---------------------------------+
        |
        |  the operator resolves the uris, builds the claims,
        |  creates the pods, and writes every status
        v
  +--------------------------------------------------------------+
  | playback pod (one per Play)                                  |
  |  +---------------------+     +---------------------------+   |
  |  | mpv + supervisor    |     | receiver container        |   |
  |  |  claims: display,   |<----|  0..n bus subscriptions   |   |
  |  |   sinks, render     | ipc |  applies the Keymap       |   |
  |  |  volumes from uris  |sock |  honors the focus mark    |   |
  |  +---------------------+     +---------------------------+   |
  |  supervisor --> operator --> Play.status                     |
  |  pod ends --> Play finished --> display back to idle         |
  +--------------------------------------------------------------+
        ^
        |  raw button and axis events
  ==================== message bus (mqtt) =======================
        ^
        |
  +--------------------------------------------------------------+
  | remote pod (one per Remote, standing)                        |
  |  claims: the controller's input nodes                        |
  |  pinned to the machine that owns the radio                   |
  +--------------------------------------------------------------+

  ========= one machine owns ALL of a Player's devices ==========
  |  display-operator      audio / bluetooth-operator           |
  |   prepares the TV       prepares sinks + controller nodes   |
  ===============================================================

  MEDIA SOURCES                      CARRIAGE LAYER (later)
  +---------------------+            . . . . . . . . . . . . . . .
  | http server / NAS   |            . snapserver <- mpv audio    .
  |   https://...       |            . snapclient beside speakers .
  |   nfs://host/path   |            . multi-room sync, lifts the .
  +---------------------+            .   one-machine rule         .
                                     . . . . . . . . . . . . . . .
```
