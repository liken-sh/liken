# mpv

`ghcr.io/liken-sh/mpv` is the base image for a program that plays
media with `mpv` on a Wayland compositor. It contains `mpv`, built
from its upstream release, and every library, plugin, and data file
that `mpv` opens by name. Its entrypoint is `mpv`. It is the
[`ffmpeg`](../ffmpeg/) image with `mpv` added, so `mpv` decodes with
the libav libraries and the VA-API drivers of the images below it.

The build turns on only the features that a player in a pod uses:

- the Wayland and `dmabuf-wayland` video outputs
- VA-API decoding
- the PipeWire audio output
- libass, for the subtitles and the OSD
- Lua, because the player passes options that only a build with Lua
  accepts

The comment above the build in the `Dockerfile` gives the reason for
each option that the build turns off.

The image builds `mpv` from its release, not from Debian's package.
So it can use a new upstream release before sid packages it, or an
upstream commit that no release includes yet. Every library that
`mpv` links comes from the same Debian snapshot as the other base
images.

`mpv` builds on `ffmpeg`. Its layer contains only the files that the
images below it do not. The player image of `media-operator` builds
on `mpv`, and adds the player's own program and the CA bundle of
[`trust`](../trust/).

## What the image contains

- **`mpv`**, the release that the `Dockerfile` names, with each
  upstream commit in [`patches/`](patches/) applied.
- **The PipeWire client** modules and SPA plugins that `mpv`'s audio
  output loads by name, and PipeWire's client configuration.
- **The fontconfig configuration** that libass reads.
- **The record of each package**, as in `vulkan`, and a CycloneDX
  record of the release tarball, `mpv.cdx.json`.

No Debian package in the image provides `mpv`, so syft finds no
package for it. Instead, `image-sbom` writes `mpv.cdx.json` from
`pin.toml`. syft copies the component in that file into the SBOM of
this image and of each image that builds on it, with its purl, its
license, and NVD's CPE for `mpv`. The record lists each patch in the
component's `pedigree`, so the SBOM does not present the binary as
an unmodified release. syft drops the `pedigree` when it copies the
component, so the list of patches is only in the record in the
image.

CI runs `smoke/mpv.sh` on the image before it publishes. The script
plays a clip to the null video and audio outputs, so nothing is shown
or heard, and its comments list what else it checks.

The image publishes under the tag `<version>-<revision>`, where the
version is the date of the Debian snapshot. The label
`sh.liken.upstream.mpv` records the release. The design of the image
is in `display-operator`'s
[plan 20](../display-operator/plans/completed/20-the-mpv-image.md).
The reasons to build from the release are in root
[plan 92](../plans/completed/92-mpv-from-its-release.md).

## Bumping mpv

`latest.sh --bump` stops in each of these cases, and each one needs a
step from you first.

**A patch is in `patches/`.** Each patch is a fix from a newer mpv
release. If the release you are bumping to already includes the fix,
the patch no longer applies and the build fails. Delete each patch
that the new release includes, remove its URL from `backports` in
`pin.toml`, and remove its row from the table in `patches/README.md`.

**The Debian snapshot's libplacebo is too old.** Each mpv release
needs a minimum version of libplacebo. When the snapshot does not
have it, `make versions` shows mpv as `held`. Move the snapshot to a
newer date with `vulkan/latest.sh --bump`, which also records the new
libplacebo version in `package.toml`.

Then:

1. Run `latest.sh --bump`.
2. Compare the new release's `meson.options` with the options that
   the `Dockerfile` sets, and add each new option to the `Dockerfile`.
3. Raise the revision in `package.toml`. Skip this if the snapshot
   bump already reset it to 1 in the same commit.
4. Run `make workflows`, build the image, and run `smoke/mpv.sh`.

MIT. See [LICENSE](LICENSE). `mpv` is under GPL-2.0-or-later.
