# An arranger inside each region

Plan 31. Proposed and built on 2026-10-27. It is step 1 and step 2 of
[root plan 85](../../../plans/completed/85-motion.md), motion on the screen, for
this operator: the animation engine in the layout module, the
arrangement protocol, the arranger in the `Display`'s status, and
weston built from its upstream release. Root plan 85 holds the
reasons for motion in the compositor and the evidence from `liken-1`.
Drill 3 ran on `liken-1` on 2026-10-27, and drill 4, a `Layout` fade
measured at the panel's refresh rate, has not run.

## The problem

A `Layout` places each claim's windows in one rectangle, and every
window of the claim fills it (plan 17). A video player that shows its
film and a panel of its own controls in one region can only stack
them at full size. The up-next card of
[library-operator plan 84](../../../library-operator/plans/completed/84-a-larger-up-next-card.md)
needs the film to step back into a frame while the card takes the
right half of the region, and to step forward again on Back, in the
next frame after the press. A `Layout` edit goes through the API
server and this operator's pass, which takes a few hundred
milliseconds each way, and a `Layout` belongs to the person who wrote
it, not to the workload.

The module's transitions are ivi-layout's own. They step at 30 frames
a second with a sine ease, whatever the panel's refresh rate is
(`ivi-layout-transition.c:163` in weston 16.0.0), so every fade and
glide of plan 18 is uneven on a 60 Hz panel.

## The design

### Two owners of a screen

The `Layout` that a `Display` names owns the screen. This operator
gives each claim a region, and no workload can move a region. That
does not change.

Inside its region, a claim's windows belong to one arranger: the first
client of the claim that asks for the protocol `liken_arrangement_v1`
(`weston/layout/liken-arrangement-v1.xml`). The arranger states where
each window of the claim shows inside the region, how opaque it is,
and in what order the windows stack. The compositor moves every window
from where it shows to where the arrangement puts it. An arrangement
never moves a window outside the region and never scales one above
the region's size.

### The engine

`liken-layout.so` gets one animation engine. It advances on each
repaint of an output, so each step lands on a vsync of that output,
and a move is a function of the clock: a start, a length, and a curve.
Nothing that moves keeps state from one frame to the next, so a new
target starts from wherever the window shows in that frame.

The fades and moves that a `Layout` region's `transition` states
(plans 17 and 18) run on the engine in place of ivi-layout's
transitions. The control socket keeps its `place` and `hide`
requests and their `fade` and `move` tokens, so the `Layout` API and
this operator's requests do not change. Two animation paths in one
module would step at two rates, and the old path would keep its 30
steps a second.

### The protocol

A client binds `liken_arranger_manager_v1`, which the module
advertises on every socket, and calls `get_arranger`. The module
grants the claim of the socket the client arrived on when the claim
has no arranger, and refuses it with `busy` when another client holds
it, or with `not_a_claim` on a socket that belongs to no claim. Any
connection to the claim's socket can arrange, so a program can bind on
the connection its window uses. Root plan 85 records a spike that
bound a global on the connection of an iced 0.14 window through
`wayland-backend`'s foreign display.

An arrangement is a list of entries and one commit. Each entry names
the claim's windows by xdg app_id and states a rectangle in logical
pixels of the region, an opacity, a fit, and a delay. The order of the
entries is the stacking order. A window no entry names fills the
region, opaque, below the named windows. The commit states a serial,
a name, a duration up to 10 seconds, and one of four curves. The
module answers `landed` with the serial in the frame where every entry
finished, and sends `region` and `surfaces` so the arranger knows the
region's size and which app_ids its claim carries.

Windows are named by app_id because every client already sets one,
and a program that does not know the protocol, such as mpv, can be
told which to set (`--wayland-app-id`). An app_id identifies a window
only inside one claim. The socket stays the authority on which claim
a window belongs to, as plan 17 decided, so a client that copies
another program's app_id can move only its own claim's windows.

There are two fits:

- `scale` scales the window into the rectangle and never sends a
  configure, so a video client never reallocates while it plays.
- `resize` scales the window during the move, then sends one
  configure at the rectangle's size when the entry lands, so a panel
  of text redraws sharp at its new size.

**The solid backdrop.** Under `scale`, a window whose own content is
one solid color and that has subsurfaces keeps filling the region, and
the rectangle applies to its subsurfaces. mpv draws its film as a
subsurface over a black single-pixel backdrop. weston lowers a solid
view that covers the output to the CRTC background, and a scaled
backdrop leaves nothing to lower. On `liken-1` on 2026-10-27, a
prototype that scaled mpv's whole window made weston 16.0.0 write
`BACKGROUND_COLOR` 0, which i915 refused in every atomic commit, and
the picture froze while the sound played on. The rule keeps a solid
view under the whole output on every weston release.

**The fallback.** When the arranger is destroyed or its client
disconnects, every window of the claim moves back to filling the
region over 400 milliseconds, and the claim has no arranger until a
client asks again. A crashed client therefore leaves its claim's
windows where a claim with no arranger shows them. A program that
restarts binds again and commits its arrangement again. The module
keeps no arrangement after its arranger leaves, so no stale
arrangement waits for a client that is gone.

### The report lines

The control socket carries four more events, beside `output`,
`surface`, `surface-size`, and `surface-gone`:

| Line | When the module sends it |
| --- | --- |
| `surface-app-id <id> <app_id>` | A surface's xdg app_id is known or changes. |
| `arranger <socket> <pid>` | The module grants a client of that claim's socket. |
| `arranger-gone <socket> <reason>` | The arranger is destroyed (`released`) or its client disconnects (`disconnected`). |
| `arrangement <socket> <serial> <name>` | The arranger commits. |

The module replaces each character of a name that is not printable
with `_`, and sends an empty name as `-`, so each line splits on
spaces. A new connection gets every arranger and its last arrangement
again (`arrangement_send_state`), the way it gets every surface. The
lines are additions, so the protocol version stays 1. An operator
that does not read them logs each line and ignores it. This operator
reads an `arranger-gone` line with no reason as `disconnected`, so a
module older than the reason still posts the Warning for a crash.

The pid is the client's as `wl_client_get_credentials` reads it from
`SO_PEERCRED`. The kernel translates it into the pid namespace of the
compositor, which is the operator pod's namespace
(`shareProcessNamespace: true`), and answers 0 for a process that the
namespace does not contain. Every workload runs in another pod, so its
arranger reports pid 0. This is read from the kernel's
`cred_to_ucred` and `pid_vnr`, not measured.

### The status

The store keeps the module's reports and nothing else
(`arrangerstore.go`), and a new connection empties it. Each pass
resolves an arranger's socket to its claim and the claim's pods, the
way it resolves a surface's (`arrangers.go`). The `Display` shows:

- `status.surfaces[].appID`, each window's app_id.
- `status.arrangers[]`, one entry for each claim on the screen that has
  an arranger, ordered by claim: `claim`, `region`, `pods`, `pid` when
  it is not 0, `arrangement`, and `duplicateAppIDs`, the app_ids that
  two or more of the claim's windows carry, which always move
  together.

The status keeps the name of the last arrangement and not its serial.
A commit follows a key press, so a field that changed on every commit
would write the `Display` on every press. A commit that keeps the name
writes nothing.

`status.arrangers` names the pods and not the container. A map from
the pid to a container through `/proc/<pid>/cgroup` needs a pid in the
host's namespace, and the compositor has none for another pod's
client. Running the compositor with `hostPID` would give it one, and
would also let every container of the operator's pod see and signal
every process on the node. A claim that one pod holds names the
arranger's pod exactly, which is every claim `media-operator` makes.

### The Events

The pass posts an `Event` on the `Display` after its status write
lands, from the difference between the arrangers it read and the
arrangers it wrote:

| Reason | Type | When |
| --- | --- | --- |
| `ArrangerGranted` | Normal | A claim is new in `status.arrangers`. |
| `ArrangerLost` | Warning | A claim left `status.arrangers` because its arranger's client disconnected while a pod that holds the claim still runs and is not being deleted. |
| `ArrangerReleased` | Normal | A claim left `status.arrangers` because its arranger was destroyed, or because its client disconnected after its pods were gone or being deleted. |

A disconnect under a running pod is how a crashed client looks, and a
person may need to read that client's log. A client that destroys its
arranger gave the region up on purpose, so that is Normal whether or
not its pod runs. The store keeps the reason of each claim's last
departure until a client of the claim is granted again, and a new
connection empties it. A commit posts nothing,
by the rule of root plan 78 that a key press posts no `Event`. The
first pass on a new connection to the module posts no loss: the
compositor or this operator restarted, every arranger went with the
old compositor, and `CompositorServing` or `CompositorKilled` reports
that already. A client that binds again on the new compositor posts
`ArrangerGranted` again.

### Weston 16.0.2 from source

The weston image takes weston from the 16.0.2 release tarball and
builds it with meson, in place of Debian sid's `16.0.0-1` package.
weston 16.0.0's DRM backend writes `BACKGROUND_COLOR` 0 whenever no
solid view covers the output, and i915 refuses those atomic commits.
On `liken-1` on 2026-10-27, the released compositor refused 12 to 25
commits at the start of every film, and the picture froze for that
time. weston 16.0.1 changed the default to opaque black (`ed403635`).
With 16.0.2 built from source, a film started with no refused commit.
The image pins the release tarball, and `weston/latest.sh` reports the
newest release, by the rule that every pin stays current.

## What was considered and set aside

- **A container name from the pid.** Set aside for the reason in "The
  status": the compositor reads pid 0 for every workload, and
  `hostPID` widens what the operator's pod can see.
- **An `Event` for each arrangement.** Set aside because commits follow
  key presses.
- **Arrangers in `status.layout.regions`.** A region shows one claim,
  but the default layout's one region shows every claim, so an
  arranger for each region cannot show two claims with arrangers under
  the default. `status.arrangers` is keyed by claim.
- **A `Layout` field for the step-back.** Root plan 85 sets it aside:
  it is too slow for a press and makes each `Layout` carry a media
  feature.

## How the work is proved

1. `arrangerstore_test.go` feeds the store each report line and each
   malformed one. `arrangers_test.go` runs placement passes against the
   fake module and the fake API server, and checks `status.arrangers`,
   the app_ids, the write that a commit of the same name does not
   make, and each `Event` with its type and message.
2. The weston image builds, and the layout smoke check
   (`weston/layout/smoke.sh`, with `arrange_smoke.py`) binds the
   protocol against the built module.
3. On `liken-1`, a Player plays a film, a test client on the film's
   claim commits a step-back and a return, and `status.arrangers`
   shows the arrangement's name. The client is killed, the film returns
   to fill its region, and the `Display` carries `ArrangerLost`.
4. On `liken-1`, a `Layout` region with a fade enters and leaves at the
   panel's refresh rate on the engine.

On `liken-1` on 2026-10-27, drill 3 ran on `stick-1` with weston
16.0.2 and the built module. A test client on the claim of an
episode's `Play` was granted, heard `region 1920 1080` and one
`liken-media-video` surface, and committed `stepped-back` over 900
ms, which landed 918 ms later. `status.arrangers` named the claim,
the playback pod, the region `play`, and the arrangement
`stepped-back`, and the `Display` carried `ArrangerGranted`. After a
`kill -9` of the client, the module logged the arranger gone as
`disconnected`, the film returned to fill its region,
`status.arrangers` emptied, and the `Display` carried an
`ArrangerLost` Warning that named the running pod. weston refused no
commit during the drill.

## What is not decided

- Whether the film changes between a scanout plane and the GPU
  composite during a step-back, and whether that shows (root plan 85).
