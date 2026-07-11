# How liken releases are versioned

A liken version is a calendar date and a serial:

    2026.07.11-001

Four-digit year, two-digit month, two-digit day, a dash, and a
three-digit serial that starts at 001 and counts up within the day.
Every field is zero-padded, which is not cosmetic: it makes plain
string comparison the correct ordering, so nothing anywhere needs to
parse a version to sort a catalog. The grammar is defined once, in
machine/versions.go, and enforced in three places: `liken bundle`
refuses to lay out a release under a malformed name, the releases
Makefile checks before spending a build, and the Cluster CRD's
pattern rejects a catalog entry or spec.version typo at admission.

## Why a date and not semver

Semantic versioning encodes a promise: majors break you, minors
don't. liken deliberately makes a different promise — there are no
breaking releases. Every release is expected to take over from the
release before it: carry the machine's deployment layer across slots,
read the on-disk state the previous release wrote, and keep going.
Compatibility is the code's job, and a migration ships inside the
release that needs it. With no boundary for a major to mark, a semver
major would only invite false readings — "liken 2.0 must mean
kubernetes 2" — when liken's number says nothing about the kernel or
Kubernetes inside. The date says the one thing the version truly
knows: when this release was cut. What shipped inside it is recorded
where it belongs, in the release document's `components` section.

If a release ever cannot be crossed to iteratively — a misjudgment,
not a plan — the escape hatch is a floor field in the release
document (an `upgradesFrom` the machine operator would check and hold
on, with a clear message), not a return to semver.

The serial 000 never names a published release. The lab uses it for
the release-shaped channel it bundles from the working tree (the root
Makefile's media targets), so a stand-in is recognizable at a glance
and sorts below any real release cut the same day.

## Releases are immutable

A version names exactly one set of bytes, forever. A bad release is
never rebuilt or republished under the same name — the fetch path
treats a document that doesn't match its catalog digest as corruption,
and the remedy is always the same: publish a corrected release under
the next serial and point the catalog at it. Tags never move for the
same reason.

## Cutting a release

The git tag is the act of release; everything else follows from it.

1. Pick the next version: today's date, and one past the highest
   serial already tagged today.

       git tag -l "v$(date +%Y.%m.%d)-*"

2. Build and bundle it:

       make -C releases release VERSION=2026.07.11-001

   This rebuilds every liken binary under the version stamp and lays
   out releases/dist/2026.07.11-001/ exactly as a web server serves
   it. The report ends with the catalog entry a deployment commits to
   adopt the release.

3. Tag the commit the release was built from and push the tag:

       git tag v2026.07.11-001
       git push origin v2026.07.11-001

The tag carries a v prefix, as git release tags conventionally do;
the version itself never does. Between releases, development builds
name themselves from the same tags: `git describe` yields
v2026.07.11-001-5-gabc123 (five commits past the release, at that
commit), which is what a dev machine reports as status.version.liken
(version.mk at the repo root explains the mechanism).

Publishing the bundle to the public website, and building it in CI
when the tag is pushed rather than on a workstation, belong to the
website milestones (plans/26-releases-on-the-website.md).
