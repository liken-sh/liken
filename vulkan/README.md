# vulkan

`ghcr.io/liken-sh/vulkan` is the base image for a program that draws
with Vulkan on a Wayland compositor. It contains the libraries that
such a program opens at run time: the Vulkan loader, Mesa's Vulkan
drivers for Intel and AMD GPUs, and the Wayland client libraries. It
also contains the data files that those libraries open by name, such
as the keymaps and the time zone database. A Vulkan client builds on
this image and adds only its own binary.

The image is a library closure. A closure is an image built
`FROM scratch` that contains only the files a program needs, copied
out of Debian packages. This one has no shell and no package manager.
Every file comes from the Debian packages of one dated snapshot of
sid. For each of those packages, the image also includes its copyright
file and its dpkg record. syft and Trivy read those records, so they
can list the packages of this image and of every image built on it.
The image has no compositor and no video driver. `weston` and `vaapi`
add those.

`vulkan` is the lowest base image. [`vaapi`](../vaapi/) and
[`weston`](../weston/) build on it, and [`ffmpeg`](../ffmpeg/) and
[`mpv`](../mpv/) build on `vaapi`. The screens of the operators, such
as the idle screen of `media-operator`, build on `vulkan` or on an
image above it. Find each image that uses it by its `FROM vulkan`
line.

The `Dockerfile` names the packages, and `vulkan-closure.sh` names
the files that the closure copies from them, with the reasons. The
upstream releases are under `[package.upstream]` in `package.toml` and
in the image's `sh.liken.upstream.*` labels. Every file is in the
image's SBOM.

## The scripts that other images use

Every closure on a Debian builder in the repository gets this
directory as the build context `builder`, for these scripts:

- `closure.sh` is the closure library. It collects the files that a
  program loads, and writes the notices and the dpkg records of the
  packages they came from. It also deletes from a tree each file that
  the base image below it already contains. `indi/closure.sh` is a
  copy of it.
- `snapshot.sh` configures apt to install from one day of
  snapshot.debian.org. It checks each `InRelease` file against its
  sha256 in `snapshot.sha256`.
- `latest.sh` reports the snapshot date of every image that installs
  from snapshot.debian.org. `latest.sh --bump` moves them all to the
  newest date.

The image publishes under the tag `<version>-<revision>`, where the
version is the date of the snapshot. The reasons for sid are in root
[plan 82](../plans/completed/82-debian-sid.md), and the design of the
image is in `display-operator`'s
[plan 16](../display-operator/plans/completed/16-the-vulkan-base-image.md).

MIT. See [LICENSE](LICENSE).
