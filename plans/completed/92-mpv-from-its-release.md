# 92, mpv from its release

Proposed and built on 2026-11-10. Touches `mpv`, `toolchains`, `ci`,
and, through the recipes, `weston`, `trust`, `indi`, and
`observatory-operator`. The image built and passed its smoke check and
the player's on a workstation with no GPU, no compositor, and no sound
server. On the same day, on a home cluster, the development build
`2026.11.09-003-dev-002-1353639c` played a TrueHD film with Atmos
through a receiver on a television, with `dmabuf-wayland`, VA-API, and
PipeWire, and the sound stayed through the seeks that had stopped it
before. The `liken-1` testbed has not run the build yet.

## The problem

When the player passes a TrueHD track through to a receiver, the sound
stops after a seek and does not come back. mpv's spdif decoder keeps
one FFmpeg spdif muxer for the whole file, and the muxer keeps the
partial TrueHD MAT frame of the position before the seek. Upstream mpv
fixed it in commit
[`1388a453950f`](https://github.com/mpv-player/mpv/commit/1388a453950f202a29d1c0346797d730a9710d72),
"ad_spdif: recreate spdif muxer on reset", which creates the muxer
again on each reset and drops TrueHD packets until a major sync. The
fix is in mpv 0.42.0, released on 2026-11-05. On 2026-11-10 sid had mpv
0.41.0-2, and the `mpv` image installed Debian's package from the
snapshot of 2026-11-01.

## Why the source, and why 0.41.0

The image builds mpv from its upstream release, as the `weston` image
has built Weston since plan 85, because the fix is in no package that
sid holds.

0.42.0 does not build on the snapshot. Its `meson.build` needs
libplacebo 7.387.0, and sid had 7.360.1 on 2026-11-10. Debian had
7.387.0 only in experimental. A second libplacebo in the image does
not work either: the `ffmpeg` base's `libavfilter.so.12` links
`libplacebo.so.360`, whose symbols carry no version, so two copies in
one process would bind each other's functions.

So the build takes the 0.41.0 release and applies the one commit of
the fix. `mpv/patches/0001-ad_spdif-recreate-spdif-muxer-on-reset.patch`
is the output of `git format-patch` for the commit, with its author,
date, and commit id. The build applies it with `--fuzz=0` and fails
when it does not apply. A `grep` then fails the build when the tree it
compiles has no `ad_spdif_reset`, and the smoke check reads the
patch's warning text in the binary.

`mpv/latest.sh` reports the pin as held, with the reason
"0.42.0 needs libplacebo 7.387.0, and the snapshot has 7.360.1". It
reads the newest release's libplacebo floor from its `meson.build`,
and the snapshot's version from the `libplacebo` entry of
`mpv/package.toml`, which `vulkan/latest.sh --bump` moves with the
snapshot. The bump refuses while the pin is held, and while `patches/`
holds a patch. The steps of the move to 0.42.0 are in `mpv/README.md`.

From 0.42.0 on, mpv's release manager signs each tag's archive, and
`latest.sh --bump` checks that signature against
`mpv/signing-keys.asc`. 0.41.0 carries no signature, so the pin rests
on its sha256.

## The options

`--auto-features=disabled` turns off each option that the list does
not name, and the list names every Linux option of the release. On:
Wayland, `dmabuf-wayland`, DRM, VA-API through Wayland and through a
DRM node, PipeWire, Lua 5.2, iconv, zlib, and vector instructions.

- The player runs `--vo=dmabuf-wayland`, `--hwdec=vaapi`, and
  `--ao=pipewire` (`playerArgv` in `media-operator/player.go`). mpv
  opens its VA display through the Wayland connection.
- Lua stays on, because the player passes `--osc=no` and
  `--load-console=no`, and mpv defines both options only in a build
  with Lua. A build without Lua refuses them and does not start.
- zlib reads the compressed tracks of a Matroska file.
- X11, the GL, EGL, and Vulkan outputs, vdpau, CUDA, the terminal
  outputs, ALSA, PulseAudio, JACK, sndio, OSS, and SDL are off. No
  pod has an X server or a sound server other than PipeWire, and plan
  31 of `media-operator` found that the GL output cannot draw in the
  player's image, because the EGL stack is in the compositor's image.
- Disc, DVB, archive, and libavdevice input are off. `parseRef` in
  `media-operator/resolve.go` accepts only `https://`, `nfs://`,
  `claim://`, and `pattern://` items, and a pattern is a file in the
  player image. Plan 40 of `media-operator` rejected live lavfi
  sources.
- uchardet is off. It only guesses a subtitle file's charset, and the
  image has none of glibc's gconv modules, so mpv could not convert
  the file anyway. lcms2, zimg, libjpeg, rubberband, and JavaScript
  are off.

## The license

The build keeps mpv's default `-Dgpl=true`, so mpv is
GPL-2.0-or-later. `-Dgpl=false` removes only options that are off
already, mpv's `Copyright` file advises against an LGPL build of the
player program, and Debian's FFmpeg in the base is GPL-3.0-or-later,
because it links libsmbclient.

## The SBOM

`mpv/pin.toml` names the tarball with the purl
`pkg:github/mpv-player/mpv@v0.41.0`, the license, NVD's CPE, and
`backports`, the URL of the upstream commit. `toolchains/manifest`
gives each backport as the component's CycloneDX `pedigree`: one
`commits` entry with the commit id and one `patches` entry of the type
`backport`. So the record at `/usr/share/doc/liken/mpv.cdx.json` states
the release with the commit and does not claim a stock 0.41.0. The
version stays the release's own. `cd ci && go run . pins -root ..`
fails a backport that is not the URL of a GitHub commit.

syft copies the component's name, version, purl, CPE, and license into
the image's SBOM, and drops the `pedigree`, so a reader finds the
patch in the record in the image. syft reads the record only with its
SBOM cataloger, which buildx's scanner turns on and the `syft` command
does not, so a scan by hand takes `--select-catalogers +sbom-cataloger`.

`toolchains/manifest` is a build context of `weston` and `trust`, so
the change raised their revisions, and `indi`'s, which copies `trust`.
`make -C observatory-operator drivers firmware` wrote the new tags.

## What the workstation measured

| | before, Debian's 0.41.0-2+b4 | after, 0.41.0 with the patch |
| --- | --- | --- |
| `mpv` image | 558.6 MB | 554.4 MB |
| `mpv` layer over `ffmpeg` | 11.3 MB | 7.1 MB |
| player image | 582.1 MB | 577.9 MB |

These libraries left the image: libEGL, libGLdispatch, libgbm,
libwayland-egl, libXpresent, libXrandr, libXss, libarchive and libacl,
libjpeg, libmujs, libsixel, libsndio, and libuchardet. The image got
only 4.2 MB smaller, because libplacebo, libass, zimg, lcms2,
libbluray, PulseAudio, JACK, and X11 are in the `ffmpeg` base already,
since Debian's FFmpeg links them.

The player image played a test pattern with every option that the
player passes, to `--vo=null` and `--ao=null`. It played an `edl://`
timeline of two patterns, read an `https://` file with
`--tls-verify=yes`, and refused `https://expired.badssl.com/`. It
answered `get_property` and `script-message` on the
`--input-ipc-server` socket, and passed an AC-3 track through as
`spdif-ac3`. libass drew a subtitle in Source Sans 3 into a frame of
`--vo=image`.

A capture of the bitstream on the workstation shows the fix. The
player image sent a TrueHD track through PipeWire 1.6.9 to an ALSA
loopback card, and a second program recorded the card while mpv took
16 seeks, 3 seconds apart. The image with Debian's mpv 0.41.0 logged
FFmpeg's `Unusual frame timing` once for each seek, which is the
framer packing the first packet after the seek with the state of the
position before it. The image of this plan logged none. In both
images, each seek left a gap between two bursts that is not a whole
number of bursts, and some gaps held about 2,000 bytes of a burst with
no header, 4 of 17 in the image of this plan. The patch does not
change that, and no test has shown whether a receiver reacts to it.

## What is not proved

- Playback with the new image on the `liken-1` testbed. A home
  cluster played a TrueHD film on a television with it, as the header
  says.
- A film with a codec or a subtitle format that the narrow build
  reads through a library the test films did not use.
