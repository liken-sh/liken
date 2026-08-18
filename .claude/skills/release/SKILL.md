---
name: release
description: Cut a liken release — tagging, the channel publish, and the channel verification. Use when I say /release or ask to cut, tag, or ship a liken release.
---

Cut a liken release and publish it on the channel.

Additional context from me: $ARGUMENTS

If that context names a version, a different target commit, or says to
stop after a step, it wins over the defaults below.

**Core principle: the tag is the release act. Everything after it is
verification.**

## 1. Check the ground

1. Be on `main`, clean, with every commit pushed.
2. CI must be green on the commit you will tag. If a push just
   happened, watch its runs first. Do not tag a commit CI has not
   proven.

## 2. Pick the version

The format is CalVer: `yyyy.mm.dd-nnn`, and the tag is the bare
version with no prefix, for example `2026.08.18-002`. `nnn` counts
releases within one day, starting at `001`. Run
`git tag -l "$(date +%Y.%m.%d)-*"` and take the next number. Tags
are lightweight, not annotated.

Tags before 2026.08.18-002 carry a `v` prefix. On a day that has
tags in both forms, list both to find the highest serial:
`git tag -l "$(date +%Y.%m.%d)-*" "v$(date +%Y.%m.%d)-*"`. On any
other day, the bare listing alone is complete.

## 3. Tag and publish

1. `git tag <version> <commit>` and `git push origin <version>`.
2. The tag triggers the `release` workflow: it builds, runs the smoke
   drills, publishes the artifacts and source mirrors to
   `https://releases.liken.sh`, and prints the catalog entry (version
   and the sha256 of `release.yaml`).
3. Watch the workflow to completion with a bounded background watch
   (`timeout 1500 gh run watch <id> --exit-status`). It takes six to
   seven minutes. If it fails, stop and report; do not retag.

## 4. Verify the channel

Fetch `https://releases.liken.sh/<version>/release.yaml`, compute its
sha256, and compare it against the digest the workflow printed. The
two must match exactly. This digest is the catalog entry's pin.

## 5. Report

Give me the version and the catalog entry. Deployments adopt a
release by their own arrangements; do not touch any cluster from
here.
