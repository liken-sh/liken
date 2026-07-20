# How liken releases are versioned

A liken version is a calendar date and a serial number:

    2026.07.11-001

The version has a four-digit year, a two-digit month, a two-digit day,
a dash, and a three-digit serial number. The serial number starts at
001 and counts up within the day. Every field is zero-padded. This is
not only for appearance: it makes plain string comparison sort
versions in the correct order, so nothing needs to parse a version to
sort a catalog. The grammar is defined once, in machine/versions.go,
and the project enforces it in three places. `liken bundle` refuses to
lay out a release under a malformed name. The releases Makefile checks
the name before it spends time on a build. The Cluster CRD's pattern
rejects a catalog entry or a spec.version typo at admission.

## Why a date and not semver

Semantic versioning encodes a promise: a major version breaks
compatibility, and a minor version does not. liken makes a different
promise on purpose: no release breaks compatibility. Each release is
expected to take over from the release before it. It carries the
machine's deployment layer across slots, reads the on-disk state the
previous release wrote, and keeps going. Compatibility is the code's
job, and a migration ships inside the release that needs it. A semver
major version needs a boundary to mark, and liken has none. Without
that boundary, a semver major number would only invite a false
reading, such as "liken 2.0 must mean kubernetes 2", when liken's
number says nothing about the kernel or Kubernetes version inside.
The date states the one fact the version actually knows: when the
release was cut. What shipped inside a release is recorded where it
belongs: in the release document's `components` section.

A release might someday not support an iterative upgrade path. That
would be a misjudgment, not a plan. If it happens, the fix is a floor
field in the release document: an `upgradesFrom` field that the
machine checks, so it can hold at that version with a clear message.
The fix is not a return to semver.

The serial number 000 never names a published release. The lab uses
serial 000 for the release-shaped channel that it bundles from the
working tree, using the root Makefile's media targets. This stand-in
version is recognizable at a glance, and it sorts below any real
release cut on the same day.

## Releases are immutable

A version names exactly one set of bytes, forever. The project never
rebuilds or republishes a bad release under the same name. The fetch
path treats a document that does not match its catalog digest as
corruption. The remedy is always the same: publish a corrected release
under the next serial number, and point the catalog at that release.
Tags never move, for the same reason.

## Cutting a release

The git tag is the act of release. Everything else follows from it.

1. Pick the next version. Use today's date, and use the serial number
   one past the highest serial already tagged today.

       git tag -l "v$(date +%Y.%m.%d)-*"

2. Tag the commit and push the tag:

       git tag v2026.07.11-001
       git push origin v2026.07.11-001

Pushing the tag hands the rest of the process to CI
(.github/workflows/release.yaml). The workflow rebuilds every liken
binary under the version stamp, bundles the release, and boots the
same tree to a Ready node. Only then does it publish the release to
https://releases.liken.sh/2026.07.11-001/. The digest discipline
exists to rule out a release that someone assembled on a laptop. The
run's summary ends with the catalog entry that a deployment commits to
adopt the release. You can build and inspect the same bundle locally,
without publishing anything:

       make -C releases release VERSION=2026.07.11-001

The tag carries a v prefix, as git release tags conventionally do. The
version itself never carries this prefix. Between releases,
development builds name themselves from the same tags. `git describe`
yields a name such as v2026.07.11-001-5-gabc123, which means five
commits past the release, at that commit. A dev machine reports this
name as status.version.liken. version.mk at the repository root
explains the mechanism.
