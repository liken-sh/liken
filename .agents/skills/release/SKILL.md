---
name: release
description: Cut a liken release — tagging, the publish of every changed component, and the channel verification. Use when I say /release or ask to cut, tag, or ship a liken release.
---

Cut a release of the liken repository. One tag releases every
component whose outputs changed since its own previous release: the OS
to the channel, and the operators and drivers to ghcr.io. The
`releases` skill holds the rules this procedure follows.

Additional context from me: $ARGUMENTS

If that context names a version, a different target commit, or says to
stop after a step, it wins over the defaults below.

**Core principle: the tag is the release act. Everything after it is
verification.**

## 1. Check the ground

1. Be on `main`, clean, with every commit pushed.
2. CI must be green on the commit you will tag. If a push just
   happened, watch its run first. Do not tag a commit CI has not
   proven. A commit that changes only plans starts no run. For such a
   commit, the proof is the newest run on `main` before it, and that
   run's summary also shows what the tag releases, because a plan
   changes no output.
3. `gh variable get PUBLISH -R liken-sh/liken` must print `true`.
   Without it, the tag publishes nothing. Stop and tell me.

## 2. Read what the tag will release

The plan job of the commit's `ci` run has a summary, "A release tag at
this commit". It lists each component, its newest release, whether the
tag releases it, and why. Tell me which components will release, and
say so plainly when the OS is one of them: an OS release reboots every
machine in a fleet that follows releases.

## 3. Pick the version

The format is CalVer: `yyyy.mm.dd-nnn`, and the tag is the bare
version with no prefix, for example `2026.10.02-001`. `nnn` counts
releases within one day, starting at `001`. Run
`git tag -l "$(date +%Y.%m.%d)-*"` and take the next number. Tags are
lightweight, not annotated.

## 4. Tag and publish

1. `git tag <version> <commit>` and `git push origin <version>`.
2. The tag starts a `ci` run. Each component that releases runs its
   checks, builds its images, and publishes. The OS's publish job
   builds the release, runs the UEFI smoke drill, and publishes to
   `https://releases.liken.sh`. The `record` job writes the GitHub
   release for the tag.
3. Watch the run to completion with a bounded background watch
   (`timeout 3600 gh run watch <id> -R liken-sh/liken --exit-status`).
   If it fails, stop and report; do not retag.

## 5. Verify

1. The GitHub release for the tag lists every component and its
   version. Each component that released shows the tag's version.
2. When the OS released, fetch
   `https://releases.liken.sh/<version>/release.yaml`, compute its
   sha256, and compare it with the digest in the GitHub release. The
   two must match exactly. This digest is the catalog entry's pin.

## 6. Report

Give me the version, the components it released, and the catalog entry
when the OS released. Deployments adopt a release by their own
arrangements; do not touch any cluster from here.
