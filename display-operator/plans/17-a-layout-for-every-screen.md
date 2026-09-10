# A Layout for every screen

Plan 17. The compositor moves from kiosk-shell to ivi-shell, and the
operator gains a controller module of its own that places every
surface. A cluster-scoped `Layout` states regions of a screen as
fractional rectangles with label selectors, a `Display` names the
`Layout` it shows, and the operator binds the surfaces on that screen
to the regions whose selectors match the pods that drew them. Every
claim gets its own Wayland socket, so the compositor knows which
claim a surface came from and no client passes an app-id. A `Display`
that names no `Layout` shows every surface fullscreen with the newest
on top, which is what kiosk-shell does today.

## The problem

Every pod that claims a screen gets the same socket and the same
treatment: kiosk-shell makes its window fullscreen, and the last
window to map covers the rest. That is one program per screen, and
the pattern the operator was built for.

Three things on the lab cluster already want more than one program
on a screen at once, and each has a workaround that costs something:

- The idle screen sits under a film. When the film's surface goes,
  kiosk-shell reveals the lower surface only on a code path gated on
  a seat, and a `liken` machine has none, so the media layer
  publishes a re-present and the idle client maps a fresh surface
  (media-operator `operate.go`, the re-present edge). The compositor
  holds the surface the whole time; the shell will not show it.
- A camera on a television is a cast through a third program today.
  There is no way to show a stream beside a film, or in a corner of
  it, because the compositor knows only fullscreen.
- A lobby wall, a classroom screen, or a kitchen dashboard is several
  programs from several owners on one screen. Under kiosk-shell that
  is one program that draws every panel itself, so every panel is one
  team's work and one image.

Two facts about the compositor make the change small. `liken`
already delivers the compositor's socket to every pod that claims a
screen through CDI (`cdi.go`), so pods from any namespace already
draw on one compositor. And weston ships a second shell, ivi-shell,
whose whole job is a controller placing many surfaces at stated
rectangles and stacking orders on each output.

The app-id routing that kiosk-shell needs is a cost of its own. A
consumer's container must pass `DISPLAY_APP_ID` back to its toolkit
through a flag every toolkit spells differently, and a claim that
allocates two outputs into one container delivers two app-ids of
which only the last survives (`cdi.go`, `outputEdits`). The app-id
is also a string the client chooses, so it is not an identity the
operator can trust.

## The design

### The shell and the module

weston.ini states `shell=ivi-shell.so`, and `modules=` names the
operator's own controller, `liken-layout.so`, built from
`layout/liken-layout.c` in the same stage that builds the hotplug
shim and copied into the compositor image beside `kiosk-shell.so`.
The closure seeds gain `ivi-shell.so` and lose nothing: the
`[output]` sections keep their names, modes and scales, and drop the
`app-ids=` line, because ivi-shell reads no such key.

weston 14 has no `ivi-module=` key. A controller is an ordinary
module in the `[core]` `modules=` list, loaded after the shell, and
it finds the shell through `ivi_layout_get_api`. The prototype on
vega (2026-09-10, below) proved the load order.

The module is an executor. It holds no layout of its own and makes
no decision. It does five things:

- On every output, it creates one ivi layer the size of the output
  and adds it to the output's screen. Every surface on that output
  goes in that layer, and the layer's render order is the stacking
  order.
- It listens on a control socket in the pod's own config volume,
  `/etc/weston/layout.sock`. That volume is an `emptyDir` that only
  the three containers of this pod mount, where the Wayland socket
  directory is a `hostPath` that every consumer mounts. A consumer
  that could reach the control socket could place surfaces, so the
  control socket is not in the directory consumers can reach.
- It opens and closes Wayland sockets on request, one per claim, in
  the socket directory, and remembers which output each one was
  opened for.
- It reports every surface it sees: which socket it arrived on, its
  buffer size, and when it goes.
- It places surfaces where it is told: a destination rectangle, a
  position in the render order, a visibility, and a transition, then
  one `commit_changes` for the whole batch.

Placement sets the destination rectangle, and ivi-layout sends the
client an xdg configure with the destination's size on the commit
that changes it (`ivi-layout.c`, `ivi_layout_surface_set_size`). The
client redraws at that size: a layout client reflows to the region,
a video player letterboxes inside it, and only a client that ignores
configure is scaled to fit. The module sets the source rectangle to
the buffer's own size on every configure the client commits, because
ivi-layout starts a surface with a zero source rectangle and draws
nothing until one is set. The source follows the buffer and the
destination asks for the buffer's next size, so a client that obeys
configure is drawn one to one after its next frame.

Transitions are ivi-layout's own. A fade sets the visibility
transition with a duration before the commit that shows the surface,
and a move sets the destination-only transition before the commit
that changes the rectangle, and ivi-layout animates the frames in
between. The prototype ran both.

The protocol on the control socket is lines of text, one message per
line, the same as every other one-reader-one-writer channel in this
operator. The first line each way names a protocol version, and the
operator treats a version it does not know as a compositor that is
not serving, which taints the outputs the way a dead compositor does
today (plan 04). The operator is the one writer, the module holds no
truth of its own, and on every new connection the operator replays
every socket and every placement, so a restart on either side
converges.

Rectangles on the wire are in the output's logical pixels, and the
module adds the output's position in the global space. The module
reports every output's logical size and scale when it appears, so
the operator computes pixels from the size the compositor lays out
in and never from the kernel mode. On a 4K panel at `scale=2` (plan
15) that is 1920 by 1080.

### One socket per claim

`prepareClaim` asks the module to open a socket named
`wayland-<claim UID>` for the output the claim allocated, and the
CDI spec's `WAYLAND_DISPLAY` names that socket. A surface that
arrives on it belongs to that claim, and the claim's
`status.reservedFor` names the pods that hold it. The module finds
the socket a client arrived on with `getsockname` on the accepted
connection, which on a Unix socket returns the listener's path.
`unprepareClaim` asks the module to close it, which unlinks the path
and stops accepting.

libwayland has no call that removes a listening socket, so the
listener's descriptor stays open until the compositor restarts. A
compositor restarts on every mode change and every card flap, and
the count of claims between restarts is small, so the leak is
bounded in practice and recorded below as an open problem.

The identity is the socket the claim delivered, and nothing in the
pod can change it. That is why the app-id is not the identity: it is
a string the client sets, and any pod could set another pod's.

`wayland-0` keeps listening. A pod prepared before this change holds
`WAYLAND_DISPLAY=wayland-0` in its environment and reconnects to it
after the compositor restarts that the upgrade causes. A surface on
`wayland-0` belongs to no claim: the default layout shows it, no
selector matches it, and `status.surfaces` reports it with an empty
claim. `DISPLAY_APP_ID` is still delivered, unchanged, so a consumer
that passes it breaks nothing; the module ignores app-ids. Both
retire in a later plan, after every consumer image has been rebuilt
against a release that carries this one.

### The `Layout`

```yaml
apiVersion: display.liken.sh/v1alpha1
kind: Layout
metadata:
  name: front-desk
spec:
  regions:
    - name: notices
      rect: {left: 0, top: 0, width: 0.7, height: 1}
      selector:
        matchLabels: {panel: notices}
    - name: lot
      rect: {left: 0.7, top: 0, width: 0.3, height: 0.6}
      selector:
        matchLabels: {panel: parking-lot}
      transition: {kind: fade, milliseconds: 300}
```

A `Layout` is cluster-scoped, like `Keymap`, because nothing in it
names a namespace or a panel: rectangles are fractions of the
screen, and selectors match labels. One `Layout` serves a lobby in
one building and a lobby in another.

Each region has a name, a rectangle, a selector, and an optional
transition. The rectangle's fields are `left`, `top`, `width`, and
`height`, and not `x`, `y`, `w`, `h`: YAML 1.1 reads a bare `y` as
the boolean `true`, `kubectl` and flux both convert with those rules,
and a `Layout` whose author forgot to quote it would be refused with
an error about a field named `true`. That was proved with a server
dry run on `liken-1` on 2026-09-10. The order of the list is the stacking order, last on
top, so a small region written after a large one is a picture in the
corner of it. Regions may overlap for that reason. The schema
rejects a rectangle with a corner past 1.0 or a width or height of 0,
and two regions with one name.

The selector is a `metav1.LabelSelector` and matches any label, the
way a `Service` does. The candidates it matches against are already
narrow: the pods that hold a claim on this `Display`. A namespace
that may create a `ResourceClaim` on a screen may draw on it today,
and this plan adds no new door and closes none.

A region shows one surface, the first to arrive from a matching pod.
A second matching surface stays unplaced and is reported. A region
with no matching surface is empty and is reported.

A transition names how a surface enters the region: `none` or
`fade`, over a stated number of milliseconds, using ivi-layout's own
visibility transition. The compositor owns a surface's entrance and
its moves, because the workload never knows where it is. The
compositor cannot own a workload's exit: a client that exits takes
its surface with it, and there is nothing left to fade. A workload
that wants a soft exit fades its own picture before it exits. The
same rule covers duration. A camera that should show for fifteen
seconds is a `Job` whose image streams for fifteen seconds; the
layout engine never reads a clock.

### The `Display` names its `Layout`

`Display.spec.layout` names a `Layout`. A `Display` with none named
shows the default: one region, the whole screen, every surface
matches, and the newest is on top. That is kiosk-shell's behavior
stated in the new words, so a cluster upgrades with every screen
showing what it showed before, and a `Layout` is written only where
someone wants one. A `Display` that names a `Layout` that does not
exist shows the default and reports the name in a condition.

### The decision

One function decides. It takes the surfaces on a screen, each with
the labels of the pods that hold its claim, and the `Layout`, and it
returns the placement: for each surface, a rectangle and a position
in the stacking order, or unplaced. It reads no clock, opens no
socket, and calls nothing, so its tests are tables.

When several pods share one claim, a surface's labels are the labels
every holder shares. The guide already recommends one pod per claim
with `Recreate`, and this rule makes the shared case predictable
instead of wrong.

The function's output is the seam. An external layout engine would
produce the same placement from the same inputs and hand it to the
same executor. This plan builds no such interface, and
[an external layout engine](open-problems/an-external-layout-engine.md)
records what one would need.

### What the `Display` reports

`status.surfaces` lists every surface on the screen: an id, the
claim, the pods that hold it, their shared labels, the buffer's
current size, and the region it is in or none. `status.layout`
names the `Layout` in force, or `default`, and lists each region
with the surface it shows or `empty`. A condition reports a named
`Layout` that was not found, and a condition reports a module whose
protocol version the operator does not speak.

The id is the claim's UID prefix and a counter the module assigns
for the compositor's lifetime. A compositor restart ends every
surface and every id, and the clients reconnect and are placed
again, the same as they reconnect today.

### The controller loop

The operator already runs one loop per node that wakes on card
events, compositor events, and a backstop tick (`main.go`). This
plan adds three wakes to it: a surface event from the module, a pod
event for a pod on this node, and a `Layout` or `Display` event. On
each pass the loop reads the surfaces the module reported, resolves
each claim to its holders and their labels, runs the decision for
each `Display`, sends the placements that changed, and writes the
status. RBAC gains list and watch on `pods`, `layouts`, and
`resourceclaims`.

### The consumer side

media-operator's film pod and idle pod need no change to run under
this plan. Both keep passing `DISPLAY_APP_ID`, which is ignored, and
both land in the default layout the way they land under kiosk-shell.
The re-present workaround stays until the media layer's own plan
retires it: under ivi-shell a lower surface stays visible when the
one above it goes, so the workaround fires and changes nothing.

Focus on a `Player` becomes a label in a later media-operator plan:
the film pod carries `media.liken.sh/focus: "true"` while its `Play`
runs and the idle pod carries it otherwise, and a living-room
`Layout` is one region whose selector is that label. display-operator
never learns the word `Player`.

### The manual

The claim guide's step 3 loses its second line: the container passes
no app-id. A guide for `Layout` shows the front desk above with two
namespaces, and the reference gains a `Layout` page from the CRD's
own descriptions through crdref. `plans/README.md` gains this plan,
and the two open problems below.

## What was considered and set aside

- **Routing by app-id.** The identity the module needs cannot be a
  string the client sets, and keeping the app-id keeps the flag in
  every consumer. The per-claim socket removes both.
- **Peer credentials on the socket.** `SO_PEERCRED` names the
  client's process, and the walk from a process to a pod goes
  through the cgroup tree. The compositor's pod is not in the host
  pid namespace, so the kernel reports the pid as 0 for a process in
  another pod. It would work with `hostPID`, which widens the pod's
  reach for a fact the socket name already carries.
- **A slot or role on the claim.** A claim would say `slot: main`
  and the `Layout` would place slots. That puts the layout's
  vocabulary into the operator that makes the pod, and a pod's
  author has no reason to know where on a screen it shows. The
  selector keeps the binding on the layout's side.
- **A `Placement` object between the decision and the executor.**
  The decision would write a per-`Display` object and the executor
  would read it, the way a controller writes `EndpointSlice` and
  kube-proxy reads it, and another controller could write it
  instead. Nobody has a second controller, so the object would have
  one writer and one reader in one binary. The decision function
  keeps the seam without the object.
- **A Wayland protocol for the layout.** river's layout generators
  are Wayland clients that answer layout demands over a custom
  protocol. That is a second contract to keep, with a server side
  in C and a client in every engine. The line protocol over the
  pod's own socket serves one engine, which is what exists.
- **Picture-in-picture inside mpv.** `--lavfi-complex` overlays a
  second input at load time, and mpv cannot change the graph while
  a file plays (issues 8935 and 13408 upstream). The compositor
  composes surfaces from different processes and the film's
  process never learns a second surface exists.
- **Keying regions on the Wayland app-id.** Same as the first point:
  a client-chosen string and a flag in every consumer.

## What was measured and what was read

Measured on vega, 2026-09-10, weston 14.0.2 from Debian trixie in a
container, headless and then nested in the desktop session with the
pixman renderer: a 110-line controller module loaded through
`--modules=`, placed three surfaces from three containers on one
output in three rectangles, kept the film's surface still when the
second container was removed, and placed a new container in the
freed rectangle two seconds later. An `ivi-module=` key in
`[ivi-shell]` loaded nothing on this weston, and `--modules=` loaded
the module after the shell. Plain xdg-shell clients arrived on
`add_listener_configure_desktop_surface` with no client change.
`weston-screenshooter` refuses a client the compositor did not
start unless weston runs with `--debug`.

Measured in the same prototype, nested in the desktop session: a
fade over 1000 ms on a new surface, a slide from off the right edge
over 700 ms, and a glide between two corners over 600 ms every four
seconds, with the fullscreen surface under them untouched. Three
screenshots 250 ms and 1200 ms apart caught one glide at its start,
its middle, and its end.

Read in the weston 14.0 source and not run: the configure that
ivi-layout sends when a destination size changes
(`ivi-layout.c`, `ivi_layout_surface_set_size`). The `getsockname`
behavior on an accepted Unix socket is documented and not yet run
in the module.

Not measured anywhere: whether the DRM backend keeps a fullscreen
surface on a scanout plane under ivi-shell the way it can under
kiosk-shell. The lab machines are the place to read that, with the
film pod's CPU and the package temperature before and after, and
the drill below records it.

## How the work is proved

1. `make test` passes with the decision function under table tests
   and the protocol client under a fake module.
2. A development build rolls to `liken-1`. Every `Display` shows
   what it showed, with no `Layout` written: the idle screen, then a
   film over it, then the idle screen again when the film ends.
3. A `Layout` with two regions and two pods from two namespaces
   shows both on one panel, and `status.surfaces` names both with
   their claims and labels.
4. One of the two pods is deleted. Its region reports `empty` and
   the other surface does not move.
5. The compositor is restarted through a mode change. Every socket
   returns, every client reconnects, and the placement returns
   without a controller change.
6. A region with `fade` shows the fade on a new surface on the
   DRM backend, where the prototype ran on pixman.
7. The film pod's CPU and the package temperature, read the way the
   playback drill read them, before and after, on the same film.
