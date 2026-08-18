# Watching the pins

Milestone 48. Built. Every domain that vendors something has a
`latest.sh` beside its `fetch.sh`. It says what the domain pins and
what upstream has now, and it moves the pin when you ask it to.

## The problem

liken vendors 28 pinned things. Sixteen are a domain's `VERSION` file.
The rest are pins inside a fetch script or inside
`licensing/sources.sh`: the Alpine builder that two domains build in,
the kmod, libeconf, and libtirpc versions that those builds link, the
gokrazy commit that the `mke2fs` binary comes from, and the source
tarballs that the release channel mirrors.

One pin moves on its own. `.github/workflows/tzdata.yaml` reads IANA's
version file every Monday and opens the pull request. The other 27
move when a person remembers to look, and looking means reading a
fetch script to learn where its upstream is, then finding that
upstream's idea of a version.

Nobody looked for a while. On 2026-07-29 the kernel was three point
releases behind, the CA bundle was two snapshots old, nfs-utils was a
minor version behind, and the lab's storage image was two builds
behind. None of that is visible from inside the repository.

## The shape

Every domain that holds a pin gets a `latest.sh` beside its
`fetch.sh`. Run alone, it reports. Run with `--bump`, it writes.

```
kernel/latest.sh                     report the kernel domain's pins
kernel/latest.sh --bump              move kernel/VERSION and re-pin
open-iscsi/latest.sh --bump kmod     move one nested pin instead
make versions                        every domain's report, in one table
```

The knowledge is in the domain, for the same reason `fetch.sh`
is. Where the kernel comes from, and what counts as a kernel
release, is a fact about the kernel domain. A single script for all 28
upstreams would put every domain's knowledge in one place.

`latest.sh` prints one tab-separated line per pin and nothing else:

```
kernel	7.1.2	7.1.5	Ubuntu mainline, newest build with no -rc
```

The root `versions` target adds the verdict column and the alignment.
Policy stays in the domain and formatting stays in one place. A script
that cannot reach its upstream prints `?` in the latest column and
exits 0, so one unreachable host does not hide the other fifteen
reports.

## What latest means

The newest tag is the wrong answer often enough that each script owns
its own rule.

**The kernel.** Canonical's mainline archive builds release
candidates too, and the index lists them beside the releases. The
script takes the newest directory whose name has no `-rc` suffix.

**k3s.** The newest tag is not the version to run. k3s publishes a
release channel at `update.k3s.io`, and the `stable` channel names the
version that liken tracks. The script reads that field.

**GRUB and systemd-boot.** Ubuntu's pool holds several series at
once. The pin `2.12-1ubuntu7.3` is a build in one series, and the pool
also holds `2.14-2ubuntu3` from a newer one. Moving between those is
an upstream version change, not an update. So the latest column names
the newest build in the pin's own series line, which is the security
update you almost always want, and the note names the newest build in
the pool. The rule also catches the failure this pool is known for: a
build that leaves the pool can no longer be fetched, and a pin that no
longer appears in the listing says so.

**Followers.** Some pins do not move on their own. musl and util-linux
are whatever the pinned Alpine image ships, so the script reads that
image's package index. iptables is whatever the pinned k3s-root
release builds, so the script reads `BUILDROOT_VERSION` from that
release's download script and then the iptables version from that
buildroot tag. glibc is whatever the base image in the gokrazy recipe
ships. That last one has no version endpoint at all, so the script
reads the recipe's `FROM` line and reports glibc as current while the
base image is unchanged.

## What --bump writes

The pin shapes differ, so each script handles its own. Most are a
`VERSION` file. A nested pin is a `foo_version=` and `foo_sha256=`
pair in a fetch script. GRUB and systemd-boot keep an associative
array, because an old checkout must still build, so a bump adds an
entry instead of replacing one. tzdata keeps a `DIGESTS` file.

Six domains have no digest literal at all: the kernel, k3s, xtables,
Flux, the CA bundle, and the lab's storage image. Each of those
upstreams publishes a checksum beside the artifact, and their fetch
scripts read it at fetch time. For them a bump is one line in
`VERSION`.

Where a bump does write a digest, the bytes it writes must have been
verified first. k3s, Flux, xtables, the CA bundle, and the Debian
image verify against upstream's published checksum. tzdata verifies
IANA's detached signature against `tzdata/tz.asc`, the same order the
tzdata workflow uses. Where upstream publishes neither, the digest
comes from the bump's own download, and the script says so in its
output, because a digest that a robot computed from its own download
pins bytes and proves nothing about their source.

A bump does not build. It ends by naming the next command,
`make -C <domain>`.

## Licensing moves with the pin

`licensing/sources.sh` mirrors the source for everything liken
redistributes, and every file it mirrors is pinned there by digest. A
domain pin that moves without its source pin fails a release, by
design.

The mirror function is the one place every source file passes through,
so a `--repin` mode changes what that function does: it downloads the
URL for the current pins, and where the bytes no longer match the
literal, it replaces that literal in the file. A sha256 string appears
once in the file, so the replacement is exact.

One case the mode must not guess at. `systemd_259.5.orig.tar.gz`
has an upstream version inside its filename, so a bump to 261
changes the URL and not only the digest. `--repin` reports a URL that
no longer resolves and stops. The mirror path is what the source offer
depends on, and a wrong guess there ships a release whose sources
404.

`NOTICES.md` needs nothing from a bump. It names one version in its
prose, glibc's, because that library arrives inside a binary and its
notice has to say which one. Every other notice names a project and a
license, and neither of those moves when a pin does.

## What stays a hand edit

The Go module graph and the Go toolchain in `go.mod`, and the action
pins in `.github/workflows`. Neither is a vendored binary, and
`go get -u` already owns the first.

No new workflow. The tzdata job keeps its weekly pull request, because
signatures and `zic` verify that one end to end. Its verify-and-pin
step becomes a call to `tzdata/latest.sh --bump`, so the rule for
reading IANA's version is in the domain with every other domain's
rule, and not in a workflow.

## What the first run found

`make versions` reads 28 pins in 8 seconds. On 2026-07-29 it found 19
of them current and 9 behind: the kernel by three point releases
(7.1.2 against 7.1.5), the CA bundle by two snapshots, nfs-utils by a
minor version, libtirpc, libeconf, open-iscsi, Flux, the lab's storage
image by two builds, and systemd-boot by one security update inside
its own series.

Two rows need more explanation. GRUB is current in its series line
at `2.12-1ubuntu7.3` while the pool also holds `2.14-2ubuntu3`,
which is the case the series rule exists for. The five source-mirror
pins all match what their governing pin ships now, which is what a
release depends on and what nothing checked before.

## What the drills measured

Each bump wrote a digest that something else agrees with. The kernel
bump moved `VERSION` to 7.1.5, and the re-pin wrote
`22a0196b3cbc...` for `linux-7.1.5.tar.xz`, the same digest kernel.org
publishes in its own signed sums file. The nfs-utils bump wrote
`e1dd8a9c95af...`, which is the line for that tarball in the release
directory's sums file. The open-iscsi bump wrote `e2441b61e4b0...`,
which matches an independent hash of the same archive.

The refusal works and costs nothing. A bump across GRUB's series to
`2.14-2ubuntu3` wrote the new pin and its two digests, then the
re-pin fetched `grub2_2.12.orig.tar.xz` under the new version, got a
404 error, reported the URL, and stopped. `sources.sh` was unchanged
afterward, because the edits are applied in one pass at the end.

An unreachable upstream degrades instead of failing. With the
mainline index pointed at a host that does not resolve, the report
printed `?` and exited 0, so one dead host cannot hide the other
sixteen reports. The same script's `--bump` refused and exited 1.
