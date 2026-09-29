# weston

`ghcr.io/liken-sh/weston` is the [`vulkan`](../vulkan/) image plus the
Weston compositor, every library it loads, two diagnostic programs,
and `liken`'s two modules for the compositor. Its entrypoint is
`weston`. The image of `display-operator` builds on it, and the
operator's pod runs this compositor.

## What the image holds

Debian puts every libweston backend in one package, and installing
`weston` pulls FreeRDP, neatvnc, GStreamer, PipeWire, libavcodec, and
flite. `weston-closure.sh` takes the modules that the operator uses:
the DRM and headless backends, the GL renderer, the ivi-shell, the EGL
and GBM loaders, and Mesa's DRI drivers. It collects them and every
library they load, and then leaves out every file that the `vulkan`
image already holds. The compositor and the Vulkan clients then share
one glibc, one libdrm, and one LLVM on a node.

The two diagnostic programs are `wayland-info`, which lists every
global that the compositor advertises, and `ddcutil`, which reads a
panel's capabilities over i2c. The image has no shell, so `kubectl
exec` runs each one by name.

The two modules are `liken`'s own code, built in this image's
`Dockerfile` against the same Debian packages as the compositor:

- `hotplug/udev-kernel-group.c` is a preload library. It moves the
  compositor's hotplug subscription from udevd's netlink group to the
  kernel's, because a `liken` machine runs no udevd. The comment in the
  file explains the detail.
- `layout/liken-layout.c` is the ivi-shell controller module. It opens
  the control socket through which `display-operator` places each
  client's window. `layout/smoke.sh` runs it against a real weston and
  real clients on a workstation with Docker.

`smoke/weston.sh` starts the image headless before it ships, and it
checks that the GL renderer, the ivi-shell, and the layout module
load.

## The version and the revision

`weston` is a pinned component. Its `package.toml` states a version and a
revision, and the image publishes under the tag `<version>-<revision>`,
for example `20260928-1`.

- The **version** is the date of the Debian snapshot that the build
  installs from, as `YYYYMMDD`. Every base on Debian uses the same
  date, so a library that two bases hold is the same file in both.
- The **revision** counts the changes to the recipe at one version. It
  starts at 1 for a new version. Any change to the build that keeps
  the version needs the next revision: a change to the `Dockerfile`, to
  a script, to the Debian image digest, or to `package.toml`.

The recipe also covers the tag of each base in `[depends]`. So a new
revision of a base under this one changes this one's recipe too, and
this one needs a new revision in the same commit. CI fails and names
each component that needs one. A published tag never changes.

## Bump the snapshot

All five bases move to a new snapshot together. The steps are in the
[`vulkan` README](../vulkan/README.md#bump-the-snapshot), and the
`bump-components` skill under `.agents/skills` holds the whole
procedure.

## Build

The bake file at the top of the repository builds this image, from
the top of the repository:

    docker buildx bake weston --load
