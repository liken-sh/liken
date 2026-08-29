# Toolkits other than Iced

The browser had to be a Wayland client under the display operator's
`weston`, on an Intel iGPU, on a one-gigabyte box beside k3s, with
text shaped well enough for a ten-foot screen, and with motion for
the idle screen. These were weighed, and the two that reached a build
were measured head to head on 2026-08-29 against the same brief: the
idle screen with its animations, and a wall of five thousand posters.

**Gio** (Go, MIT) built both demos, held 60 frames a second at about
a millisecond of work per frame, and cost 94 MB at rest. It lost on
the poster wall: 242 MB resident at the same 96-poster cache where
Iced held 115 MB, with the difference in Go's heap and the runtime's
headroom, and the collector's memory limit bought 93 MB back at a
46 ms 99th-percentile frame. It also offers no way to read back a
frame and no corner-anchored text call. It would have kept the whole
client in Go, with Litestream's VFS in-process, and that no longer
matters with Corrosion.

**Slint** (Rust) is the toolkit built for exactly this job, under 300
KiB of runtime. It is triple-licensed, and its royalty-free license
excludes embedded devices, which leaves GPLv3 for a box on a TV. The
project's other code is MIT, and a GPL client that a fork must also
publish under GPL was set aside for a toolkit with no licensing
question at all.

**Bevy** (Rust) is the open-source game engine with Wayland and
shaped text, and it loads its default plugins at about 590 MB.
**Godot** reports 200 to 400 MB for an empty project. Both fail the
memory bar.

**Ebitengine** (Go) removed native Wayland support pending a rewrite
and has no text shaping. **raylib** has Wayland behind a build tag
and a history of unshaped Unicode. **LÖVE 12** with SDL3 and HarfBuzz
was unreleased. **Flutter**'s embedded runtimes are BSD and rich, and
heavier than the box wants, with no footprint numbers found.
**GTK4** is mature and desktop-shaped. **Gio** was the runner-up.

Iced (Rust, MIT) drew the idle screen at 87 MB and 0.63 ms a frame,
and the poster wall at 115 MB and 0.54 ms, with shaped text through
`cosmic-text`, native Wayland, and a canvas that draws like the Lua it
replaces. Its two costs are recorded in plan 05: a fixed draw order
inside a layer, and a build of 205 crates that the release workflow
caches.
