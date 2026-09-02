# 25, The screen client holds the rules

Plan 22 put the idle screen's timers, its focus gate, and its shade in
a pod of their own, the idle command pod, because the pod also held the
keymaps and a client must not grow a keymap. Plan 24 moved the keymaps
into the standing remote pod and put the kernel's key names on the bus.
What the command pod still held was small, and every client that draws
a screen is now a Rust program with a bus reader of its own. This plan
moves the rest of the pod into the clients, through one crate they
share, and deletes the pod.

## What the command pod held

Between films the pod read every press of the unit's controllers and
gated each one on the controller's focus mark. It brought the shade
down when the quiet window ran out and lifted it on a press. It stated
the panel desire when the off window ran out. It stepped the volume,
asked the operator for a focus cycle, and forwarded the navigation
presses to a delegate's client on the `Player`'s commands topic. It
told the client each of those moments on a screen topic, and it read
the client's one request, a sleep, off the commands topic.

Each of those is a rule over what arrived and what time it is. None of
them needs a process of its own once the client can read the bus, and
both clients can: the idle screen in this repository and the library
layer's media browser. A press that crossed two processes and two
topics now crosses none.

## The crate

`media-screen` is a Rust library in this repository, a Cargo workspace
member next to the idle client. It is the bus half of a screen client
for one `Player`, and it names no toolkit, so a client draws with
whatever it chose.

It has two halves. `Screen` is pure: it takes each message with the
time it arrived and returns what the client draws and what the crate
publishes. `Reader` is the thread over the broker. It subscribes on
every session, folds each message through `Screen`, runs the two
deadlines on a clock of its own, performs the publishes, and hands the
client the moments it draws: a press under the kernel's key name, the
shade down or up, a focus, and a fresh surface. The client asks for
one thing, the shade, and publishes on one kind of topic, its own.

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

The operator stands one pod fewer. Under its own controller it stands
the claim and the idle client pod, and that pod's container carries
the whole contract the crate reads: the bus address, the `Player`'s
object name, the status, volume, commands, and panel topics, each
controller's events and focus topics in `spec.remotes` order, and the
two resolved windows. Under a delegate the operator stands the claim
alone, and `status.idle` carries the same contract, so a delegate's
operator sets the same variables on its own container.

The screen topic is gone, and so is the `sleep` request. The commands
topic carries the operator's `re-present` and nothing else. For one
release the reconcile also deletes the `<player>-idle-command` pod an
older release stood, because a live one would step the volume beside
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

On `liken-1`: a unit's idle screen fades and wakes on its own timers
with no command pod in the namespace, the panel goes dark at the off
window and lights on a press, a volume key steps the level once per
press, the cycle key moves the mark, a film ends and the clock returns
on a fresh surface, and the media browser on the same crate navigates,
sleeps at its top level, and plays a film from the list.
