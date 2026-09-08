# The Vulkan base image

Plan 16. The `Dockerfile` builds a third image, `ghcr.io/liken-sh/vulkan`,
under the compositor image. It holds the Vulkan loader, the Intel and
AMD drivers, and the libraries a Wayland client opens by name, on
scratch. The media browser and the idle screen build `FROM` it, and the
weston image builds on it too, so a node that draws holds LLVM once.

## The problem

Two client images draw with Vulkan: the media browser in
library-operator, and the idle screen in media-operator. Each one was
Ubuntu 26.04 plus `mesa-vulkan-drivers`, and each was 388 MB unpacked
and about 120 MiB in the registry. What was in them, measured on
2026-09-08 in the published `library-operator-media-browser:2026.09.08-008`:

| Bytes | What | Needed |
| --- | --- | --- |
| 138,567,144 | `libLLVM.so.21.1` | no |
| ~87,000,000 | seven Vulkan drivers: lavapipe, nouveau, asahi, hasvk, gfxstream, virtio, radeon | no |
| ~80,000,000 | the Ubuntu base: coreutils, perl, apt, bash, dpkg | no |
| 23,538,064 | `libvulkan_intel.so` | yes |
| ~35,000,000 | the binary, glibc, xcb, libstdc++, xkb data, zoneinfo | yes |

Ubuntu ships every Vulkan driver in one package, and two of them link
LLVM: lavapipe, which draws on the CPU, and the AMD driver. The Intel
driver does not. No liken machine draws on the CPU, because every
screen is a cable into a GPU, so lavapipe and its LLVM were dead weight
in both images.

The two images also carried the same weight twice. Each installed the
same packages from the same base, but the idle screen added one font
package, so the two layers had different digests and a node pulled
both.

## The design

The closure that plan 01 built for the compositor is the pattern. The
closure stage installs the Vulkan packages beside the compositor's,
and `vulkan-closure.sh` walks the closure of five seeds:

* `libvulkan.so.1`, the loader,
* `libvulkan_intel.so` and `libvulkan_radeon.so`, the two drivers,
* `libwayland-client.so.0` and `libxkbcommon.so.0`, which the toolkit
  opens by name.

Its data list is the two ICD files the loader reads to find each
driver, mesa's `drirc`, the xkb tree, and zoneinfo. The tree lands on
scratch as the `vulkan` stage.

The weston stage now builds `FROM vulkan`. `weston-closure.sh` computes
the compositor's tree whole, then subtracts every file the vulkan tree
already holds with the same content, so the weston layer carries only
what the base lacks. Docker writes a copied file into a new layer
whether or not a lower layer has it, which is why the subtraction is
explicit. The loader's cache is the one file kept in both, because
each tree builds its own.

The order of the stages is the design. Docker shares a layer between
two images only when the whole chain under it matches, so the base has
to come first, the compositor on it, and the operator on that. A node
that runs the compositor, the operator, the browser, and the idle
screen then holds one copy of glibc, libdrm, libwayland, and the LLVM
that gallium and the AMD Vulkan driver both link.

## What it chooses

**Intel and AMD, not Intel alone.** The AMD driver is the whole reason
LLVM is in the base: on Debian's build it links `libLLVM.so.19.1` as
`DT_NEEDED`, and the Intel driver links nothing of the kind. An
Intel-only base would be about 22 MiB in the registry instead of about
65. The base keeps AMD because liken's own image keeps `amdgpu` and
`radeon` so that an ordinary machine boots with a console, and a
client image narrower than the OS that runs it is the wrong shape.
When a machine with AMD graphics arrives, nothing changes. The
[LLVM open problem](open-problems/llvm-is-two-thirds-of-the-image.md)
states the same tension for the compositor.

**Debian trixie for the clients, not Ubuntu 26.04.** The clients ran
Mesa 26.0 from Ubuntu and the compositor Mesa 25.0 from Debian. One
Mesa on both ends of the dmabuf handoff is one fewer thing to wonder
about. The Rust binaries build on the bookworm toolchain image, and
bookworm's glibc is older than trixie's, so they run on trixie's
without change.

**The display operator owns the base.** It already owns a Mesa closure
and a release path that ships two images on every tag. A Vulkan client
runtime is Mesa's other side, and the compositor sharing its layers is
a property no separate repository could give. The two client
repositories pin the base by tag in their `FROM` lines, and a bump of
that pin is the same act as a bump of any other base image.

**hasvk stays out.** It drives Intel parts from 2013 and 2014, and
every machine liken has run is Gen9 or newer.

## What was measured

Built on vega on 2026-09-08 from the closure stage at
`debian:trixie-slim`, with mesa 25.0.7-2+deb13u1, libvulkan1
1.4.309.0-1, and libllvm19 1:19.1.7-3+b1:

| Image | Unpacked | Own layer |
| --- | --- | --- |
| `vulkan` | 208 MB | 208 MB |
| `weston` | 272 MB | 64 MB |
| `display-operator` | 291 MB | 19 MB |

The compositor image was 234 MB before this plan and is 272 MB after
it. The 38 MB it gains is the two Vulkan drivers, the loader, and
zoneinfo, which is the price of the sharing. The release check, the
headless start on an ordinary runner, passed on the new weston image
with `GL renderer: llvmpipe` and `kiosk-shell.so` in the log, so the
subtraction removed nothing the compositor loads.

The two client images, and what a screen node pulls once all four run
on it, are measured in the client repositories when they move onto the
base.

## What it does not do

The base does not answer the LLVM open problem. LLVM is still in
every image, now for the AMD Vulkan driver and gallium together. What
changes is that it is in a node's store once instead of three times.
A private Mesa built without LLVM would shrink the base and the
compositor at once, and that trade is unchanged.

The base ships no software rasterizer, so nothing in it can be started
on a runner with no card. The release workflow checks the compositor,
which has llvmpipe through gallium, and it checks nothing of the base
directly. A client image that misses a library the base should carry
finds out at its first start on a machine.
