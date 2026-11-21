# Working on media-operator

This directory contains `media-operator`, a Go Kubernetes operator
with screen programs in Rust. `README.md` says what it is for and
describes its kinds.

## How it runs

The operator is a `Deployment`, and a Lease makes sure that only one
replica acts at a time. It creates the `ResourceClaim`s and pods that
each `Player`, `Play`, and `Remote` needs, and it writes the status of
each of its kinds. It plays no media in its own process.

Several of the operator's pods run the pod build of this Go program.
The pod build is the same code built with the build tag `pod`, and
it leaves out leader election. For each `Play`, the operator creates
a playback pod. In that pod, the `player` container builds the
arguments for `mpv` and runs `mpv`. The `command` sidecar is the only
program that writes to `mpv`'s IPC socket. It reads presses and
commands from the bus, and it publishes the report of the run. The
`display` sidecar runs the Rust program in `display/`, which draws
the on-screen display over the film. An idle pod runs the Rust
program in `idle/`, which links the `media-screen` crate. The pod of
each `Remote` publishes each press of its controller.

Presses, commands, reports, and screen requests go through the
Mosquitto broker in `deploy/bus.yaml`. A screen client from another
component, such as `library-operator`'s media browser, reads and
publishes the same topics. The `media-api` `Deployment` serves the
capture of a `Player`. It muxes the screen from `display-operator`'s
API with the sound from `audio-operator`'s API. The `DaemonSet` in
`capabilities/` reads through libva what the media driver of each
render node can do. It publishes a `media.liken.sh` device for each
render node with those facts.

## Design

The pods that the operator creates have no Kubernetes credential. The
playback pod decodes media from the network, so it is the least
trusted process in the system. Each pod sends its reports on the bus,
and only the operator writes them to the API server. A press goes on
the bus from a `Remote`'s pod to the playback pod or the idle screen,
not through the operator. So remotes keep working while the operator
restarts.

The hardware operators own their devices and their kinds. To change a
device, this operator sets the fields that the owning operator reads,
and the owning operator applies the change to the device. This
operator's watches of their kinds are optional, so it runs on a
cluster that does not have them. When this operator needs a new fact
about a device, the operator that owns the device publishes the fact,
and this operator reads it. Claims, `ResourceSlice`s, and CDI files
are the interfaces between this operator and `liken`.
`library-operator` reads this operator's kinds and its bus, and this
operator reads nothing from `library-operator`.

## Finding things

- `keycodes.go` is generated from the kernel's `input-event-codes.h`.
  `make codes` regenerates it.
- `openapi.json` is the OpenAPI document of `media-api`. A test fails
  when it differs from the router, and `go generate` regenerates it.
  `docs/Makefile` generates the route reference from it.
- `patterns/` contains the committed test patterns. `make patterns`
  creates the missing files, and their names match those in
  `patterns.go`.
- `topics.go` builds every bus topic, and
  `docs/content/docs/reference/bus.md` lists each topic with its
  writer and its readers.
- `wire.go` and `media-screen/src/wiring.rs` declare the same
  environment variables, so a change to one needs the same change in
  the other.
- `transect/` contains the kind knowledge that `transect` reads for the
  `Player` and the `Remote`, in `kinds.yaml`. The `Remote` states its
  shape, the library's `handheld`, and the `Player`'s roles take the
  shape of each part's kind unless a role states one. The `Player`'s
  roles are stated once, and its elevations, the room and the signal
  path, place them. The signal path's wires follow `status.receiver`,
  and the `Remote` is as healthy as its `Peripheral`. A change to a
  condition, a printer column, or a status field that the file names
  also changes the file.
