# 82, Every Debian closure on one sid snapshot

Proposed, built, and drilled on 2026-10-24. The dev builds passed the
drills on the `liken-1` testbed and on a home cluster, and
[The drills](#the-drills) gives what they found.

`liken`'s images take their graphics and video stack from Debian
packages, and each image installs from one dated snapshot of
snapshot.debian.org. Until this plan, the suite was trixie, Debian 13,
the stable release. A stable release takes security fixes and no new
versions, so the stack stayed where trixie froze it: Weston 14.0.2,
Mesa 25.0.7, ffmpeg 7.1.5, and mpv 0.40.0.

Weston 14 is the cause of a visible fault. With subtitles on, the
picture shifts color each time a subtitle line appears, and shifts
back when the line leaves. Weston 14 converts YUV to RGB in two
places. When it puts a video on a display plane, the display hardware
converts with the plane's default, which i915 creates as BT.709 at
limited range (`skl_universal_plane.c` on Linux master). When mpv's
subtitle buffer is on screen, Weston draws the video with its GL
renderer instead, and Weston 14's shader hard-codes BT.601. Weston 15
added `wp_color_representation_v1`, and from that version Weston uses
one set of coefficients on both paths.
[display-operator plan 30](../../display-operator/plans/completed/30-weston-16.md)
holds the measurement and the compositor's side of the move.

## The decision

Every image that installs Debian packages into what it ships installs
from one snapshot of sid, Debian unstable, on one date. sid is where
every Debian upload lands first, so on 2026-10-24 it held the newest
release, or close to it, of each part of the stack:

| | trixie | sid on 2026-10-24 | upstream on 2026-10-24 |
|---|---|---|---|
| Weston | 14.0.2 | 16.0.0 | 16.0.1 |
| Mesa | 25.0.7 | 26.2.5 | 26.2.5 |
| ffmpeg | 7.1.5 | 9.0.2 | |
| mpv | 0.40.0 | 0.41.0 | 0.41.0 |
| libva | 2.22.0 | 2.24.1 | |
| Intel media driver | 25.2.3 | 26.3.5 | |
| Vulkan loader | 1.4.309 | 1.4.363 | |
| libdrm | 2.4.124 | 2.4.134 | 2.4.134 |
| libwayland | 1.23.1 | 1.26.0 | 1.26.0 |
| PipeWire | 1.4.2 | 1.6.9 | |

The versions come from ftp-master's madison API, the sid `Packages`
index of the snapshot, and the upstream tag lists.

Three alternatives were set aside:

- **Build the stack from source on trixie.** Each shared library
  would build in the lowest image that holds it, so that every image
  above links one copy. That makes about ten source builds to own,
  among them Mesa with LLVM, ffmpeg, and the Intel media driver.
  Debian already builds them.
- **Weston from source, the rest on trixie.** It fixes the fault and
  leaves the rest of the stack two years behind.
- **forky, Debian testing.** forky trails sid by days to weeks, and on
  2026-10-24 it had Mesa 26.1.6 and Intel media driver 26.2.4. A
  snapshot already freezes the suite, so testing's extra settling
  time does not help these builds.

sid breaks more often than stable. A library that changes its soname
leaves its dependents uninstallable for a few days. Because the build
reads a dated snapshot, a bad date fails the build and changes
nothing that runs. The bump takes another date. sid has no security
suite: a fix arrives as an ordinary upload, often within days of the
advisory, and an image takes it at the next bump. That is the same
rule the trixie snapshots had, because `snapshot.sh` froze the
security archive at the date too.

So each bump of the date is a real upgrade, and it runs the same path
every time: the CI smoke checks, the dev builds on the `liken-1`
testbed, and then the house cluster.

## What moves

- **The bases**: `vulkan`, `vaapi`, `ffmpeg`, `mpv`, and `weston`,
  and `astrometry`, which installs through the same snapshot. Each
  starts from `debian:sid-slim` and takes the new date and revision 1.
  `vulkan/snapshot.sh` names the suite `sid` and has no updates or
  security suite.
- **The other closures**: `audio-operator`, `git-csi-driver`, and
  `firmware-operator/scan` installed from an unpinned
  `debian:trixie-slim`, and `media-operator`'s player and screen
  images took a font from one. Each moves onto `snapshot.sh` and the
  shared date. `media-operator`'s capabilities image and
  `library-operator`'s appearances image copy glibc from the Debian
  image, so they start from the same `debian:sid-slim`.
- **The compositor**: `weston` moves to Weston 16, with the changes
  in display-operator plan 30.
- **The toolchains stay.** The `golang` and `rust` builder stages on
  bookworm compile programs, and they ship no Debian library. A binary
  that links glibc from bookworm runs on the newer glibc of sid.
  `liken/dev-cluster/storage` fetches a trixie cloud image for a lab
  guest, which ships nothing in an image, and it stays too.

## Documents

The text that names trixie as the suite moves with the code:
`vulkan/README.md` and its snapshot steps, the base-image section of
the `bump-components` skill, the root `AGENTS.md`, and every comment
that names the suite. The manuals of `display-operator` and
`media-operator` change where Weston 14 behavior shows to a reader.

## Order

1. The snapshot: `snapshot.sh`, `snapshot.sha256`, the bases, and
   `astrometry`, with each smoke check.
2. The compositor, as display-operator plan 30 gives it.
3. The other closures, and the documents.
4. The dev builds on `liken-1`: the operators that run on the bases
   start, each component's pods run, and a Player plays.
5. The dev builds on the house cluster, for the drill of the
   subtitles.

## The drills

On 2026-10-24 the dev builds ran on `liken-1` and then on a home
cluster.

- On `liken-1`, Weston 16.0.0 started on Mesa 26.2.5 and logged `color
  representation: yes`. The media browser drew, and `lab-portable`
  played a film with English subtitles. With the `drm-backend` scope
  on, every commit set `COLOR_ENCODING` to BT.709 and `COLOR_RANGE` to
  limited, where Weston 14 set neither. `stick-1` composited every
  frame with the GL renderer, so the testbed did not show the plane
  path.
- On `liken-1`, `audio-operator` on `stick-1` failed its WirePlumber
  startup probe. PipeWire 1.6's bluez5 plugin refuses to start without
  its HFP CVSD codec, and the closure named only the codecs of
  PipeWire 1.4. The closure now takes every bluez5 codec plugin.
- On the home cluster, a film whose picture had shifted color with
  each subtitle line earlier that day played with no shift.
- On the home cluster, the OSD's scrims drew as flat bands. Mesa 26
  lists 10-bit swapchain formats first, and the display took
  `ABGR2101010`, whose alpha has 2 bits. The display image now makes
  Mesa list `B8G8R8A8_UNORM` first, and Weston's scene dump on
  `liken-1` showed the display's surface as `ARGB8888`. The open
  problem "the display cannot choose its surface format" in
  `media-operator` holds the rest.
- A display clip, a Player recording, and an audio tap each produced
  a file with picture or sound on the home cluster. No plate solve and
  no firmware scan ran on the new builds.
