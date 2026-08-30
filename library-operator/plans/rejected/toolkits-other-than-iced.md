# Toolkits other than Iced

The media browser has to be a Wayland client under the display
operator's `weston`, on an Intel iGPU, on a one-gigabyte box beside k3s.
It needs text shaped well enough for a ten-foot screen, and motion for
the idle screen. These were weighed. The two that reached a build were
measured head to head on 2026-08-29 against the same brief: the idle
screen with its animations, and a wall of five thousand posters.

**Gio** (Go, MIT) built both demos, drew 60 frames a second at about a
millisecond of work per frame, and used 94 MB at rest. It lost on the
poster wall: 242 MB resident at the same 96-poster cache where Iced used
115 MB, with the difference in Go's heap and the runtime's headroom. The
collector's memory limit bought 93 MB back at a 46 ms 99th-percentile
frame. Gio also has no way to read back a frame and no corner-anchored
text call.

**Slint** (Rust) is built for this job, with a runtime under 300 KiB. It
is triple-licensed, and its royalty-free license excludes embedded
devices, which leaves GPLv3 for a box on a TV. The project's other code
is MIT. A GPL client, which a fork must also publish under GPL, was set
aside for a toolkit with no licensing question.

**Bevy** (Rust) is the open-source game engine with Wayland and shaped
text, and it uses about 590 MB with its default plugins loaded.
**Godot** reports 200 to 400 MB for an empty project. Both fail the
memory bar.

**Ebitengine** (Go) removed native Wayland support pending a rewrite and
has no text shaping. **raylib** has Wayland behind a build tag and a
history of unshaped Unicode. **LÖVE 12**, with SDL3 and HarfBuzz, was
unreleased. **Flutter**'s embedded runtimes are BSD and heavier than the
box has room for, and no footprint numbers were found. **GTK4** is
mature and desktop-shaped.

Iced (Rust, MIT) drew the idle screen at 87 MB and 0.63 ms a frame, and
the poster wall at 115 MB and 0.54 ms. It has shaped text through
`cosmic-text`, native Wayland, and a canvas that draws like the Lua it
replaces. Its two costs are in plan 05: a fixed draw order inside a
layer, and a build of 205 crates that the release workflow caches.