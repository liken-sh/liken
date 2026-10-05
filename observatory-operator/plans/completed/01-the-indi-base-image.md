# 01, The INDI base image

Built on 2026-10-05, and published as `ghcr.io/liken-sh/indi:20261005-1`
and `-2`. The measurements below come from a prototype on a workstation
and from the published image.

## The problem

Every image of the observatory runs INDI: the server, each driver, and
the simulators. INDI's current releases come from the INDI PPA on
Ubuntu, not from Debian. Debian packages INDI 1.9.9 in sid and forky,
and 2.2.5 only in experimental. Ubuntu 26.04 with the PPA's `indi-bin`
is about 1 GB, and almost all of it is Ubuntu and apt, which INDI
never loads.

The other bases of the repository solve the same problem with a
library closure: `vulkan/closure.sh` collects named programs, every
library the loader resolves for them, and the data files they read by
name, into one tree on `scratch`. This image uses the same method.

## The image

`indi` is a pinned component at the top of the repository, like `mpv/`
and `weston/`. It is a closure on `scratch` of these seeds:

- `indiserver`.
- `socat`, which each device pod runs to serve its driver's stdin and
  stdout over TCP:
  `socat TCP-LISTEN:7625,reuseaddr EXEC:<driver>,pipes`.
- Every driver in `indi-bin`: 227 binaries, of which 208 are drivers,
  15 are simulators, and the rest are tools such as `indi_getprop`.
  Every core mount, focuser, and dome driver runs from this image with
  no image of its own.
- The alignment subsystem's math plugins in
  `/usr/lib/x86_64-linux-gnu/indi/MathPlugins/`. `libindiAlignmentDriver`
  loads them with `dlopen`, so `ldd` does not list them, and a closure
  built from `ldd` alone leaves them out with no error. Mounts use
  them for pointing models.
- The driver catalog and the drivers' skeleton files in
  `/usr/share/indi`.
- The shim that connects the server to each device pod, from
  [plan 03](03-the-topology-by-hand.md). It is the only code of our
  own in the image.

The image holds no shell. `/tmp` exists, and `HOME` is `/tmp`, because
a driver writes its configuration under `~/.indi`. The loader's cache
is built with `ldconfig -r` over the tree, as the other bases do.

Some drivers call `system()` or `popen()`, which need `/bin/sh`. None
of those calls is on a path that a pod uses: the GPS interface sets
the host's clock (`libs/indibase/indigpsinterface.cpp`), the CCD
simulator renders planets through ImageMagick and runs `gsc` (plan 02
gives the simulator image a shell for that), one 10Micron command runs
a program, and every LX200 driver links `system` only through the
bundled `fpack` code.

## Pinning and versioning

Both archives install from dated snapshots: `snapshot.ubuntu.com` for
Ubuntu, and `snapshot.ppa.launchpadcontent.net` for the PPA. The PPA
itself keeps only the newest build of each package, so an image built
from the live PPA cannot be built again. The snapshots can: on
2026-10-04 the PPA snapshot of 2026-09-20 served `indi-bin` 2.2.4, and
the snapshot of 2026-10-04 served 2.2.5. Whether an old snapshot keeps
its files for good was not checked.

The version follows the other bases: the snapshot date as `YYYYMMDD`,
and a revision, published as `<version>-<revision>`. The date names the
snapshots at midnight UTC of that day. `[package.upstream]` in
`package.toml` states the package versions that the date installs, and
CI writes each one as the image label `sh.liken.upstream.<name>`. Every
base of the repository now states its upstream releases the same way.

One date pins both archives. The INDI version alone does not name a
build, because the PPA builds from git every night, as in
`2.2.5+202610040455`. The image records the versions of `indi-bin`,
`indi-3rdparty-drivers`, and `gsc` that it was built from in
`/etc/indi-versions`, and every smoke check fails when they differ
from `[package.upstream]`. No check compares the package version with
the version that `indiserver` reports about itself, which
[indi#2335](https://github.com/indilib/indi/issues/2335) found
disagreeing once.

The Ubuntu image has no CA bundle, and the snapshot services answer
only over HTTPS. The builder copies the bundle of the `trust`
component, which pins Mozilla's roots by date for the OS and every
image, and `indi/snapshot.sh` points apt at it and at both snapshots
before the first `apt-get`. CI refuses a pinned stage that runs apt before a
`snapshot.sh`. The final images carry no bundle, except `indi-open`.

## Measurements

| What | Size |
|---|---|
| Ubuntu 26.04 with `indi-bin` and its dependencies | about 1 GB |
| The closure of `indiserver` and `socat` alone | 19 MB |
| The `indi` image with every core driver | 71.2 MB |

The largest libraries in the closure are `libcrypto` (6.1 MB),
`libnova` (3.5 MB), `libgsl` (2.9 MB), and `libstdc++` (2.6 MB).

## How we test it

- Each of the 232 binaries and plugins in the image resolves every
  library with the image's own loader:
  `/lib64/ld-linux-x86-64.so.2 --list <file>`. The prototype passed for
  all 232.
- `indiserver` starts the telescope simulator, the CCD simulator, and
  the LX200 driver in simulation mode, and a client connects each one
  and slews the mount. The prototype passed. The LX200 driver defined
  40 properties after it connected.
- The smoke test reads `DRIVER_INFO` and the INDI version.

## Maintenance

A bump of the snapshot date is the only way the image takes a new INDI.
The `bump-components` skill holds the procedure for the other bases,
and this base follows it. Each bump repeats the `dlopen` search of
plan 02 over the new closure, because a new release can add a library
that `ldd` does not see.

## Upstream issues

- [indi#2472](https://github.com/indilib/indi/issues/2472), CVE-2026-71979:
  one unauthenticated packet of about 1.2 KB to port 7624 crashes
  `indiserver`. The fault is in the shared XML read path, so a driver
  can trigger it too. [PR #2473](https://github.com/indilib/indi/pull/2473)
  fixed it on 2026-08-14, and v2.2.5 is the first release with the fix.
  The image pins INDI 2.2.5 or later.
- [indi#2485](https://github.com/indilib/indi/issues/2485): private
  vulnerability reporting is on, and a researcher has more findings to
  report. Security bumps of this base are likely.
- [indi#2265](https://github.com/indilib/indi/issues/2265): the PPA
  builds only for Ubuntu LTS releases, so the base stays on an LTS.
- [indi#2240](https://github.com/indilib/indi/issues/2240): the PPA's
  `libxisf` conflicts with Ubuntu's `libxisf0`.
- [indi#2418](https://github.com/indilib/indi/issues/2418), open: a
  community build publishes Debian trixie packages. If it lasts, the
  base could move to the same Debian snapshot as the other bases.

## References

- INDI releases: <https://github.com/indilib/indi/releases>
- The INDI PPA: <https://launchpad.net/~mutlaqja/+archive/ubuntu/ppa>
- Ubuntu snapshots: <https://snapshot.ubuntu.com/>
- The closure method: `vulkan/closure.sh`, and
  [Loads that ldd cannot see](../../../display-operator/plans/open-problems/loads-that-ldd-cannot-see.md)
- [Root plan 74](../../../plans/74-astrophotography.md), "Images"
