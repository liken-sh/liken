# 25, Move idle-screen behavior into `media-screen`

Plan 22 put the idle screen's timers, its focus gate, and its shade in
a pod of their own, the idle command pod, because the pod also held the
keymaps and a client must not grow a keymap. Plan 24 moved the keymaps
into the standing remote pod and put the kernel's key names on the bus.
The command pod still implemented a small set of screen rules. Every
client that draws a screen is now a Rust program with its own bus
reader. This plan moves the remaining rules into those clients through
one shared crate and deletes the pod.

## What the command pod held

Between films the pod read every press of the unit's controllers and
gated each one on the controller's focus mark. It brought the shade
down when the quiet window ran out and lifted it on a press. It stated
the panel desire when the off window ran out. It stepped the volume,
asked the operator for a focus cycle, and forwarded the navigation
presses to a delegate's client on the `Player`'s commands topic. It
told the client each of those moments on a screen topic, and it read
the client's one request, a sleep, off the commands topic.

Each operation uses received messages and the current time.
None needs a separate process once the client can read the bus. Both
clients can do so: the idle screen in this repository and the library
layer's media browser. A press now stays within the client instead of
crossing two processes and two topics.

## The crate

`media-screen` is a Rust library in this repository and a Cargo
workspace member next to the idle client. It provides bus integration
for one `Player`'s screen client. It names no toolkit, so each client
draws with the toolkit it chose.

It has two halves. `Screen` is pure. It takes each message with the
time when it arrived and returns what the client draws and what the
crate publishes. `Reader` runs in a thread and reads messages from the
broker. It subscribes on every session and folds each message through
`Screen`. It runs the two deadlines on its own clock and performs the
publishes. It gives the client the events it draws: a press under the
kernel's key name, the shade down or up, a focus, and a fresh surface.
The client requests the shade through the crate's local API. This is
the only behavior it requests. Separately, the client publishes on only
its own topic.

The rules are the command pod's rules, and its tests moved with them.
A press acts only while the controller's mark names this `Player`,
only while the unit plays nothing, and only while the screen is awake.
A press on a sleeping screen wakes it and does nothing else. The
quiet window runs from the last press and brings the shade down; the
off window runs from the shade and states the off desire; a wake
states the on desire. The volume keys step the level on the volume
topic, and the cycle key publishes the cycle request on the
controller's own cycle topic. The re-present arrives on the commands
topic and becomes the fresh surface.

The idle client in this repository takes the crate by path. The media
browser takes it as a git dependency pinned to a release tag of this
repository, the same pin discipline a cluster's overlay keeps.

## What the operator writes

The operator runs one fewer pod. Under its own controller, it maintains
the claim and runs the idle client pod. The pod's container receives the
complete contract the crate reads. That contract is the bus address, the
`Player`'s object name, the status, volume, commands, and panel topics,
each controller's events and focus topics in `spec.remotes` order, and
the two resolved windows. Under a delegate, the operator maintains only
the claim. `status.idle` carries the same contract, so the delegate's
operator sets the same variables on its own container.

The screen topic is gone, and so is the `sleep` request. The commands
topic carries the operator's `re-present` and nothing else. For one
release the reconcile also deletes the `<player>-idle-command` pod an
older release created, because a live one would step the volume beside
the client.

## Considered and set aside

A crate in the brand repository beside `liken-iced`. That crate is the
look, and it is shared by a submodule. The bus contract belongs to the
operator that owns the bus, the `Player`, and `status.idle`, and a
release tag of this repository is the pin a consumer wants.

A crate of its own on crates.io. That is a public API commitment with
one consumer, and it separates the contract from the operator that
changes it.

A retained shade. The command pod retained the shade on the screen
topic, so a client that restarted read the shade it left. The shade is
in-process now, and a client that restarts starts awake and starts the
quiet window again. The command pod did the same on its own restart.

## How the work is proved

The crate's tests prove every rule with no broker and no thread, and
the reader's tests prove the subscribe, the publish, and the clock
against canned connection events and a listener on loopback. The Go
tests prove the pod carries the contract, the status carries the same
one, and a pod from an older release is deleted once.

On `liken-1`, a unit's idle screen fades and wakes on its own timers
with no command pod in the namespace. The panel goes dark at the off
window and lights on a press. A volume key steps the level once per
press, and the cycle key moves the mark. When a film ends, the clock
returns on a fresh surface. The media browser on the same crate
navigates, sleeps at its top level, and plays a film from the list.

### The drill record

Released as media-operator 2026.09.02-002 and library-operator
2026.09.02-002, and rolled to `liken-1` together on 2026-09-02. The
first pass deleted the two `-idle-command` pods an older release
created. The idle screen and the browser started on the new images
with the whole contract in their environment. `status.idle` on the
delegated unit carried both windows, both remotes, and the panel and
volume topics.

The X6 drill ran the same day. The browser navigated, and a film played
from a chosen cover. When it ended, the browser returned at once. The
old path took five to fifteen seconds and required a restart. The
remaining wait was the start of a film: one to five seconds from
select to the `Play` running, which is the playback pod's own start.
The drill did not time the fade, the off window, or the volume keys.
