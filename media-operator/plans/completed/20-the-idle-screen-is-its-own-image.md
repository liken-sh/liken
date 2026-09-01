# 20, The idle screen is its own image

Plan 09 built the idle screen and plan 12 gave it the bus. Both built it
inside `mpv`: a player with no file, a Lua overlay under `display/`, and
a sidecar that drives that overlay through `mpv`'s IPC socket. This plan
makes the idle screen a program of its own, `media-operator-idle`, a
Rust client on the Iced toolkit that reads the bus and draws the same
screen. The seven Lua modules that drew it are deleted, and the playback
overlay stays exactly where it is.

`library-operator` asked for this, and its [plan
05](https://github.com/liken-sh/library-operator/blob/main/plans/05-the-idle-screen-in-iced.md)
states why. Nothing here knows about libraries. That repository runs
this image on its screens and adds a browser beside it.

## The problem

A video player holds a Wayland surface open to draw a clock. It decodes
nothing while it does, every fact it draws arrives as a script message
on a socket protocol `mpv` defines, and the client cannot rebuild its
own surface when a film ends, so the sidecar drives `force-window`
through that socket to make it happen.

The Lua also draws a screen the project keeps extending. Fourteen
hexagons pulsing on two sines each, four eased animations, and a
per-frame timer are what ASS drawing can just barely express, and every
one of them is redrawn as a string on every frame.

## The image

`media-operator-idle` is the third image this repository ships, beside
the operator and the player. It carries one Rust binary, the Wayland and
Vulkan client libraries, Noto Sans, and `tzdata`. Its crate is at
`idle/`, and the build context is the repository root, the way the other
two images take it, so the build reaches the `brand` submodule the look
comes from.

The operator names that image on every idle container, from
`IDLE_IMAGE`, the way it names the player image from `PLAYER_IMAGE`. A
`Player` overrides it with `spec.idle.image`, which resolves through
`IdlePolicy` beside `fadeAfterSeconds` and `offAfterSeconds`, so a
household states one client in `MediaPreferences` and one screen
overrides it. An override runs that image with its own entrypoint and
keeps every claim, mount, and variable the idle container carries.

## The client reads the bus

Every fact the screen draws is a bus fact or a sidecar decision, so the
client subscribes and the socket goes away. Three topics reach it.

* The `Player`'s status topic, retained, which exists today. It carries
  `displayName`, `activity`, `components`, and the current `play`, and
  it drives the identity block, the energy, and the activity line.
* The `Player`'s volume topic, retained, which exists today. The
  broker's catch-up sets the level and shows no indicator, and every
  live message is a press. The broker marks a catch-up delivery with the
  retain flag, so the client tells the two apart and holds no state.
* `<base>/players/<namespace>/<name>/screen`, new, which the sidecar
  publishes. It carries what the sidecar decides.

| `event` | What it says | What the client does |
|---|---|---|
| `sleep` | The quiet window ran out. | Ease the shade down over 4000 ms. |
| `wake` | A press or a starting `Play` came. | Ease the shade up over 400 ms. |
| `focus` | A live mark named this `Player`. `remote` holds the controller's index in `spec.remotes` order. | Beat that part's marker white once. |
| `present` | A `Play` ended and the screen is the client's again. | Build a new Wayland surface, and start the arrival motion on the frame it is up. |

The shade events are state, so the sidecar publishes them retained, and
a client that restarts reads the cover it should draw. The sidecar also
restamps the shade it holds on every bus session, so a pod that rolls
while the screen is dark comes back lit. The focus and present events
are moments and travel unretained, because a replayed moment is a press
that already happened, or a surface nothing asked for. The retained
status is the fallback for a lost `present`: the client also maps a
fresh surface on the status's own move to `Idle`.

`present` is one event where the socket had three steps. The reveal
problem is the compositor's and it does not go away: Weston's
kiosk-shell reveals a lower surface only along a code path gated on a
seat, and `liken`'s compositor has none. A client that maps its own
fresh surface is revealed along the seat-independent path, and it knows
the frame the new surface is up, so it needs the request and not a
report that `mpv`'s surface came back.

## The sidecar keeps its job and loses its socket

The idle command sidecar holds the quiet window, the off window, the
panel desire, the keymaps, the focus gate, and the volume presses. None
of that changes. What leaves is every line that reaches `mpv`: the
dialog, the script messages, the property writes, the `force-window`
pair, and the 200 ms teardown gap between them. The sidecar publishes,
and the client draws.

## What the client draws

The seven elements of `display/main.lua`'s idle branch, in its order:
the mark at centre, the clock at the top right, the activity line under
it, the identity block at the bottom left, the volume row, the preview
legend where the keys are bound, and the shade over everything.

The whole screen draws inside one `canvas`, in the order `main.lua` draws
its elements in. That order holds between shapes and it does not hold
between a shape and text: `iced_wgpu` renders a layer in four passes,
quads then meshes then images then text, and a canvas frame is one
layer. So the shade is not a black rectangle over the screen. Every
element scales its own colours by the cover instead, which over a black
ground composites to the same pixels, and which is how `theme.lua` fades
the whole overlay today: one factor that every element applies.

Every number below is `display/`'s, and every one is a test.

| Motion | The numbers |
|---|---|
| The mark | Fourteen hexagons, each changing size about its own centre. Two sines a hexagon, the mean of the two. The first rate runs from 0.22 to 0.40 Hz across the mosaic, the second is the first times the golden ratio, and both phases come from the hexagon's index. The swing is ten percent at full energy. |
| The energy | Smoothstep to 1 over 1200 ms while a `Play` starts, and to 0 over 2500 ms otherwise. `Playing` steps to 0 with no ease, because a film covers the surface. The phase advances at 0.3 of its rate at rest and at the full rate at energy 1, and it never resets. |
| The arrival | A `present` puts the energy at 1 and eases it to 0 over 2500 ms, unless a `Play` starts or runs. |
| The shade | Smoothstep, 4000 ms down and 400 ms up. |
| The identity block | 400 ms between dim and full. A part that reconnects flashes white, 120 ms up and 500 ms down. The focus marker beats on the same two rates. |
| The volume row | 350 ms in, 600 ms out, and the row leaves four seconds after the last press. |
| The activity line | Opaque while a `Play` starts or runs. After that its alpha follows the energy, so the line leaves with the mark's motion. |
| The clock | One redraw a second, so the minute turns. |

Every animation is a function of the wall clock and the moments before
it, and never a counter advanced per frame. That is what makes a
captured frame reproducible and a dropped frame harmless. The energy's
phase is the one value that integrates rather than reads, and it is
integrated in closed form rather than accumulated.

The canvas is 1080 rows tall and takes its width from the surface's own
ratio. The margins, the type scale, the line pitch, and the face are
`display/theme.lua`'s.

## The look comes from `brand`

`brand` carries a Rust crate at `iced/` that parses `liken.svg` for the
fourteen hexagons and `liken.css` for the colour tokens. The client
takes it as a path dependency through the submodule this repository
already holds at `docs/themes/brand`, so one checkout serves the
documentation theme and the client, and one pin moves both.

The playback overlay keeps its palette in `display/theme.lua`, because
`mpv` draws that overlay over its own frames and Lua reads no Rust
crate. Those values stay in step with the brand by hand, and
`theme.lua`'s palette comment names the token each colour is, so a
person changing a brand colour finds the second place it lives.

## The window, the seeds, and the preview

The window requests no decorations, or the toolkit draws a title bar and
the surface is 35 rows short of 1080. It takes its app-id from
`DISPLAY_APP_ID`, which the display claim delivers and which routes the
surface to the right screen. It reads the zone from `TZ` against the
image's own `tzdata`.

`IDLE_WINDOW_GRACE_SECONDS` arms the watchdog, and a client with no
window after the grace exits 7, so the
kubelet restarts the container until the compositor answers again. The
code stays 7, so a person reading a container's last state reads the
same number for the same reason.

`IDLE_PLAYER_NAME` and `IDLE_PLAYER_COMPONENTS` seed the identity block
before the broker answers, and the first status replaces it.
`IDLE_PREVIEW=1` binds the preview keys, and each
one builds the message the bus carries and folds it through the same
code the bus path uses. `local/idle` runs the client from source with
those keys, takes the unit's name and its parts as arguments, and passes
the rest to the client, so an edit to an element shows on the next run
with no cluster and no release.

## What leaves

Seven modules under `display/` draw the idle screen and nothing else:
`logo.lua`, `energy.lua`, `activity.lua`, `identity.lua`, `shade.lua`,
`preview.lua`, and `window.lua`. They are deleted, with the idle branch
of `main.lua`, its five script-message handlers, and its one-second
clock timer. `clock.lua`, `volume.lua`, and `theme.lua` stay, because
the playback overlay draws them during a `Play`.

`runIdle`, `idleArgv`, and the `idle` mode leave the binary. The idle
container runs an image, not this operator's binary.

The deletion is the last commit of this plan. The Lua screen is what the
port is measured against, so it stays until the two screens have been
read side by side and the client has run on `liken-1`.

## What was considered and set aside

A client that serves `mpv`'s IPC dialect on the shared socket. The
sidecar would need no change at all, and the scripting surface would be
identical. It ties every future client to a protocol another project
defines and versions.

A fully retained screen topic. The moments must not replay, so only
the shade, which is state, travels retained.

The idle screen shipped from `library-operator`, drawn by the media
browser at rest. A television with no library still shows a clock.

## How the work is proved

On `liken-1`: the idle pod rolls onto the new image and the screen draws
the mark, the clock in the `Player`'s zone, and the unit's parts. A
`Play` starts and the mark ramps into motion under the activity line.
The film covers the screen and the motion stops. The film ends, the
surface comes back in motion, and the mark eases to rest. A quiet window
runs out and the screen fades; a press brings it back. A volume press
shows the row. A controller disconnects and its line dims, reconnects
and its line flashes, and a focus mark beats its marker.

The surface rebuild is the one part `cage` cannot prove. Weston's
kiosk-shell is the reason `present` exists, so the reveal is read on the
television, and the fallback if the client's order fails there is
`mpv`'s: destroy, wait, and build.

Before the cluster, on a workstation: `local/idle` against the Lua
screen at the same seconds, read side by side, with the resident memory
of the new client written down beside the head-to-head's 87 MB.
