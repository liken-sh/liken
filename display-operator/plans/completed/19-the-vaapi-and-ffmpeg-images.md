# The VA-API and ffmpeg images

Plan 19. Built on 2026-09-11. The `Dockerfile` builds two more images
on the Vulkan base: `ghcr.io/liken-sh/vaapi`, the VA-API loader and the
Intel media driver, and `ghcr.io/liken-sh/ffmpeg`, that plus ffmpeg and
ffprobe and every library they load, on scratch. A program that decodes
video on the node's GPU builds `FROM` one of them.

## The problem

The library operator tiles every video into scrub-bar thumbnails with
ffmpeg. Its image is one static binary on scratch plus a static ffmpeg,
and a static ffmpeg cannot load a VA-API driver, so it decodes in
software at half a core and loses the race for every new title to a
media server that decodes on the GPU. The media operator's player has
the opposite problem: it decodes on the GPU through a whole Ubuntu
install, the one distribution image left in the organization, because
mpv's runtime closure was never named.

Both need the same thing, which this repository already knows how to
make: a closure on scratch, the way [plan 16](16-the-vulkan-base-image.md)
made the Vulkan base.

## The design

**`vaapi`, FROM `vulkan`.** The seeds are `libva.so.2`, `libva-drm.so.2`,
and the Intel media driver `iHD_drv_video.so`. libva opens the driver
by file name from its compiled-in directory, under the name the kernel
driver reports, so the driver is in no program's `DT_NEEDED` graph and
is a seed on its own. No data file. The tree is computed whole and
then less every file the vulkan tree holds.

**`ffmpeg`, FROM `vaapi`.** The seeds are Debian trixie's `ffmpeg` and
`ffprobe`, 7.1.5, and the tree is less both trees under it. Debian's
ffmpeg links 212 libraries, so the layer is what it is. The entrypoint
is ffmpeg, so a release can run it.

**AMD is left out.** Mesa's VA driver for AMD is a link into
libgallium on Debian, and the vulkan tree does not hold libgallium
(the weston tree does). The seed would add 40 MB to the layer for a
card no liken machine decodes on. A machine with AMD graphics decodes
in software until that changes. The vulkan base keeps its AMD Vulkan
driver as before.

**The release check.** The runner has no GPU. The check runs ffmpeg on
a generated clip in software, runs ffprobe for its version, and reads
`vaapi` out of `-hwaccels`. That proves the closure loads and the
acceleration is compiled in. The driver load is proved on hardware,
the same gap the [ldd open problem](../open-problems/loads-that-ldd-cannot-see.md)
names for the compositor.

**What the two consumers do with it.** library-operator's plan 58
builds its file-facts image `FROM ffmpeg` and drops the static ffmpeg
from its operator image. media-operator's player closure is a later
plan against its open problem, and it starts from this image, because
mpv links the same libav libraries.

## How the work is proved

The weston tree is unchanged by the new packages, checked layer digest
by layer digest and by a file list diff. The three release checks pass
on a workstation. On a workstation with an Intel GPU, the image
encoded a generated clip through `h264_vaapi` and logged the iHD
driver, 25.2.3, so the closure loads on hardware.

| layer | uncompressed |
| --- | --- |
| vulkan | 208 MB |
| vaapi | 19 MB |
| ffmpeg | 218 MB |

The ffmpeg layer compresses to about 88 MB. The library operator's
static ffmpeg was 133 MB in a layer of its own, so a node that runs the
file facts and the browser pulls less than it did.
