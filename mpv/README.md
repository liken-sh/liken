# mpv

`ghcr.io/liken-sh/mpv` is the [`ffmpeg`](../ffmpeg/) image plus `mpv`
and every library and data file that it opens by name. Its entrypoint
is `mpv`. The player image of `media-operator` builds on it.

## What the image holds

The `Dockerfile` builds `mpv` from its upstream release, not from
Debian's package, because a fix that `liken` needs is in no package
that sid holds: `mpv` 0.41 did not reset FFmpeg's TrueHD framer on a
seek, so a TrueHD track that the player passed through to a receiver
went silent after each seek. The fix is upstream commit
[`1388a453950f`](https://github.com/mpv-player/mpv/commit/1388a453950f202a29d1c0346797d730a9710d72),
which `mpv` 0.42.0 holds. 0.42.0 needs libplacebo 7.387.0, and sid
has 7.360.1, so the build takes the 0.41.0 release and applies that
one commit from `patches/`. The build turns on only what the player
uses: the Wayland and `dmabuf-wayland` video outputs, VA-API decoding,
the PipeWire audio output, libass for the subtitles and the OSD, and
Lua, because the player passes `--osc=no` and `--load-console=no`,
which only a build with Lua accepts. The comment above the build in
the `Dockerfile` gives the reason for each option it turns off. Every
library `mpv` links comes from the same Debian snapshot as the other
bases.

`mpv-closure.sh` names `mpv` and the PipeWire client modules and SPA
plugins that its audio output loads by name, found in a traced
playback run. It collects them and every library they load, `mpv`'s
license files, the PipeWire client configuration, and the fontconfig
configuration that libass reads. It then leaves out every file that
the images under this one already hold, so the layer carries no second
copy of the libav libraries or the VA-API driver.

No Debian package names `mpv` in the image, so syft finds no package
for it. The image carries `/usr/share/doc/liken/mpv.cdx.json`, a
CycloneDX record of the release tarball that `image-sbom` writes from
`pin.toml`. The record states the patch as the component's
`pedigree`, a CycloneDX commit and a `backport` patch for each URL in
the `backports` of `pin.toml`, so it does not claim a stock 0.41.0.
syft copies the component, with its purl, its license, and NVD's CPE
for `mpv`, into the SBOM of this image and of each image that builds
on it. The SBOM that syft writes does not keep the `pedigree`, so the
record in the image is where a reader finds the patch.

`smoke/mpv.sh` runs the image before it ships. It checks the notices
and the SBOM record, checks that `mpv` is the release that the
`Dockerfile` pins, checks that the binary holds the reset of the spdif
muxer, and checks that the `dmabuf-wayland` video output, the
PipeWire audio output, and VA-API decoding are in the build. Then it
plays a clip that the image's own `ffmpeg` encodes, with a subtitle
track and an AC-3 track that mpv passes through, to no display and no
audio, with the IPC socket open.

## The version and the revision

`mpv` is a pinned component. Its `package.toml` states a version and a
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

## Bump mpv

`latest.sh` reports the newest `mpv` release on GitHub, and
`latest.sh --bump` writes it into the `Dockerfile` and `pin.toml`: the
version, the URL of the tag's archive, and its sha256. `mpv` publishes
no checksum file. From 0.42.0 on, the release carries a signature of
the tag's archive, and the bump checks that signature against
`signing-keys.asc`, the key of `mpv`'s release manager, before it
writes the pin. A release with no signature, or a signature by
another key, stops the bump. The 0.41.0 release has no signature, so
the pin rests on its sha256.

The row reads `held` while the newest release needs a newer libplacebo
than the snapshot's, which the `libplacebo` entry of `package.toml`
states. The bump refuses then, and it refuses while `patches/` holds a
patch. The move to 0.42.0 takes these steps:

1. Move the snapshot to a date whose libplacebo is 7.387.0 or newer,
   as the next section says. `vulkan/latest.sh --bump` writes the new
   `libplacebo` entry, and the `mpv` row no longer reads `held`.
2. Delete `patches/0001-ad_spdif-recreate-spdif-muxer-on-reset.patch`,
   whose commit 0.42.0 holds, and its URL in the `backports` of
   `pin.toml`. Keep `patches/README.md`.
3. Run `latest.sh --bump`.
4. Read 0.42.0's `meson.options` against the list in the
   `Dockerfile`. 0.42.0 adds `dvda`, `libcurl`, and `subrandr`, which
   `--auto-features=disabled` already turns off; name each one as
   disabled.
5. Raise the revision, unless the snapshot bump set it to 1 in the same
   commit, run `make workflows`, build the image, and run
   `smoke/mpv.sh`.

## Bump the snapshot

All five bases move to a new snapshot together. The steps are in the
[`vulkan` README](../vulkan/README.md#bump-the-snapshot), and the
`bump-components` skill under `.agents/skills` holds the whole
procedure.

## Build

The bake file at the top of the repository builds this image, from
the top of the repository:

    docker buildx bake mpv --load
