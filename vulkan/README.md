# vulkan

`ghcr.io/liken-sh/vulkan` is the Vulkan loader, the Intel and AMD
Vulkan drivers, and the client libraries that a Wayland program opens
by name, on `scratch`. It is the lowest base image of the repository.
[`vaapi`](../vaapi/) and [`weston`](../weston/) build on it, and every
Vulkan client that `liken` ships builds on it or on an image above it:
the idle screen and the display of `media-operator`, and the media
browser of `library-operator`.

## What the image holds

The image is a library closure. `vulkan-closure.sh` names the seeds:
the loader, `libvulkan_intel.so`, `libvulkan_radeon.so`,
`libwayland-client.so.0`, and `libxkbcommon.so.0`. It copies each seed,
every library that the dynamic loader resolves for it, and the data
files that the seeds read by name: the ICD files, Mesa's `drirc`, the
keymap data under `/usr/share/X11/xkb`, and the time zone database.
The image holds no shell and no package manager.

The image also holds the notices that the licenses of its files ask to
travel with them. A closure copies only what the loader resolves, so
`closure.sh` also copies the copyright file of each Debian package that
the closure took a file from, to `/usr/share/doc/<package>/copyright`,
and the license texts of `/usr/share/common-licenses`. It writes
`/usr/share/doc/liken/vulkan.packages`, which names each package, its
version, and its source package. snapshot.debian.org serves the source
of every version that Debian published. Each base above this one
writes its own list beside it, such as `ffmpeg.packages`.

Debian puts every Vulkan driver in one package, and two of the seven,
lavapipe and the AMD driver, link LLVM. The tree keeps the Intel and
AMD drivers and leaves out the other five, so LLVM is in the image for
the AMD driver alone.

This directory also holds the files that every base on Debian uses.
The other bases take this directory as the build context named
`builder` for them:

- `closure.sh` holds the functions that collect a closure, the
  function that copies the notices of the packages a closure took files
  from, and the function that removes from a tree every file that the
  base under it already holds.
- `snapshot.sh` points apt at one day of
  [snapshot.debian.org](https://snapshot.debian.org/), reads the
  package lists, and checks each `InRelease` file against its sha256
  in `snapshot.sha256`. The sources use http, because the slim image
  has no CA certificates. apt checks Debian's signatures, but an old
  snapshot is past its Valid-Until date, so without the checksums a
  host on the path could serve an older signed `InRelease`.
- `snapshot.sha256` holds the sha256 of each `InRelease` file of the
  snapshot date. It is in this image's recipe, so the date and the
  package lists are bound together.

## The version and the revision

`vulkan` is a pinned component. Its `package.toml` states a version
and a revision, and the image publishes under the tag
`<version>-<revision>`, for example `20260928-1`.

- The **version** is the date of the Debian snapshot that the build
  installs from, as `YYYYMMDD`. The build installs every package from
  that snapshot, so the version names the package versions in the
  image.
- The **revision** counts the changes to the recipe at one version. It
  starts at 1 for a new version. Any change to the build that keeps
  the version needs the next revision: a change to the `Dockerfile`, to
  a script, to the Debian image digest, or to `package.toml`.

CI computes a hash of the recipe, and the published image carries it
in the label `sh.liken.recipe`. When the hash of the tree differs from
the hash of the published tag, CI fails and names each component that
needs a new revision. A published tag never changes.

Every base on Debian uses the same snapshot date and the same Debian
image digest, so a library that two bases hold is the same file in
both, and a node holds it once.

## Bump the snapshot

Every image of the repository that installs Debian packages into what
it ships installs from one snapshot of sid, Debian unstable, on one
date. Root [plan 82](../plans/completed/82-debian-sid.md) gives the reasons. sid
moves every day, so a new date can bring a new major release of Mesa,
ffmpeg, or Weston, and each bump is an upgrade to test.

1. Pick the date. The snapshot at `YYYYMMDDT000000Z` is the newest one
   before midnight UTC of that day. Check that it exists:

       curl -sI http://snapshot.debian.org/archive/debian/YYYYMMDDT000000Z/dists/sid/Release

2. Replace the checksum line of `snapshot.sha256` with the sha256 of
   the date's `InRelease` file. Fetch it over https, which checks the
   host:

       curl -sfL https://snapshot.debian.org/archive/debian/YYYYMMDDT000000Z/dists/sid/InRelease | sha256sum

   The line names the file as apt stores it under
   `/var/lib/apt/lists`, as the line there now does, with the new date.
3. Read the digest of the newest `debian:sid-slim`:

       docker buildx imagetools inspect debian:sid-slim

4. Set the digest in the `debian` entry of `toolchains/images.toml`.
   Every Dockerfile in the repository names that image bare, as
   `FROM debian`, and the bake file gives each one the same digest, so
   that each one installs the glibc that the bases carry. In all five
   bases, `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`, set
   `version` to the date and `revision` to 1 in `package.toml`.
   `astrometry` installs from the same snapshot, because this
   directory's `snapshot.sha256` holds the checksum of one date, so
   set its `version` and `revision` the same way.
5. Run `make workflows` at the top of the repository. The bake file
   carries each base's version and tag.
6. Run `make images` at the top of the repository. It builds every
   image, each base and each consumer, for this machine's platform.
   Then run each image's smoke check, the script that its
   `package.toml` names under `smoke`, with the image's reference.
7. Compare the package versions with the old snapshot. A soname that
   changed, such as `libLLVM.so.22`, fails a closure script that names
   the old one, and a new major release of a program can drop a flag
   that a component passes it.

The `bump-components` skill under `.agents/skills` holds the whole
procedure, with the rule for a new revision.

## Build

The bake file at the top of the repository builds this image, from
the top of the repository:

    docker buildx bake vulkan --load
