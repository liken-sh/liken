# A play becomes a pod

Plan 01. Built; the drill below has not run yet. The first slice of
the design: `Player` and `Play`, the operator that reconciles them,
and the player image. No input, no carriage. The proof is a film on
`liken-1`, started by `kubectl create` and stopped by `kubectl
delete`.

## The problem

The design exists and nothing runs. [The founding
design](00-design.md) names four resources, and the smallest set that
proves the idea is two: a `Player` that names the equipment, and a
`Play` that runs media on it. Everything later, remotes, keymaps, the
bus, carriage, drives or extends the pod this plan creates. So the
first bite is the pod.

## The resources

`Player` declares one unit of equipment: CEL selectors for its
display, its audio sinks, and its render node, plus the device
parameters the unit should get, such as the display mode and the
audio codec. It runs no pod and holds no claims of its own.

`Play` names one `Player` in `spec.players`, a list validated to
length one, and the media in `spec.uris`, 1..n URIs played in order.
The status reports the phase, the current item, the position inside
it, and a `paused` field.

The phase vocabulary is `Pending`, `Running`, `Finished`, and
`Failed`, the same words Jobs and Pods use. `Running` rather than
`Playing`, because a phase moves forward and never returns, and a
paused film would force `Playing` to flap. The pod performs the play
whether or not the media is paused, so `Running` stays true and the
`paused` field beside the position says the rest.

## The operator

The operator watches `Play` resources. For each one it resolves the
URIs, builds the claims from the `Player` spec, and creates one
playback pod. The resolver ships two schemes: `https://` becomes an
argument, and `nfs://host/export/path` becomes an inline NFS volume
plus a file path. The operator writes every status; the playback pod
holds no API credentials.

A `Player` whose devices cannot co-locate gets no condition in this
plan. The `Play` parks `Pending` because the pod cannot schedule,
which states the same fact with less machinery. The condition can
come when the operator learns to explain it.

## The player image

One image: `mpv` under a thin supervisor. The supervisor starts `mpv`
with the resolved media, drives its JSON IPC socket, and reports the
phase, item, position, and paused state to the operator. Nothing
pauses in this plan, because input arrives in a later plan, but the
status carries the field from the start so the input plan changes no
schema.

## The scaffolding

The repository grows what its siblings have: a Go module, the
Dockerfile for the operator and player images, deployment manifests,
CI that builds and releases on a tag, and the brand submodule it
already carries.

## Set aside for this plan

* `Remote`, `Keymap`, the message bus, and the receiver container.
  The film stops by deleting the `Play`.
* The idle screen. When no `Play` holds a display, the display shows
  whatever the compositor shows.
* Multiple players per `Play`, and every part of the carriage layer.

## How it will be proved

On `liken-1`, with the portable monitor and its built-in speakers.
Declare a `Player` for that monitor, its speakers, and the render
node. Create a `Play` with one `nfs://` URI. The phase reaches
`Running`, the film shows on the monitor with sound from its
speakers, and the position in `kubectl get plays` advances. Delete
the `Play`; the pod ends and the claims release. The drill record
lands in this document when it runs.
