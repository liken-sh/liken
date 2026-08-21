# A play becomes a pod

Plan 01. Built, and drilled on `liken-1` on 2026-08-20, in release
2026.08.20-001. The first slice of the design: `Player` and `Play`,
the operator that reconciles them, and the player image. No input,
no carriage. The proof was a film on `liken-1`, started by `kubectl
create` and stopped by `kubectl delete`.

## The problem

The design exists and nothing runs. [The founding
design](00-design.md) names four resources, and the smallest set that
proves the idea is two: a `Player` that names the equipment, and a
`Play` that runs media on it. Everything later, remotes, keymaps, the
bus, carriage, drives or extends the pod this plan creates. So the
pod comes first.

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

## How it was proved

On `liken-1` on 2026-08-20, with the portable BOE monitor and its
built-in speakers, in release 2026.08.20-001. A `Player` named
`lab-portable` selected the monitor and its speakers by
`monitor.liken.sh/id` through the cluster's `display-output` and
`audio-output` classes, and the render node through
`display-render`. A `Play` named one `nfs://` URI on the NAS. The
path holds spaces and brackets, in either the raw name or its
percent-encoded form: the resolver parses the URI and reads the
decoded path, so both reach the mount as the same bytes.

The claims allocated, the pod scheduled, and the phase reached
`Running` about forty seconds after the create, most of it the
first pull of the player image. `kubectl get plays` showed item 1
of 1 and the position advanced from 0:00:15 to 0:00:25 across two
reads ten seconds apart, against a reported duration of 2:28:49.
mpv's own log carried the three delivery paths: hardware decoding
with vaapi, `VO: [gpu] 3840x1744 vaapi[p010]`, and `AO: [pipewire]
48000Hz 5.1(side) 6ch`.

Deleting the `Play` ended the run and the garbage collector took
the rest: twenty seconds later the playback pod and the
`space-odyssey-devices` claim were gone, and nothing of the run
remained in the namespace.
