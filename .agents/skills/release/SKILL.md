---
name: release
description: Cuts a liken release - the checks before the tag, what the tag will release, the version, the tag, the watch of the publish run, a rerun of a job that failed outside the repository, and the verification of the channel. Use when the maintainer says /release or asks to cut, tag, publish, or ship a liken release, and when a release tag's run fails. To roll a release onto a cluster, follow that cluster's own procedure after this one.
---

Cut a release of the `liken` repository. One tag releases every
component whose outputs changed since that component's previous
release. The OS goes to the release channel, and the operators and
drivers go to ghcr.io. The rules that this procedure follows are in
the `releases` skill.

Additional context from the maintainer: $ARGUMENTS

If that context names a version or a different commit to tag, or says
to stop after a step, follow it in place of the defaults below.

**Core principle: pushing the tag is the release. Every step after it
is verification.**

## 1. Check the commit before you tag it

1. Be on `main`, with a clean tree and every commit pushed.
2. CI must be green on the commit you will tag. If a push just
   happened, watch its run first. Do not tag a commit that CI has not
   passed. A commit that changes only plans starts no run. For such a
   commit, use the newest run on `main` before it. That run's summary
   also shows what the tag will release, because a plan is not part of
   any output.
3. `gh variable get PUBLISH -R liken-sh/liken` must print `true`.
   Without it, the tag publishes nothing. Stop and tell the
   maintainer.
4. If the maintainer asked for a soak, the development builds of the
   commit must run on the testbed first, for as long as the maintainer
   said. Do not tag before the soak ends.

## 2. Read what the tag will release

The plan job of the commit's `ci` run prints a table under the heading
"A release tag at this commit". The table lists each component, its
newest release, whether the tag releases it, and why. Read the table
from the job's log. The API returns an empty summary for the check
run.

```sh
gh run view <run id> -R liken-sh/liken --job <plan job id> --log |
  grep -A45 "A release tag at this commit"
```

Tell the maintainer which components will release before you push the
tag. If the OS is one of them, say so plainly, because an OS release
reboots every machine in a fleet that follows releases. If the OS will
release and the request did not say so, wait for an answer.

A tag cannot leave one component out. It releases every component
whose outputs changed, including the OS. If the maintainer wants a
release without one of them, say that a tag cannot do that, and ask.

## 3. Pick the version

The format is CalVer: `yyyy.mm.dd-nnn`. The tag is the bare version
with no prefix, for example `2026.10.02-001`. The date is the
workstation's local date. `nnn` counts the releases within one day,
starting at `001`. Run `git tag -l "$(date +%Y.%m.%d)-*"` and use the
next number. Tags are lightweight, not annotated.

## 4. Tag and publish

1. `git tag <version> <commit>` and `git push origin <version>`.
2. The tag starts a `ci` run. Each component that releases runs its
   checks, builds its images, and publishes. The OS's publish job
   builds the release, runs the UEFI smoke drill, and publishes to
   `https://releases.liken.sh`. The `record` job writes the GitHub
   release for the tag.
3. Watch the run to completion with a bounded background watch
   (`timeout 3600 gh run watch <id> -R liken-sh/liken --exit-status`).
   A run takes about 10 minutes. It takes about 40 when buildroot must
   build the OS's programs because no cached artifact exists.
4. If the run fails, do not retag. Read the failure with
   `gh run view <id> -R liken-sh/liken --log-failed`.
   - If the cause is outside the repository, such as a 5xx from a
     registry or a network fault, report it. Then run the failed jobs
     again on the same tag: `gh run rerun <id> -R liken-sh/liken
     --failed`. A published version never changes, so the rerun
     pushes only what the failed run did not push.
   - If the cause is in the repository, stop and report. The fix is a
     commit on `main` and a new tag with the next serial.

## 5. Verify

1. The GitHub release for the tag lists every component and its
   version. Each component that released shows the tag's version.
2. If the OS released, fetch
   `https://releases.liken.sh/<version>/release.yaml`, compute its
   sha256, and compare it with the digest in the GitHub release. The
   two must match exactly. The catalog entry pins the release by this
   digest.

## 6. Report

Give the maintainer the version, the components it released, and the
catalog entry if the OS released.

A release changes no cluster. Roll a release onto a cluster only when
the maintainer asks, as a separate step after this report, by that
cluster's own procedure.
