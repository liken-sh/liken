# The timezone database

Milestone 47 — Built. The image carries the IANA timezone database, so
a CronJob can name the zone its schedule means. A weekly job in CI
opens the pull request that moves the pin.

## The problem

A CronJob names its zone in `spec.timeZone`, a field that has been
generally available since Kubernetes 1.27. liken rejected every value
of it:

```
spec.timeZone: Invalid value: "America/New_York": unknown time zone
```

That message comes from admission, so the object never reached etcd
and Flux could not apply one at all.

A CronJob written before the zone field existed failed differently.
The apiserver had already accepted it, so it sat in etcd and never
fired. The controller logged one event for the failure, and that event
aged out within the hour.

## Why one directory fixes it

Both halves of the feature resolve the zone through Go's
`time.LoadLocation`, and both run inside the k3s process: the
apiserver's validation of the CronJob, and the controller that
schedules it. `LoadLocation` reads `$ZONEINFO`, then
`/usr/share/zoneinfo`, then two paths no modern system uses, then the
copy compiled into the binary when the program imported `time/tzdata`.
k3s does not import it, and liken staged no zoneinfo, so every lookup
came up empty.

No container needs the database. A CronJob's zone is resolved entirely
in the control plane, and a workload that uses a zone carries its own
copy in its image. So the fix is one directory in the system image,
and no environment variable, because the default search path already
names it.

## Why liken compiles it

IANA publishes the database as text rules, not as the binary TZif
files that a program reads. Someone has to run `zic`. liken runs it,
from IANA's own source, for three reasons.

The version matches the release IANA announces. When a country moves
its clocks and the fix ships as 2026c, `tzdata/VERSION` says `2026c`.
A distribution package would name the same data
`2026c-0ubuntu0.24.04`, and a language ecosystem's package would name
it `2026.3`.

The archive does not rot. `data.iana.org/time-zones/releases/` keeps
every release back to 1993. The Ubuntu pool returns a 404 error the
day a newer version replaces the pinned one. `grub/fetch.sh` and
`systemd-boot/fetch.sh` both carry that warning, and tzdata is
superseded four to six times a year, far more often than either of
those.

liken picks the compiler's flags. That decision is `-b fat`, below.

Building from pinned source inside a pinned container is the shape
`open-iscsi` and `nfs-utils` already use, so this adds no build-host
requirement beyond `gpg` and `gpgv`.

## Why the signature, and not the digest alone

Every other vendored domain pins one sha256 digest, written into
`fetch.sh` by the person who chose the version. That pin detects a
change made after somebody chose the version. It cannot detect a bad
download at the moment the pin was written, because the person who
wrote the pin computed it from that same download.

The weekly job makes that gap matter, because no person sees the bytes
before the digest is written. So this domain checks IANA's detached
PGP signature first, against Paul Eggert's key, pinned in
`tzdata/tz.asc` at fingerprint
`7E3792A9D8ACF7D633BC1588ED97E90E62AA7E34`. Eggert coordinates the tz
database and has signed its releases with that key since 2010.

The digest stays, in `tzdata/DIGESTS`, and does its usual job: a
rebuild produces the same bytes the pull request showed.

`DIGESTS` is a separate file rather than a `digest=` line inside
`fetch.sh`. This breaks from the eleven other domains on purpose. A
version bump is then a two-file diff of four lines, which a person can
review at a glance and a robot can write without editing a shell
script.

## Why fat, and not slim

`zic -b slim` writes the smallest file that describes a zone and
leaves future transitions to the POSIX rule string in the file's
footer. `zic -b fat` writes the explicit transitions as well. The
measured difference in liken's squashfs is 33 KB:

```
fat    1.5 MB on disk, 604 files, 131 KB in the zstd squashfs
slim   1.5 MB on disk, 604 files,  98 KB in the zstd squashfs
```

liken ships fat. A workload container that bind-mounts the host's
`/usr/share/zoneinfo` may hold a glibc older than 2.34, which misreads
a slim file.

## Why the machine stays on UTC

The tz `make install` writes an `/etc/localtime`, and this build
discards it along with `zic`, `zdump`, `libtz.a`, and the manual
pages. Only `usr/share/zoneinfo` reaches the image.

Go's `time.Local` falls back to UTC when `/etc/localtime` is absent.
Every timestamp liken writes is RFC3339 in UTC: in the Machine status,
in the facts tree, and in the log relay. A CronJob names its own zone
rather than reading the machine's. A machine-level timezone would be a
setting with no reader.

## Where the version shows up

`image/build.sh` writes `/usr/share/liken/components.yaml` from each
domain's `VERSION` file, and `tzdata` joins that list.
`init/versions.go` carries it into `status.version.tzdata` on the
Machine, beside the kernel, k3s, and the rest. `releases/Makefile`
publishes the same pin in the release document.

An operator who reads a fired CronJob and doubts its hour can ask the
machine which database resolved it:

```
kubectl get machine node-1 -o jsonpath='{.status.version.tzdata}'
```

## The weekly pull request

`.github/workflows/tzdata.yaml` runs every Monday and on demand. It
reads `https://data.iana.org/time-zones/tzdb/version`, a file that
holds the current release name and nothing else, and stops when that
name matches `tzdata/VERSION`. When the names differ it downloads the
two tarballs and their signatures, verifies both against the pinned
key, writes `VERSION` and `DIGESTS`, and opens a pull request.

The job opens a pull request rather than pushing to main. A tz release
sometimes changes more than offsets: it renames a zone, or moves one
into `backward` as a link. A person should see that.

GitHub starts no workflow for a pull request that `GITHUB_TOKEN`
opened, so `checks.yaml` and `build.yaml` stay quiet on that pull
request until somebody pushes to the branch. The bump job carries the
evidence instead: it runs `tzdata/fetch.sh` on the runner, so the pull
request arrives only after both signatures verified and `zic` compiled
the new data. Merging to main runs the full build, which is liken's
gate either way.

## What the lab measured

The build, on 2026c, in the pinned Alpine image: both signatures
verified as "Good signature from Paul Eggert <eggert@cs.ucla.edu>",
and `zic` produced 604 files in about 20 seconds. A tarball with one
byte appended failed the signature check and stopped the build.

Go reads the result. With `ZONEINFO` pointed at the built tree, a
program resolved four zones that cover a whole hour, a half hour, a
half hour with summer time, and a zone whose winter offset is zero:

```
America/New_York       2026-12-25T12:00:00-05:00
Asia/Kolkata           2026-12-25T12:00:00+05:30
Australia/Lord_Howe    2026-12-25T12:00:00+11:00
Europe/Dublin          2026-12-25T12:00:00Z
```

## The dev-cluster drill

node-1 booted the built image and reported the pin:

```
$ kubectl get machine node-1 -o jsonpath='{.status.version.tzdata}'
2026c
```

The apiserver admits a real zone and still rejects a name that no
zone file carries:

```
$ kubectl apply -f cronjob-america-new-york.yaml
cronjob.batch/zoned created

$ kubectl apply -f cronjob-nowhere-atlantis.yaml
The CronJob "bogus" is invalid: spec.timeZone: Invalid value:
"Nowhere/Atlantis": unknown time zone Nowhere/Atlantis
```

The second result is the one that proves the database is read rather
than the check disabled. It is the message every zone produced before
this milestone.

Admission alone would pass on a stub that accepts any name, so the
drill also proves the offset. Two CronJobs took the same schedule
string, `54 06 * * *`, in two zones 9.5 hours apart. New York reads
10:54 UTC as 06:54, and Kolkata reads it as 16:24, so only one of
them can fire in a two-minute window:

```
NAME            ZONE               LASTSCHEDULE
ny-fires        America/New_York   2026-07-28T10:54:00Z
kolkata-waits   Asia/Kolkata       <none>

NAME                STATUS     COMPLETIONS   DURATION   AGE
ny-fires-29753934   Complete   1/1           5s         25s
```

## What the drill caught

The first boot of the drill reported no `tzdata` at all, with a live
CRD that carried the property and an image whose components record
named the pin. The version reaches the Machine through the facts
tree, and `WriteVersion` and `readVersion` each name their fields by
hand. Both lists had to grow, and the first attempt grew neither. A
field that one list forgets fails quietly: no error is raised
anywhere, and the operator publishes a status that is missing a
component.

`TestEveryVersionFieldSurvivesTheFactsTree` closes that class. It
fills every field of `VersionStatus` with a distinct value through
reflection and demands all of them back, so the test fails the moment
the struct grows a field that the facts lists lack.

## One boot that has no explanation

One `-kernel` boot in the lab refused to mount the system image:

```
liken: switch_root: mounting /liken.sqfs at /liken-boot/system:
invalid argument (continuing on rootfs)
```

The rootfs carries no k3s, so that boot printed "boot complete,
powering off" and stopped. It happened on the boot that followed a
hard kill of the previous guest's QEMU process.

It did not reproduce. Three later boots came up: one at 8 GB on the
same disks, the UEFI smoke drill, and one at the same 4 GB on the
disks that drill had freshly installed. The image itself was sound
throughout, and `unsquashfs -s` read a valid superblock from the same
file on the build host.

So this milestone changes no memory pin. The lab's `MEM_kernel` of
4 GB carries the current image, and the smoke drill's install phase
carries the larger `install.cpio` at that same size. What caused the
one failure is unknown, and it is written down here because a second
occurrence should be read against this one rather than met fresh.
