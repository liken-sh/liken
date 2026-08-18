# The library closure includes loads that `ldd` cannot report

Open problem. `weston-closure.sh` builds the compositor image by
resolving weston's modules and their loader graphs into a rootfs. `ldd`
reports the `DT_NEEDED` graph and nothing else, so every library that a
program opens by file name at runtime was added by hand. A mesa or
weston release that adds one more such load breaks the image, and
nothing in the build reports it except starting the compositor.

## What was added by hand

Four kinds of load, none of them in any `DT_NEEDED` entry:

* **The modules weston opens by name.** `drm-backend.so`,
  `headless-backend.so`, `gl-renderer.so`, and `kiosk-shell.so`.
  `weston.ini` names them and weston opens the file.
* **glvnd's EGL vendor library.** `libEGL.so.1` is the dispatch. It
  reads `/usr/share/glvnd/egl_vendor.d/50_mesa.json` and opens the
  library that the JSON names, which for mesa is `libEGL_mesa.so.0`.
* **mesa's gbm backend.** `libgbm.so.1` opens a backend from the `gbm`
  directory. `dri_gbm.so` is mesa's.
* **The DRI drivers.** `libEGL_mesa.so.0` opens the driver for the
  card, under the name the kernel driver reports: `iris_dri.so` on
  Intel, `radeonsi_dri.so` on AMD.

Four data paths were added the same way, because nothing in the library
graph points at a data file either:

* the glvnd vendor JSON, which glvnd needs before it loads any vendor,
* `drirc`, which mesa reads for its per-application workarounds,
* `/usr/share/X11/xkb`, because xkbcommon compiles a keymap for the
  seat at every start, with `require-input=false` and no keyboard on
  the machine,
* libinput's quirks database, which libinput reads whether or not a
  device is there to apply a quirk to.

## What the release check covers

The release starts the compositor headless on an ordinary runner,
reads the log, and requires two lines in it: `Using GL renderer` and
`kiosk-shell.so`. Neither image pushes to ghcr.io until that passes.
`go test` covers none of this, because the failure is a file that is
not in the image.

That check covers the module loads, the EGL vendor, the shell, and one
DRI driver, which is `swrast_dri.so` on a runner with no graphics card.

## What it does not cover

Anything that loads only on real hardware. The largest case is the DRI
driver itself: the headless run resolves the software driver, and the
driver for a card the build machine does not have is never opened. A
missing `iris_dri.so` passes every check the build runs and fails on
the one machine that has an Intel card in it.

## The pin is deliberate

The Debian suite is pinned in the `Dockerfile`. `weston-closure.sh`
names weston 14 in the path of every module it copies, so a suite that
ships weston 15 fails the build. That is the intended report: the
module set needs reading again against the new release. The cost is
that a distribution upgrade is a manual step, and it is accepted.

## The shape of an answer

Nothing is decided. Two directions are open, and they are not
exclusive:

* Check the closure against the packages rather than against a run.
  The builder has the full Debian install beside the closure it wrote,
  so a check could compare the two. It would report a file that the
  packages contain, that the closure omits, and that some file in the
  closure names as a string.
* Start the compositor on a machine with a card. That covers the DRI
  driver and the DRM backend, and it needs hardware the release
  workflow does not have.
