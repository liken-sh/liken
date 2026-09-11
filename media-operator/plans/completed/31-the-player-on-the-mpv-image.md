# 31, The player on the mpv image

Built on 2026-09-11. The player image leaves its Ubuntu base for
`ghcr.io/liken-sh/mpv`, the display operator's closure of mpv on
scratch. This plan answers the open problem "the player image is still
a distribution", and this document replaces it.

## The problem

The operator image was one static binary on scratch. The player image
was not: Ubuntu 26.04 pinned by digest and apt snapshot, plus mpv, the
Intel media driver, Mesa, PipeWire, a fallback font, and tzdata,
because mpv opens a wide closure at run time and a GPU-less runner
cannot measure the files it opens on real hardware. The playback pod is
the least trusted process in this system, and it ran on the widest
image.

## The design

**The base.** display-operator's [plan 20](https://github.com/liken-sh/display-operator/blob/main/plans/completed/20-the-mpv-image.md)
measured mpv's closure from a traced playback run and put it on the
ffmpeg image: mpv, the PipeWire client modules and SPA plugins, and
fontconfig's configuration, on the ffmpeg, VA-API, and Vulkan trees.
This image builds `FROM` it, pinned by tag the way the idle image pins
the Vulkan base, and a workstation builds against a local base with a
build argument.

**What this image adds.** The media-operator binary, the brand's two
faces, the display script directory, and the one CJK fallback font,
copied out of a Debian stage on the same suite. Nothing else. The
zoneinfo database is in the Vulkan base already.

**What changes for the player.** mpv is Debian's 0.40 where Ubuntu's
was 0.41. Against a compositor whose output reports no refresh rate,
0.40's `dmabuf-wayland` output crashes on start; every liken panel and
every weston output reports one, so the pod's path is clear. mpv's
`gpu` video output no longer works, because the EGL and GL stack lives
in the compositor's tree and not in this chain; the player runs
`dmabuf-wayland` and never selected it. A subtitle file in a legacy
charset does not convert, because glibc's gconv modules are left out.

## How the work is proved

On a workstation with an Intel GPU the image played a generated clip
inside the container against liken's own weston, headless: the log
read `Using hardware decoding (vaapi)`, `VO: [dmabuf-wayland] 1280x720
vaapi[nv12]`, and `AO: [pipewire] 44100Hz`, the run exited zero, and
the player shim loaded the display script. libass resolved the brand
face and the fallback face through the base's fontconfig. The release
gate is unchanged: mpv prints its version and the binary is present.

| image | uncompressed | over the wire |
| --- | --- | --- |
| player on Ubuntu | 647 MB | 240 MB |
| player on mpv | 478 MB | 173 MB |

A node that already holds the ffmpeg image pulls 16 MB for the player.
The drill on `liken-1` is the coordinated release with display-operator
and library-operator.
