# The mpv image

Plan 20. Built on 2026-09-11. The `Dockerfile` builds a sixth image,
`ghcr.io/liken-sh/mpv`: mpv and every library and data file it opens by
name, on the ffmpeg image. The media operator's player builds `FROM`
it, and its open problem "the player image is still a distribution"
closes with media-operator's plan 31.

## The problem

The player image was Ubuntu plus apt, the one distribution image left
in the organization, because mpv opens its drivers and plugins by name
and a runner with no GPU cannot measure the list. [Plan 19](19-the-vaapi-and-ffmpeg-images.md)
put ffmpeg's closure on scratch, and mpv links the same libav
libraries, libplacebo, libass, and libva, so most of mpv's closure was
already an image.

## The design

**The seeds.** mpv, and every file mpv's own process opened by name in
a traced playback run against a real compositor and a real PipeWire
server: the seven PipeWire client modules `client.conf` names, the SPA
plugins its `context.spa-libs` maps, `libspa-support` with the loop and
the logger, `libspa-dbus`, and `libpulsecommon`, which libpulse opens
at process start whether or not the pulse output is selected. Three
SPA plugins the configuration names and no traced run loaded, the
audio mixer, control, and video convert, are seeds too, at tens of
kilobytes each, so a path the trace missed does not fail at the first
play.

**The data.** `client.conf`, without which libpipewire refuses to build
a client context, and fontconfig's configuration under `/etc/fonts` and
`/usr/share/fontconfig`, which libass reads to resolve the OSD font
family. `/etc/fonts` copies whole, so it carries the configuration of
one font family the builder installed and the image does not; that is
harmless. mpv reads no file under `/etc/mpv` in a traced run, and libva
reads no data file on trixie.

**What is left out.** glibc's gconv modules, 8 MB, so a subtitle file
in a legacy charset does not convert; the player plays embedded tracks
and UTF-8 files. The EGL and GL stack, which lives in the weston tree
and not this chain, so mpv's `gpu` video output no longer works on this
image; the player runs `dmabuf-wayland`, which draws through the
compositor and needs no GL of its own.

**mpv 0.40, not 0.41.** Debian trixie ships 0.40 where Ubuntu shipped
0.41. Against a compositor whose output reports no refresh rate, such
as cage's headless output, 0.40's `dmabuf-wayland` output crashes on
start, and 0.41 did not. Every liken panel and every weston output
reports a rate, so the pod's path is clear, and the plain Debian
package crashes the same way, so the closure is not the cause.

## How the work is proved

The vulkan, vaapi, ffmpeg, weston, and operator trees are unchanged by
the new packages, checked layer digest by layer digest. The release
runs mpv for its version and decodes five generated frames to no
display and no audio. On a workstation with an Intel GPU the player
image played a generated clip inside the container against liken's own
weston, headless, with the log reading `Using hardware decoding
(vaapi)`, `VO: [dmabuf-wayland] 1280x720 vaapi[nv12]`, and
`AO: [pipewire] 44100Hz`, and the display script loaded through the
player shim.

| layer | uncompressed |
| --- | --- |
| mpv | 9 MB |

The player image is 478 MB uncompressed on the mpv image where the
Ubuntu image was 647 MB, and a node that already holds the ffmpeg image
pulls 16 MB compressed for the player instead of 240 MB.
