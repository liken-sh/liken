# LLVM is two thirds of the image

Open problem. The compositor image is already `FROM scratch`, and it is
still 234,346,900 bytes. `libLLVM.so.19.1` and the three libraries that
only LLVM needs are 159,444,752 of them, which is 68% of the image.
LLVM is in the closure because Debian builds mesa with llvmpipe, and
llvmpipe is the one driver in that build that no liken machine ever
runs.

[Plan 01](../completed/01-the-compositor-image.md#what-stays-and-why) calls
LLVM and gallium "the floor for a GL renderer on mesa". That is the
floor for Debian's mesa. Mesa's own build has options that Debian does
not take, and this document states what those options would drop, what
they would cost, and what nobody has measured.

## What the closure measures

These numbers come from a rebuild of `weston-closure.sh` in
`debian:trixie-slim` on 2026-08-16, with mesa 25.0.7-2+deb13u1,
libllvm19 1:19.1.7-3+b1, and weston 14.0.2-1. The rebuilt tree is
234,348,772 bytes, which is 1,872 bytes over the size plan 01 records
for the published image.

| Bytes | File | Why it is there |
| --- | --- | --- |
| 129,673,080 | `libLLVM.so.19.1` | `libgallium` names it as `DT_NEEDED` |
| 42,565,904 | `libgallium-25.0.7-2+deb13u1.so` | `libEGL_mesa.so.0` and `dri_gbm.so` name it |
| 27,751,664 | `libz3.so.4` | `libLLVM.so.19.1` names it, and nothing else does |
| 1,799,256 | `libxml2.so.2` | `libLLVM.so.19.1` names it, and nothing else does |
| 220,752 | `libedit.so.2` | `libLLVM.so.19.1` names it, and nothing else does |

`libz3.so.4` is the finding that was not in plan 01. Z3 is a theorem
prover. Nothing in mesa calls it. It is in the image because Debian
builds LLVM with `-DLLVM_ENABLE_Z3_SOLVER=ON`, which
`llvm-toolchain-19` sets for every architecture except sh4 whenever
`libz3-dev` is newer than 4.7.0. So the true cost of LLVM in this image
is not 127 MB. It is 159,444,752 bytes over four files.

LLVM and gallium together are 202,010,656 bytes, or 86.2% of the
image. Everything else is 32.3 MB: weston, libweston, the four modules,
glvnd, libdrm, libinput, xkbcommon, libwayland, glib, cairo, pango, and
3.4 MB of data files under `/usr/share`.

## Why LLVM is in the image

Debian's `debian/rules` for mesa 25.0.7-2+deb13u1 builds amd64 with
thirteen gallium drivers: `softpipe`, `nouveau`, `r300`, `r600`,
`virgl`, `crocus`, `i915`, `iris`, `svga`, `d3d12`, `radeonsi`, `zink`,
and `llvmpipe`. It passes `-Dllvm=enabled` and
`-Dplatforms="['x11','wayland']"`. Its own comment says why: "LLVM is
required for building r300g, radeonsi and llvmpipe drivers."

Mesa's `meson.build` at tag `mesa-26.2.0` states the same requirement
as build logic, and it names exactly which drivers force LLVM on:

    with_llvm = with_llvm \
      .enable_if(with_gallium_i915, error_message : 'i915 Gallium driver requires LLVM for vertex shaders') \
      .enable_if(with_gallium_llvmpipe, error_message : 'LLVMPipe Gallium driver requires LLVM') \
      .enable_if(with_gallium_r300 and r300_needs_draw and draw_with_llvm, error_message : 'R300 Gallium driver requires LLVM for vertex shaders on IGP parts') \
      .enable_if(with_gallium_radeonsi and amd_with_llvm, error_message : 'RadeonSI Gallium driver configured to require LLVM')

Four drivers force it: `llvmpipe`, `i915`, `r300` on integrated parts,
and `radeonsi` while `amd-use-llvm` stays at its default of `true`.
Lavapipe forces it too, and Debian builds the `swrast` Vulkan driver.
`iris` is not in that list, and neither is `crocus`. So a mesa built
for `iris` alone does not require LLVM.

The 42 MB of `libgallium` follows the same list.
`src/gallium/targets/dri/meson.build` builds one shared library named
`gallium-<version>` and links every enabled driver into it:
`driver_swrast, driver_r300, driver_r600, driver_radeonsi,
driver_nouveau, ... driver_iris, ...`. Debian ships that one file, and
the fourteen names in the `dri` directory are symlinks to it. So the
driver list sets the size of `libgallium`, and a shorter list makes a
smaller file. How much smaller is not measured here.

## What a narrow build would drop

Mesa 26.2.0 takes a driver list on the command line. `gallium-drivers`
is an array option whose choices include `iris`, `crocus`, `llvmpipe`,
`softpipe`, `radeonsi`, and twenty more. `vulkan-drivers` is a second
array, `platforms` is a third, `glx` is a combo with a `disabled`
choice, and `llvm` is a feature that accepts `disabled`.

A build configured as `-Dgallium-drivers=iris -Dvulkan-drivers=
-Dllvm=disabled -Dglx=disabled -Dplatforms=wayland` violates none of
the `enable_if` rules above. If it links, it drops 159 MB of LLVM,
Z3, libxml2 and libedit, and it drops whatever share of `libgallium`
the other twelve drivers hold. That is the candidate. Nobody has run
it.

The three other options are worth naming because they cut different
things. `-Dvulkan-drivers=` drops lavapipe, which is the second reason
Debian's build needs LLVM. `-Dglx=disabled` and
`-Dplatforms=wayland` drop mesa's X11 paths, which is where
`libX11-xcb.so.1` enters the closure from `libEGL_mesa.so.0`.

## What building mesa costs

The `Dockerfile` installs three packages today. A narrow mesa turns the
closure stage into a source build, and four costs follow from that.

* **CI time.** The release workflow spends its time on
  `apt-get install` and one `go build` today. A mesa build replaces the
  first of those, and neither its duration nor a cache strategy for it
  has been measured.
* **A version to carry.** Debian's suite pin already fixes weston at
  14 and mesa at 25.0.7. A source build replaces the mesa half of that
  pin with a tag this repository chooses, and with the security updates
  this repository then owes. Debian issued 25.0.7-2+deb13u1 as an
  update. Nobody here would see the next one.
* **The dependency set.** Mesa needs libdrm, wayland-protocols,
  libxkbcommon and more at their build versions, and it needs Rust for
  some options. The build stage grows a list that the three
  `apt-get install` lines do not have today.
* **Two mesas in the tree.** The closure would ship mesa from source
  and weston from Debian, and weston links against Debian's libdrm and
  libwayland. Whether mesa built from source loads correctly beside
  those is unverified.

## The release check runs on llvmpipe

The release starts the compositor headless on an ordinary runner with
no graphics card, and requires `Using GL renderer` and `kiosk-shell.so`
in the log.
Plan 01 records what that run printed: `GL renderer: llvmpipe (LLVM
19.1.7)`. llvmpipe is what makes the check possible. A mesa without
llvmpipe fails that check on the first line it reads.

Three ways out, none chosen:

* **Keep a second image that carries llvmpipe, and check that one.**
  The check then passes on bytes the fleet never runs. Plan 01 builds
  the operator image `FROM` the weston image for one stated reason:
  "the compositor that passed the check and the compositor the pod runs
  are the same set of bytes and cannot drift." This option gives that
  property up.
* **Build `softpipe` beside `iris`.** `softpipe` is a choice in
  `gallium-drivers` at 26.2.0, and it is absent from every `enable_if`
  that forces LLVM, so a build of `iris` and `softpipe` with
  `-Dllvm=disabled` keeps a software rasterizer. Mesa's `meson.build`
  treats either one as the swrast path: `with_gallium_swrast =
  with_gallium_softpipe or with_gallium_llvmpipe`. Whether weston's
  headless backend starts its GL renderer on softpipe, and how long one
  frame takes, is unverified. Softpipe is slower than llvmpipe by
  design. The check starts the compositor and reads two lines from the
  log, so how much that costs is a question of seconds, not of whether
  the check can pass.
* **Move the check to hardware.** The
  [other open problem](loads-that-ldd-cannot-see.md#what-it-does-not-cover)
  already asks for this for a different reason: a headless runner never
  opens the DRI driver for a real card, so a missing `iris_dri.so`
  passes every check the build runs. One machine with an Intel card
  answers both questions. The release workflow has no such machine.

## What a narrow driver list costs in coverage

Today the cost of breadth is one file. The `dri` directory holds
fourteen names, thirteen of them symlinks to `libdril_dri.so`, so the
image runs on any card mesa supports. A build of `iris` alone chooses
the cards the image runs on.

The fleet this operator was written for is Intel throughout. The seven
machines in `44stonypoint/cluster/machines` are an i5-8279U with Iris
Plus 655, an i5-8257U with Iris Plus 645, three N100s, and two N95s.
Five of them declare the `i915` kernel module. studio1 and utility1
declare no graphics module at all, and say why: an undriven GPU stays
out of the ResourceSlice, so no GPU claim lands there. liken's own
testbed, `liken-1`, is an N95. Every one of those parts is Gen9 or
newer. Mesa's `src/loader/pci_id_driver_map.h` sorts an Intel card into
`i915`, `crocus`, or `iris`: the first two carry explicit chip-id
lists, and `iris` takes everything its predicate accepts, which is Gen8
and newer. So `iris` alone covers this fleet, and `crocus`, `i915`,
`radeonsi`, `r300`, `r600`, `nouveau`, `svga`, `virgl`, `d3d12` and
`zink` cover nothing in it.

liken is wider than this fleet, and that is the open half of the
question. liken publishes no supported-hardware list, and its image
keeps `i915`, `xe`, `radeon` and `amdgpu` so that a console works on an
ordinary machine
([milestone 32](https://github.com/liken-sh/liken/blob/main/plans/completed/32-hardware-support-in-the-image.md)).
An `iris`-only compositor image would be narrower than the OS that runs
it: a machine with AMD integrated graphics would boot, publish a GPU,
and then start a compositor with no driver for the card. The image is
one artifact on ghcr.io for every liken user, not one artifact for this
house. What that image owes a machine it was not built for is not
decided.

## What is next after LLVM and gallium

Take LLVM, Z3, libxml2, libedit and gallium out, and 32.3 MB is left.
The next items are small, and the closure attributes each of them to a
seed. Removing one seed and recomputing the graph gives what only that
seed carries:

* **`headless-backend.so` carries 11,269,608 bytes over 28 files that
  nothing else in the closure needs.** It links cairo, pango,
  harfbuzz, freetype, fontconfig, glib, gio, libjpeg, libwebp, libpng
  and libX11. The production path never loads the headless backend.
  The release check is the only thing that does, and the check is also
  the reason plan 01 keeps one image rather than two.
* **`drm-backend.so` carries 1,848,232 bytes over 6 files**, which is
  libsystemd through libseat, libva, libva-drm, and libdisplay-info.
  That is the backend the fleet runs, and it is 0.8% of the image.
* **`wayland-info` is 55,976 bytes.** Plan 01 keeps it as the only
  diagnostic in an image with no shell. At 0.02% of the image, the
  size argument against it does not exist.

There is one more candidate that keeps llvmpipe and still cuts most of
the weight. Debian's `libLLVM.so.19.1` exports the target
initialisers for X86, AArch64, AMDGPU, NVPTX, WebAssembly, RISCV,
SystemZ, Hexagon and PowerPC, measured with `nm -D`. llvmpipe on
x86_64 uses X86. Mesa's `meson.build` asks LLVM for the `native` module
when `draw-use-llvm` is on, and it asks for `all-targets` only on
darwin, or as an optional module when it builds `clc`. A private LLVM
built with `-DLLVM_TARGETS_TO_BUILD=X86` and
`-DLLVM_ENABLE_Z3_SOLVER=OFF` would drop Z3's 27.7 MB outright and an
unmeasured share of LLVM's 129.7 MB, with no change to mesa at all.
That trades a mesa build for an LLVM build, which is the larger of the
two, so it is a worse trade for CI time and a better one for coverage.

## What nobody has measured

* The size of `libgallium` built with `-Dgallium-drivers=iris`.
* Whether that build links and runs with `-Dllvm=disabled`,
  `-Dvulkan-drivers=`, `-Dglx=disabled` and `-Dplatforms=wayland`.
* Whether weston's headless backend starts the GL renderer on
  `softpipe`, and how long it takes to draw one frame.
* Whether mesa built from source loads beside Debian's weston, libdrm
  and libwayland.
* How long a mesa build adds to the release workflow, cold and cached.
* The size of `libLLVM` built for X86 alone with Z3 off.
* What the image size costs today. A node pulls it once and both images
  share every layer, and no pull time or disk figure has been recorded
  against the 252 MB.
