# 32, The display in its own process

The on-screen display leaves mpv's process. It becomes an iced client
in a third container of the playback pod, drawing on its own Wayland
surface above mpv's, and it looks exactly as the Lua display looks
today: the same positions, colors, alphas, type, animations, and
timings. The player, the command sidecar, the bus, and every resource
stay as they are.

## The problem

The display is a Lua script inside mpv. It draws through libass into
mpv's own OSD path, and on `dmabuf-wayland` that path composites on
the CPU, on the video thread, inside the frame. Plan 31's move to that
output made the cost visible: with the OSD up, the video thread spent
85% of a core and dropped four frames a second on the lab machine.
Commit 3bc29c7 cut that to 17% and no visible drops, but the fix was a
day of reverse engineering three layers we do not own: the script's
redraw rate, libass's change detection, and mpv's software compositor.
One of the causes, an empty overlay left by mpv's console script, was
an upstream bug nothing in our code could have shown us.

Two costs remain in that path. The fade still submits a recomposite on
every tick, so a fade-in burns most of a core for a third of a second.
And every brand decision is an ASS tag: a scrim is a blurred shape, a
card is a stack of rectangles, and text measures against a hand-built
advance table, while the idle screen and the browser draw the same
brand with iced on the same compositor.

## The design

**Three containers.** The playback pod becomes mpv, the display, and
the command sidecar. The display container runs one Rust binary on the
Vulkan base image, the base the idle screen uses, with the brand's two
faces and the CJK fallback copied in as files. It holds the same
display and render claims the player container holds, so it can open
a surface on the compositor's socket and draw with Vulkan. It mounts
the IPC volume and the art volume the sidecar already writes.

**Stacking.** The compositor's controller stacks a claim's surfaces
newest on top. The display waits until mpv's socket answers, `time-pos`
is a number, and `vo-configured` is true, then opens its window. The
last of the three is what makes the order hold: mpv reports a position
before it maps its surface, and a display that waited on the position
alone arrived first and sat under the film. So its surface arrives
after mpv's and is drawn above it. The window is 1920 by 1080 in logical
pixels, undecorated, and transparent. Between summons it draws
nothing, and a frame that draws nothing is a fully transparent frame.

**One channel.** The display is an IPC client of mpv, on the socket
the sidecar already drives. Every message the sidecar sends the Lua
display today goes through `script-message-to display`. mpv delivers a
`script-message` to every client as a `client-message` event, and an
IPC client cannot be named, so the sidecar sends the same messages as
`script-message` and nothing else about it changes. The reverse
direction is already a broadcast: `liken-art-request`, `liken-next`,
`liken-presentation-request`, and `liken-exit` reach the sidecar as
they do now. The display reads properties over the same socket, and
observes the ones the Lua observes. It opens no bus connection and
reads no input device. Presses keep their path: the Remote pod
publishes them, the focus mark gates them, and the sidecar turns them
into the six action messages. Outside control over the commands topic
reaches the display the same way it reaches the Lua.

**Art.** Through the side-by-side, the sidecar keeps serving bitmaps
as it does: a `liken-art` reply names a BGRA file under the art
volume with its width, height, and stride, and the display reads that
file into a GPU image and draws it in its own z order, so the four
`overlay-add` ids and the rule that a bitmap always draws above the
ASS layer go away. The album cover keeps its one exception: it holds
the frame whether or not the OSD is up. That indirection was mpv's
need, not ours: its overlay command takes a raw file, and a script
cannot decode. Once the port matches, the display decodes the sources
itself, the logo file or URL, the trickplay sheet, and the cover, and
the request, the reply, the files, and the sidecar's art half go. The
display container then mounts the media the way the player does and
reaches the network for an https logo.

**The look, one to one.** The inventory of the Lua display is the
specification: every element with its position formula in canvas
units, its color, alpha, font size, and anchor; every timer and
threshold; every message and its payload. The port keeps the canvas
model as it is, 1080 rows and a width that follows the surface's
ratio, and maps canvas units to output pixels with the same scale and
the same whole-pixel snap. The scrim is a vertical gradient with the
same edge alpha and the same reach the blurred shape has, judged
against captured frames of the Lua. The fade is one alpha on the whole
layer, 350 ms in and 600 ms out at 60 Hz, and the volume row and the
up-next card keep their own fade clocks and their own delays. The
module map is the Lua's: theme, focus, scrubber, strip and the four
controls, header, presentation, clock, chooser, images, album,
trickplay, up next, volume. Two things change under the same look.
Text measures with the toolkit's own shaping instead of the advance
table. And a panel dims with the fade like everything else, which the
Lua's panel helper skipped. Two more came out of the side-by-side.
A logo, a tile, and the offer's art fade with the layer instead of
popping in at full strength over text that is still rising. And a
bitmap draws in its own colors: mpv's overlay path ran every bitmap
through the film's color conversion, so on an HDR film the Lua washed
the logo and the tile out, and the port draws them as their sources
are.

Text is the one place the port is allowed to differ by a few pixels.
libass and the toolkit shape and hint the same face differently, so a
glyph's exact pixels will not match, and the review does not ask them
to. Every other measure is identical: where a box, a bar, a scrim, an
image, or a text anchor sits, how large it is, what color and alpha
it carries, and when it moves.

**Local review.** The screens in `local/` stay the way the player and
the display are reviewed on a workstation, so the port keeps them
working. `local/video` and `local/music` take the display's new
shape: the released compositor runs nested on the desktop with
ivi-shell, a stand-in controller places surfaces the way the operator
does, and mpv, the sidecar, and the display run beside it as they run
in the pod. The prototype that proved the plumbing is the seed of that
harness, and `local/osd` carries it until the two screens absorb it.

**The cost model.** A fade re-renders one transparent surface on the
GPU in the display's process. mpv's video thread does video. The
compositor composites two surfaces, and the display's surface is
empty between summons. What the design does not yet know is whether
an empty mapped surface above the video costs the video its hardware
plane; the proof below measures it, and if it does, the display
unmaps its window when it has nothing to draw.

**The switch.** The operator grows one knob, `MEDIA_DISPLAY`, with
values `lua` and `iced`, defaulting to `lua` until the port reaches
parity. It picks the pod shape: the Lua shape passes `--script` to
mpv and runs two containers; the iced shape drops `--script` and runs
three. The testbed runs `iced` while the house runs `lua`. When Chris
signs off the last screen, the default flips, and the last step of
this plan removes the Lua display and the knob.

**Nothing left behind.** The port ends with no Lua display in the
tree and no living prose that describes one. The `display/` directory
and its tests go, the player drops the script flag, the shim's
comments and the manual stop naming a script that draws, and the
local screens stop loading one. Completed plans are history and stay
as they are; every other document, comment, and help text that
describes the display describes the new one.

## The order of the work

1. The plumbing prototype: weston with ivi-shell, mpv, and an iced
   surface in separate containers on the laptop, a fade in and out on
   a press. This proves the stacking, the transparency, and the cost
   outside mpv's frame path before any port code exists.
2. The crate, the image, the third container behind the knob, and the
   IPC client: properties, events, and the client messages. Summon,
   the fade, and the scrim, with nothing on them.
3. The scrubber, the clock, the header, and presentation. This is the
   screen a viewer sees most, and it is the first side-by-side.
4. The strip, the four controls, the offsets, the chooser, and the
   focus model with its five stops and the pause rule.
5. The art bridge: logo, album cover, trickplay tile, up-next art.
6. Up next, then volume, then the music presentation.
7. A side-by-side of every state against the Lua, reviewed frame by
   frame, then the default flips.
8. The removal: the Lua display, its tests, the knob, the script flag,
   and every living sentence that describes the old display, with the
   completed plans left as they are. The local screens take the new
   shape in the same step.
9. The art decoded in the display, and the sidecar's art half removed.

What this plan does not do, and a later one should: the port drew a
brush, a fade clock, a text measure, and a canvas snap that the idle
screen and the library's browser draw in their own words. Those
belong in the brand's iced crate, and moving them touches three
repositories, so they move under a plan of their own.

Each step lands on the testbed under the knob, and each carries the
tests the Lua's four test files carry today, in Rust, plus a test per
module the Lua left untested.

## Considered and set aside

**The display inside the idle client.** The idle client is on the
screen already, holds a bus connection, and on some units is the library's
browser. Folding the OSD into it ties the player's display to another
operator's release and to every idle controller a unit might name. The
display belongs to the Play's lifetime, so it lives in the Play's pod.

**Keep libass and move to `gpu-next`.** That output blends the OSD on
the GPU and would hide the recomposite cost. It also gives up the
zero-copy plane the video has today, and runs scaling and color
conversion through shaders on an iGPU that is idle now. The OSD cost
would move, not go away, and the brand would stay in ASS tags.

**A player of our own.** Nothing measured was a playback problem.
Decode, hardware paths, zero-copy scanout, sync, subtitles, EDL
albums, chapters, seeking, and the IPC all work and are proven on both
clusters. A player in Rust reimplements all of it to reach the same
place.

**The display on the bus.** The display could subscribe to presses
itself and skip the sidecar. That makes two programs that decide what
a press means, and it loses the property that the commands topic
drives a unit from outside exactly as a controller does. The sidecar
stays the one driver of mpv and the one reader of presses.

**Patching mpv.** A seven-line guard in mpv's OSD code would have
fixed the empty-overlay bug at the source. The rule against patching
upstream stands, and this plan removes the dependence on that code
path instead.

## What proves it

On the lab machine, with a 1080p film and the same experiment that
measured plan 31's cost: the video thread's share of a core with the
OSD hidden, during a fade, and standing still; the dropped-frame
counter over a twelve-second window with the OSD up; and the display
process's own CPU in the same three states. A fade must cost the
video thread nothing it did not cost with the OSD hidden.

The compositor's scene graph, read with its debug protocol, shows
whether the video surface keeps its hardware plane while the display's
surface is mapped and empty. If it does not, the display unmaps
between summons, and the measurement runs again.

Every state of the Lua display, captured under the existing headless
harness, beside the same state of the port under the compositor, with
the same film at the same position: idle, summoned, each focus stop,
each chooser open, a scan with a trickplay tile, the up-next chip and
card, the volume row, and the music screen with its cover. The port is
done when Chris finds no difference he wants kept.
