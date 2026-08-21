# Keymaps onto the bus

Plan 05. It gives every `Play` an abstract command topic, splits the
playback pod's one bridge into a command sidecar and a translator
sidecar per controller, and moves the keymap off an environment
variable and onto the bus as retained state. When this plan lands, any
program on the bus can command a `Play` in media terms, and a `Keymap`
edit reaches a running film with no pod restart.

## The problem

The keymap is carried in an environment variable, and the environment
variable has three costs. A keymap edit is baked into the pod at
creation, so remapping one button forces the pod to restart. The one
bridge sidecar both applies the keymap and drives `mpv`, so the two
jobs cannot scale apart. And the named action, the `play-pause` or the
`volume-up` a keymap produces, never appears anywhere a second thing
could reach it, so nothing but a bound `Remote` can command a `Play`.

The design keeps the third one open. A future frontend, a phone, or a
Home Assistant integration should command any `Play` in the same media
terms a controller uses, without holding a controller. That calls for
the named actions to travel on the bus, on a topic any program can
publish. Once they do, the keymap becomes a translation stage that
turns a controller's raw codes into those actions, and it is no longer
part of the process that drives `mpv`.

## The command topic

`liken/media/plays/<namespace>/<name>/commands` carries the named
media actions, the same words a `Keymap`'s right side names:
`play-pause`, `volume-up`, `seek-forward`, and the rest of the
vocabulary this operator defines. It is not `retained`, because a
command is an event and not a state. Any client that reaches the
broker may publish to it, and that open surface is the design's intent:
the command topic is the one place a program joins a `Play` in media
terms.

Nothing checks that a command came from a bound controller. The trust
boundary is the whole cluster, and that is
`open-problems/the-bus-authorizes-nothing.md`. Broker access control
is the answer when a cluster runs a workload its owner does not trust,
and this plan does not build it.

## The bridge splits into two sidecars

Plan 03's one bridge did two jobs across the IPC socket: it applied
keymaps and wrote commands to `mpv`, and it read `mpv`'s properties and
published the status. This plan splits the two along the command topic.

The **command sidecar** is the pod's one owner of the IPC socket. It
subscribes to the `Play`'s command topic, writes each named action to
`mpv`, reads `mpv`'s property changes back, and publishes the status
and availability reports exactly as plan 03's bridge did. It is a
native sidecar, an init container with `restartPolicy: Always`, so a
crash restarts it alone and it resubscribes to the retained status
without ending the film.

A **translator sidecar** runs per controller the `Player` names. It
subscribes to its `Remote`'s events topic and to its `Keymap` topic,
applies the keymap to the raw evdev codes, and publishes the named
actions to the `Play`'s command topic. It never touches the IPC
socket. It subscribes to the controller's focus topic too, and it acts
on every press for now, because a unit names one controller and there
is no focus to honor. Plan 06 makes the gate live.

The pod is then `mpv`, one command sidecar, and one translator sidecar
per named controller. With one controller that is three containers.
The container set is immutable, so adding a controller reshapes the pod
and the operator recreates it. That recreate is graceful because plan
04 made it graceful: the film resumes at its place within about a
second.

## Retained keymaps on the bus

The operator compiles each `Keymap` and publishes it to a keymap topic,
`retained`. A translator sidecar subscribes and reads the current
keymap the instant it connects, and a `Keymap` edit is one retained
publish that every subscriber applies with no pod restart. The
environment-variable keymap is gone, and with it the restart a remap
used to cost.

A retained publish reaches a subscriber at once, so this is the
deterministic delivery a mounted file could not give. A file from a
configmap updates only on the kubelet's sync, tens of seconds later,
and a restart to force it races that sync and can re-read the old file.
The bus carries the value itself, so a restarting sidecar reads the
current keymap on connect and never races a file.

## The Keymap becomes cluster-scoped

A `Keymap` is one controller model's table, written once per model and
shared by every `Remote` of that model. That is a vocabulary shared
across every room, not a resource that belongs to one. Kubernetes
models a shared vocabulary as a cluster-scoped resource, the way a
`DeviceClass`, a `StorageClass`, and a `PriorityClass` are cluster
scoped. This plan changes the `Keymap` CRD from `Namespaced` to
`Cluster`.

A `Remote` in any namespace then names one `Keymap` by name, and a
namespaced object naming a cluster-scoped one is a normal, safe
reference, the same shape a `PersistentVolumeClaim` uses to name a
`StorageClass`. There is no owner reference to break, because a
`Remote` does not own its `Keymap`. The keymap topic drops the
namespace segment it never needed: `liken/media/keymaps/<name>`. Every
translator sidecar of that model subscribes to the one topic, and the
operator publishes the compiled table there once.

## Set aside for this plan

* **Focus and several controllers per unit.** A `Player` still names
  one controller, so no command topic has two translators and the focus
  gate stays inert. Plan 06 lifts the count and makes the gate live.
* **The per-unit keymap override.** A `Remote` still resolves one
  `Keymap` for its unit. Plan 06 fills the reserved slot, and an
  override is just a different shared `Keymap` topic the translator
  subscribes to.
* **Broker access control.** Any client may publish a command or a
  keymap or an event. That is
  `open-problems/the-bus-authorizes-nothing.md`.
* **Home Assistant discovery.** The command topic is the surface a
  Home Assistant integration would publish a `media_player`'s commands
  to, and this plan publishes no discovery configs. That is
  `open-problems/the-player-is-not-a-home-assistant-entity.md`.

## How it will be proved

On `liken-1`, with one monitor and a paired DualSense, one `Play`
running.

The drill checks each claim:

* The X button pauses the film, now over the command topic through the
  translator and the command sidecars, not the old environment
  variable.
* A `play-pause` published by hand to the `Play`'s command topic, with
  no controller in the loop, pauses the film. This is the generic
  surface the design named.
* The `dualsense` `Keymap` is edited mid-film to remap a button. The
  new mapping applies with no pod restart, because the operator
  republishes the retained keymap and the translator re-reads it on
  the spot.
* The command sidecar is killed. It restarts alone, resubscribes to the
  retained status, and the film plays on.
* The `Keymap` is read from any namespace as a cluster-scoped resource,
  and a `Remote` names it with no namespace qualifier.
