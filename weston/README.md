# weston

`ghcr.io/liken-sh/weston` is the [`vulkan`](../vulkan/) image plus the
Weston compositor, every library it loads, three diagnostic programs,
and `liken`'s two modules for the compositor. Its entrypoint is
`weston`. The image of `display-operator` builds on it, and the
operator's pod runs this compositor.

## What the image holds

The `Dockerfile` builds weston from its upstream release, not from
Debian's package, because a fix that `liken` needs reaches a weston
point release before it reaches sid: weston 16.0.0 froze the
screen on i915 at the start of each film, and 16.0.1 fixed it. The
build turns on only the modules that the operator uses: the DRM and
headless backends, the GL renderer, and the ivi-shell. Every library
weston links comes from the same Debian snapshot as the other bases.
`weston-closure.sh` takes those modules, the EGL and GBM loaders, and
Mesa's DRI drivers. It collects them and every
library they load, and then leaves out every file that the `vulkan`
image already holds. The compositor and the Vulkan clients then share
one glibc, one libdrm, and one LLVM on a node.

The diagnostic programs are `wayland-info`, which lists every global
that the compositor advertises, `ddcutil`, which reads a panel's
capabilities over i2c, and `liken-arrange-drill`, which arranges a
claim's surfaces through `liken_arrangement_v1` by hand. The image has no shell, so
`kubectl exec` runs each one by name.

The two modules are `liken`'s own code, built in this image's
`Dockerfile` against the same Debian packages as the compositor:

- `hotplug/udev-kernel-group.c` is a preload library. It moves the
  compositor's hotplug subscription from udevd's netlink group to the
  kernel's, because a `liken` machine runs no udevd. The comment in the
  file explains the detail.
- `layout/liken-layout.c` is the ivi-shell controller module. It opens
  the control socket through which `display-operator` places each
  client's window. `layout/motion.c` runs every fade and move it
  places a window with, and every tween of an arrangement, on the
  output's own repaints. `layout/arrangement.c` serves
  `liken_arrangement_v1`, the protocol in
  `layout/liken-arrangement-v1.xml` through which one client of a claim
  places, fades, and stacks the claim's surfaces inside its region.
  `layout/build.sh` builds the module and `liken-arrange-drill`.
  `layout/smoke.sh` runs the module against a real weston and real
  clients on a workstation with Docker, and `layout/smoke.sh arrange`
  tests the protocol with the drill.

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

## Bump weston

`latest.sh` reports the newest weston release on
gitlab.freedesktop.org, and `latest.sh --bump` writes it into the
`Dockerfile`: the version, the tarball's URL, and its sha256. The bump
checks the tarball's signature against `signing-keys.asc`, the keys
of weston's release managers as Debian's package lists them, and
checks the tarball against the release's own sha256sum file. A new
major release changes libweston's soname and the copy of
`layout/ivi-layout-export.h`, so the bump stops and a person moves it.
A bump changes the recipe, so it needs the next revision.

## Bump the snapshot

All five bases move to a new snapshot together. The steps are in the
[`vulkan` README](../vulkan/README.md#bump-the-snapshot), and the
`bump-components` skill under `.agents/skills` holds the whole
procedure.

## Build

The bake file at the top of the repository builds this image, from
the top of the repository:

    docker buildx bake weston --load

## Arrange a claim by hand

`liken-arrange-drill` connects to one claim's socket, asks to arrange
the claim, sends the arrangements that its arguments name, and prints
each event with the time since it started. The claim's surfaces tween
back to where the operator placed them when the drill exits, so
`wait` and `hold` keep an arrangement on the screen. A rectangle is in
logical pixels of the claim's region. `display-operator`'s image builds
on this one, so the drill is in the operator's pod, and every claim's
socket is in `/var/run/display.liken.sh` there:

    kubectl exec -n liken-system <display-operator pod> -- \
        liken-arrange-drill /var/run/display.liken.sh/wayland-<claim uid> \
        entry mpv 96 270 960 540 1 scale 0 commit small 900 smoothstep \
        wait hold 3000

The comment at the top of `layout/liken-arrange-drill.c` lists every
command and exit status.
