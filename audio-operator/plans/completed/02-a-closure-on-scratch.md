# 02, A closure on scratch

Built, and drilled on liken-1 on 2026-08-17.

This plan answers the open problem "The image is still Debian", and
this document replaces it.

## The problem

The image is `debian:stable-slim` with `pipewire`, `pipewire-bin`, and
`wireplumber` installed from the distribution. The two sibling
operators build their images from a named set of files instead: the
bluetooth operator ships two `FROM scratch` images totaling about
20 MB, and the display operator ships a 252 MB library closure that
was 608 MB as a distribution install. This operator ships a whole
Debian userland to run two daemons and one static binary.

A static build is not the answer here. PipeWire and WirePlumber both
open their modules with `dlopen`, so a static daemon binary carries
neither daemon's function. The work is a closure, the display
operator's shape: name every file the daemons really use, resolve
each one's libraries, and copy that set onto `scratch`.

## The measurement

Nobody had written the file list, so it was measured instead of
derived. On 2026-08-17 a drill pod on liken-1 read
`/proc/<pid>/maps` for the running `pipewire` and `wireplumber`
processes. The kernel publishes there every file a process has
mapped, which is the loader's own answer to what the daemons need.

The whole running system is about fifty shared objects: eleven
PipeWire modules, nine WirePlumber modules, five SPA plugins,
`libasound`, Lua, the glib stack, and the ordinary libc family. The
operator's own binary is static and maps nothing else.

Three findings from the measurement shape the script:

* `libsystemd`, `libudev`, `libselinux`, and `libdbus` are mapped
  even though nothing here uses them, because Debian's builds link
  them. The closure carries them; dropping them would mean building
  PipeWire ourselves, and they are small.
* The maps miss files that are read and closed at startup: the
  WirePlumber scripts under `/usr/share/wireplumber`, the stock
  configuration under `/usr/share/pipewire`, alsa-lib's tree under
  `/usr/share/alsa`, and glibc's gconv table. The script names these
  data trees whole.
* `libspa-alsa.so` loads only on a machine with a sound card, so no
  check that runs without hardware sees it. The script names it
  explicitly, and the release gate asserts it by name.

## The design

`audio-closure.sh`, sibling to the display operator's
`weston-closure.sh`. A Debian build stage installs the three
packages, the script copies the named set into `/out`, and the final
image is `FROM scratch` with that set and the operator binary.

The script names four binaries: `pipewire`, `wireplumber`, `pw-dump`,
and `wpctl`. `pw-dump` is how the operator reads the graph and the
only debugging window a shell-less image offers, the way the display
operator keeps `wayland-info`. `wpctl` is the readiness probe for
WirePlumber. Modules are named individually, from the measurement,
because the distribution's module directories hold modules this
operator never loads, and a blanket copy would drag their
dependencies in.

## The release gate checks itself

The daemons run without a sound card; that is the operator's idle
mode on a cardless node, and the release check uses it. The workflow
starts the built image's `pipewire` and `wireplumber` on the runner,
confirms `pw-dump` connects, reads each daemon's `/proc/<pid>/maps`
from the runner side, and fails the release if any mapped file is
missing from the image.

That gate closes the failure class the display operator's open
problem "loads that `ldd` cannot see" names: a load added by a new
daemon version passes silently today and fails only on hardware.
Here it fails the release. The gate cannot see `libspa-alsa.so`, the
one hardware-only load, so it asserts that file and the four binaries
by name.

## What was considered and set aside

* **Static daemons.** The function of both daemons lives in modules
  they `dlopen`, so a static binary is the daemon without its
  function.
* **Blanket module-directory copies.** Simpler to write, but the
  directories hold modules this graph never loads, and the
  dependency walk of the unused ones is where the size would come
  back.
* **Building PipeWire without the unused link deps.** The four
  baggage libraries cost a few megabytes; owning a PipeWire build
  costs every future upgrade.

## What the drill showed

The drill ran on liken-1 on 2026-08-17, against the release built
from this closure. The image is 36.4 MB against the Debian image's
163.4 MB, a 4.5x reduction. The release gate passed on the release
that shipped it, and a negative control during development proved the
gate: an image built with `/usr/share/wireplumber` deleted failed it.

The new image serves the same graph the Debian image serves. The
slice came back identical in shape, `kubectl exec` runs `pw-dump`,
and the movie's audio plays through the claimed sink. Not one file
was missing from the closure: the one defect the drill surfaced was
a permissions policy, recorded in plan 03, not an absent load.
