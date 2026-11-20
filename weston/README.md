# weston

`ghcr.io/liken-sh/weston` is the Weston compositor, packaged to drive
a machine's monitors from a pod. It contains Weston, built from its
upstream release, every library and driver that Weston loads,
`liken`'s modules for it, and programs that diagnose a screen. Its
entrypoint is `weston`.

The build turns on only the features that `display-operator` uses: the
DRM and headless backends, the GL renderer, and the ivi-shell. The image
also contains the EGL and GBM loaders and Mesa's DRI drivers, which the
GL renderer uses to draw. The image builds Weston from its release, not
from Debian's package, so it can use a point release with a fix before
sid, Debian's development suite, packages it. The `Dockerfile` explains
which fix. Every library that Weston links comes from the same dated
snapshot of snapshot.debian.org as the other base images, as
[`vulkan`](../vulkan/) explains. A program that draws on the screen runs
in its own pod and connects to the compositor's socket.

`weston` builds on [`vulkan`](../vulkan/). Its layer contains only
the files that `vulkan` does not, so a node that runs the compositor
and Vulkan clients stores one copy each of glibc, libdrm, and LLVM.
The image of `display-operator` builds on `weston`, and the
operator's pod runs this compositor. The compositor ships in that
image and not in the OS image, so a machine with no screens never
downloads it.

## What the image contains

- **Weston**, with the features above.
- **The hotplug shim**, in `hotplug/`, a library that
  `display-operator` preloads into the compositor. Weston normally
  listens for monitor hotplug events on udevd's netlink group. A
  `liken` machine runs no udevd, so the shim makes Weston listen on
  the kernel's netlink group instead.
- **The layout controller**, in `layout/`, an ivi-shell module. It opens
  one Wayland socket for each claim, the `ResourceClaim` through which a
  pod gets a screen. It also opens a control socket, which
  `display-operator` uses to place, fade, and move each window. A
  claim's region is the rectangle of the screen that `display-operator`
  gives the claim. Inside it, one client of the claim can arrange the
  claim's surfaces, which means to set the position, size, and opacity
  of each one. It does this through `liken_arrangement_v1`, a protocol
  that the controller implements. The protocol is defined in
  `layout/liken-arrangement-v1.xml`.
- **The diagnostic programs**, such as `wayland-info`, which lists
  every global that the compositor advertises, and
  `liken-arrange-drill`, which arranges a claim's surfaces by hand.
  The image has no shell, so you run each one by name with
  `kubectl exec`. The `Dockerfile` names each one.
- **The record of each package**, as in `vulkan`, and a CycloneDX
  record of the release tarball, `weston.cdx.json`. syft copies that
  record into the image's SBOM.

CI runs `smoke/weston.sh` on the image before it publishes. The
script starts the compositor headless and checks that the GL
renderer, the ivi-shell, and the layout controller load.

## Arrange a claim by hand

`liken-arrange-drill` connects to one claim's socket and sends the
arrangements that you give as arguments. It prints each event with
the time since it started. Rectangles are in logical pixels of the
claim's region. When the drill exits, the claim's surfaces move back
to where the operator placed them. So to keep an arrangement on the
screen, end with `wait`, which waits until the last commit finishes,
or `hold`, which keeps the drill connected for a number of
milliseconds:

    kubectl exec -n liken-system <display-operator pod> -- \
        liken-arrange-drill /var/run/display.liken.sh/wayland-<claim uid> \
        entry mpv 96 270 960 540 1 scale 0 commit small 900 smoothstep \
        wait hold 3000

The comment at the top of `layout/liken-arrange-drill.c` lists every
command and exit status.

The image publishes under the tag `<version>-<revision>`, where the
version is the date of the Debian snapshot. The label
`sh.liken.upstream.weston` records the release. The design is in
`display-operator`'s plans:
[plan 01](../display-operator/plans/completed/01-the-compositor-image.md)
for the image,
[plan 17](../display-operator/plans/completed/17-a-layout-for-every-screen.md)
for the layout controller, and
[plan 31](../display-operator/plans/completed/31-an-arranger-inside-each-region.md)
for `liken_arrangement_v1`.

MIT. See [LICENSE](LICENSE).
