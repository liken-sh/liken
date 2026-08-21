# The bus carries input and reports

Plan 03. The third slice of the design. It stands up the message bus
the founding design named, moves the remote reader off the playback
pod and onto its own standing pod, and rebuilds the playback pod
around `mpv` itself with one sidecar. When this plan lands, a
controller keeps working across an operator restart, a `Remote`
survives the `Play` it drives, the operator serves no HTTP endpoint,
and no supervisor process wraps the player.

## The problem

Plan 02 proved input with a sidecar, and named the two costs it paid
to do so. The reader's device claim forces the playback pod onto the
radio's machine, so a controller on one machine and a display on
another cannot pair. And a pod's container set is fixed once it runs,
so a `Remote` bound in the middle of a film joins only at the next
`Play`. Both costs come from one choice: the reader runs in the
playback pod. The design already holds the answer. A standing pod per
`Remote`, pinned to the radio's machine, publishes button events to a
bus, and the playback pod subscribes.

There is a second data plane to fold in here. The supervisor reports
what `mpv` is doing with a plain HTTP `POST` to an endpoint the
operator serves. That endpoint exists for one message type, its
in-memory token table does not survive an operator restart, and a
drill saw a film's position stop advancing when the operator
restarted while `mpv` kept playing. A bus that already carries input
can carry the report too, and the operator stops serving HTTP.

This plan takes a third thing the sidecar work made visible: the
supervisor. Plan 01 runs `mpv` as a child of a supervisor process
that is the pod's PID 1. That shape forces the supervisor to relay
the kubelet's signals to `mpv`, to reap `mpv`'s children, and to
carry `mpv`'s exit code out as the pod's outcome. All three are the
container runtime's own work, and the supervisor does them only
because it, and not `mpv`, is the container's process. This plan makes
`mpv` the container's process, and the kubelet does the three.

## The bus

The bus is [Mosquitto](https://mosquitto.org/), an MQTT broker. It
runs as one `Deployment` and one `Service` in the operator's
namespace, named `bus`, separate from the operator's own
`Deployment`. The two are separate so the operator restarts without
dropping a message. The broker and the two pods that use it stay up,
and a button press reaches `mpv` while the operator is down.

An in-cluster `bus` is the default, and an external broker works too.
Many homes
already run one MQTT broker for Home Assistant and its devices, and a
second broker beside it is waste. Pointing the operator at an existing
broker is a mode this plan does not build. It is an open problem,
`open-problems/the-broker-is-always-in-cluster.md`, and the topic and
auth choices below are made so that mode stays reachable.

MQTT is the choice over the one real alternative, NATS, for a reason
outside this repository. MQTT is the native protocol of home
automation, and Home Assistant maps an MQTT topic tree onto entities
through its discovery convention. A `Remote`, a `Player`, and a
`Play` that already speak MQTT are one wiring step from appearing in
a home's automation, and that is worth more than the three
megabytes the NATS image saves. The `bus` image is nine and a half
megabytes and its runtime memory is a few megabytes, so its cost on a
one-gigabyte machine is noise beside k3s.

The broker keeps no volume. Input events are momentary and want no
persistence. Reports are published `retained`, so the broker holds
the last report per `Play` in memory and delivers it to the operator
the moment the operator subscribes. A broker restart drops the
retained set, and the next report from each running `Play` refills it
within seconds.

## The topics

Each topic extends a base the operator holds as one string,
`liken/media` by default.
One configurable base is the shape
[zigbee2mqtt](https://www.zigbee2mqtt.io/) uses, and it keeps a Home
Assistant integration clean: the data topics stay under this one
base, and Home Assistant's own discovery topics stay under
`homeassistant/`, so neither tree constrains the other. A base that
includes a cluster's name, so several clusters can share one broker
without collision, is a later refinement the string already allows.
That refinement is `open-problems/one-broker-for-many-clusters.md`.

* `liken/media/remotes/<namespace>/<name>/events` carries one
  `Remote`'s raw button and axis events. The standing remote pod
  publishes, not `retained`, because a press is an event and not a
  state. The keymap stays off this topic, so the events are the
  controller's own evdev codes, and one `Remote` can feed two
  players that map it differently.

* `liken/media/plays/<namespace>/<name>/status` carries one `Play`'s
  report: the paused flag, the item, the position, and the duration.
  The playback pod's sidecar publishes, `retained`, so a restarted
  operator reads the current position back from the broker and does
  not lose a running `Play`'s place. The sidecar clears the topic when
  `mpv` ends, so a finished `Play` leaves no report that reads as
  still playing.

* `liken/media/plays/<namespace>/<name>/availability` carries
  `online` or `offline` for the sidecar that publishes the status.
  The sidecar names this topic as its MQTT Last Will when it connects,
  with `offline` as the will payload, and publishes `online` once it
  is connected. The broker then publishes `offline` on any disconnect
  the sidecar does not make cleanly, so a retained status left behind
  by a killed pod does not read as a live `Play`. This is the same
  signal Home Assistant reads to mark an entity unavailable, so the
  availability that keeps a bus consumer honest is the availability a
  Home Assistant integration needs. Retained status without it is the
  ghost entity the Home Assistant documentation warns about.

* `liken/media/remotes/<namespace>/<name>/focus` carries the mark
  that says which binding is active. It is `retained` control-plane
  state the operator writes. This plan publishes nothing to it,
  because a binding list of length one leaves no focus to arbitrate.
  The topic is named now so the sidecar can subscribe to it from the
  start and the mark can arrive in a later plan without a pod
  restart.

## The standing remote pod

Each `Remote` reconciles into one pod, owned by the `Remote` through
an owner reference, so deleting the `Remote` tears the pod down. The
pod holds the controller's device claim, which pins it to the machine
that owns the radio and runs it whether or not anything plays. The
container is the same image plan 02 built, in the reader mode plan 02
added. It reads the claim's event nodes and publishes each event to
the `Remote`'s events topic.

The pod tolerates the controller's sleep the way the sidecar did. The
`bluetooth.liken.sh/disconnected` taint is `NoExecute`, and the pod
tolerates it with no time limit, so a sleeping controller never ends
the standing pod. The `bluetooth.liken.sh/no-input-node` taint is
`NoSchedule` and the pod does not tolerate it, so the pod stays
`Pending` until the controller first connects, then schedules and
runs across every sleep after. The reader reopens the event nodes
when a sleeping controller wakes, because the nodes disappear on sleep
and return on connect while the pod keeps running.

## The playback pod is mpv and one sidecar

`mpv` is the pod's main container, and it is the container's own
process, so the kubelet sends it `SIGTERM` at the start of the grace
period and `mpv` quits on its own handler. The container's exit code
is `mpv`'s exit code, and a zero code is a `Play` that ran to the
end, the way a `Job` completes. No process wraps `mpv`, and the
signal relay, the child reaping, and the exit-code carrying that the
plan-01 supervisor did are the kubelet's work now.

One piece of the supervisor's job is real and stays, in the smallest
form. The display operator writes `DISPLAY_APP_ID` into the
container's environment at run time through its CDI spec, after the
pod spec is fixed, and `mpv` reads the value only as a
`--wayland-app-id` flag, not from the environment. So the container's
entrypoint is a small shim that reads that one variable, appends the
flag, and `exec`s `mpv`. Because it `exec`s, the shim replaces
itself, and `mpv` is the process the kubelet drives. The shim builds
the rest of `mpv`'s arguments the same way plan 01 did, from the
items and from `spec.start`.

The sidecar is the pod's one bus client, a native sidecar, an init
container with `restartPolicy: Always`, so it starts before `mpv` and
a crash restarts it alone without ending the film. It reaches `mpv`
through the IPC socket on an `emptyDir` the two containers share. It
does two jobs across that socket:

* It subscribes to the events topic of every `Remote` bound to its
  player, applies each remote's compiled keymap, and writes the named
  action's command to the socket. One subscriber holds zero or more
  subscriptions, because the container set is immutable and a new
  binding must not restart the pod. The operator resolves each bound
  `Remote`'s `Keymap`, compiles the table, and passes the set in an
  environment variable, so the map is as immutable as the container
  set and the pod holds no API credentials.

* It reads `mpv`'s property changes off the same socket, the paused
  flag and the item and the position and the duration, and publishes
  the report to the `Play`'s status topic. It throttles the position
  the way the supervisor did, so a report is one status write and not
  a rewrite as fast as the player counts.

## The report moves onto the bus

The sidecar publishes its report to the `Play`'s status topic instead
of `POST`ing it. The operator subscribes to
`liken/media/plays/+/+/status` and writes each report into the
`Play`'s status, exactly as the reconcile pass reads it today.

The trust boundary the HTTP shape drew stays, because it is the
correct boundary and not an artifact of HTTP. The playback pod
decodes media pulled off the network, so it is the least trusted
process in the system, and it holds no Kubernetes credentials. Only
the operator writes a `Play`'s status, and the sidecar reaches the
control plane through the operator's subscription and no other way.
The pod authenticates to the broker with whatever the broker asks
for, and nothing more: the in-cluster `bus` accepts the cluster's own
pods, and a bring-your-own broker states its own credentials, which
the operator passes to the pod. The bespoke per-run token the HTTP
endpoint minted is gone, because the broker's own auth is the auth
this plan uses.

Two parts of the plain-HTTP shape go away. The operator serves
no HTTP endpoint and needs no `Service` for one message type. And the
restart recovery the drill found broken is fixed by the `retained`
report: a restarted operator subscribes and reads the current
position of every running `Play` back from the broker, so a film's
position keeps advancing across the restart.

## What plan 01 and plan 02 this retires

* The supervisor process. `mpv` is the container's process, and the
  signal relay, the child reaping, and the exit-code carrying move to
  the kubelet. The one part that stays is the argument shim, which
  `exec`s `mpv` and does not outlive it.
* The sidecar reader in the playback pod. The reader runs in the
  standing remote pod, and the playback pod's sidecar is the bus
  bridge instead.
* The operator's HTTP report endpoint, its `Service`, its in-memory
  token table, and the per-run token itself. The broker's own auth
  takes their place.
* The `Remote` and `Keymap` resources do not change. The design
  promised that the bus retires the sidecar and keeps the resources,
  and only where the reader runs changes.

## Set aside for this plan

* **Focus arbitration.** A binding list of length one means a
  `Remote` drives exactly one player, so no press reaches two plays.
  A player may still name several remotes, and the sidecar's zero or
  more subscriptions already cover that. The focus topic is named and
  unused until a `Remote` may bind more than one player.
* **Per-binding keymap overrides.** A `Remote` still resolves one
  `Keymap` for all of its bindings.
* **Child reaping in the playback pod.** `mpv` is PID 1 of its
  container and is not a general init. The current media schemes,
  `https://` and `nfs://`, spawn no helper process, so nothing
  orphans. A scheme that needs a subprocess later, such as a
  downloader, reopens the question of a minimal init in the player
  image.
* **A broker outside the cluster.** This plan stands up the in-cluster
  `bus` and points every pod at it. A broker a home already runs is
  `open-problems/the-broker-is-always-in-cluster.md`.
* **One broker for many clusters.** The base topic is one string, so a
  cluster's name can join it later. Until then two clusters on one
  broker collide, and that is
  `open-problems/one-broker-for-many-clusters.md`.
* **TLS on the bus.** The in-cluster network is the boundary, and the
  playback pod holds no credential a TLS session would protect.
* **Home Assistant discovery.** The topic tree and the availability
  signal are chosen so Home Assistant can read them, and this plan
  publishes none of the discovery configs that would make a `Play`
  visible in Home Assistant. That is
  `open-problems/the-player-is-not-a-home-assistant-entity.md`, which
  also covers each `Player` publishing its own retained status.
* **Input that starts playback.** A standing remote pod now exists
  when nothing plays, so a button could create a `Play`. That is the
  first sliver of a frontend and stays out of scope until the
  frontend is designed.

## How it will be proved

On `liken-1`, with the portable BOE monitor and a paired DualSense,
the way plan 02 was proved. The `bus` `Deployment` runs. The
`dualsense` `Keymap` and the `Remote` from plan 02 stay unchanged.

The drill checks each claim the design makes:

* The standing remote pod schedules once the DualSense connects and
  runs with no `Play`. This is the fact plan 02 could not express: a
  reader that outlives the film.
* A `Play` on `lab-portable` starts, and the playback pod schedules
  with `mpv` and one sidecar and no supervisor. A press of the X
  button flips `status.paused`, and the position advances in the
  status, both carried over the bus.
* `mpv` ends on its own at the film's end, and the pod reaches
  `Succeeded` on `mpv`'s exit code with no wrapper in between.
* The playback pod is killed, not stopped. The `Play`'s availability
  topic flips to `offline` from the sidecar's Last Will, so the stale
  retained status does not read as a live `Play`.
* The operator is killed mid-film. `mpv` keeps playing, and the
  position keeps advancing in the status when the operator returns,
  because the report is `retained`. This is the recovery the
  plan-02-era HTTP drill saw fail.
* The `Remote` is deleted, and its standing pod tears down through
  the owner reference.

The open problem `open-problems/the-playback-pod-reports-over-plain-http.md`
closes when this drill passes.
