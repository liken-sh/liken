---
name: release
description: Cuts a liken release - the checks before the tag, what the tag will release, the version, the tag, the watch of the publish run, a rerun of a job that failed outside the repository, and the verification of the channel. Use when the maintainer says /release or asks to cut, tag, publish, or ship a liken release, and when a release tag's run fails. This is the procedure. For the version scheme, which components a tag releases and why, and the other rules of versions and publishing, the releases skill is the reference. To roll a release onto a cluster, follow that cluster's own procedure after this one.
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

1. Run `git fetch origin --tags`. The commit to tag is the head of
   `origin/main`, which `git rev-parse origin/main` prints. You can
   tag it from a worktree on another branch. Every commit that the
   release should include must already be on `origin/main`.
2. CI must be green on the commit you will tag. Find its run:

   ```sh
   gh run list -R liken-sh/liken --workflow ci.yaml --commit <sha> \
     --json databaseId,status,conclusion
   ```

   If a push just happened, watch its run first. Do not tag a commit
   that CI has not passed. A commit that changes only plans starts no
   run. For such a commit, use the newest run on `main` before it.
   That run's summary also shows what the tag will release, because a
   plan is not part of any output.
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
job=$(gh run view <run id> -R liken-sh/liken --json jobs \
  --jq '.jobs[] | select(.name == "plan") | .databaseId')
gh run view <run id> -R liken-sh/liken --job "$job" --log |
  grep -A45 "A release tag at this commit"
```

Every run prints this heading except the run of a tag. To see what a
tag's own run published, read the table
under "The plan" in that run's plan job, which has a row for each
component with what it publishes, its version, and why.

Tell the maintainer which components will release before you push the
tag. If the OS is one of them, say so plainly, because an OS release
reboots every machine in a fleet that follows releases. If the OS will
release and the request did not say so, wait for an answer.

A tag cannot leave one component out. It releases every component
whose outputs changed, including the OS. If the maintainer wants a
release without one of them, say that a tag cannot do that, and ask.

## 3. Pick the version

The version is `yyyy.mm.dd-nnn`, as "The version scheme" in the
`releases` skill defines it, for example `2026.10.02-001`. Run
`git tag -l "$(date +%Y.%m.%d)-*"` and use the next serial after the
highest one it prints, or `001` when it prints nothing.

## 4. Tag and publish

1. `git tag <version> <commit>` and `git push origin <version>`. The
   tag is lightweight, with no `-a`.
2. The tag starts a `ci` run. Each component that releases runs its
   checks, builds its images, and publishes. The OS's publish job
   builds the release, runs the UEFI smoke drill, and publishes to
   `https://releases.liken.sh`. The `record` job writes the GitHub
   release for the tag.
3. Find the tag's run with
   `gh run list -R liken-sh/liken --workflow ci.yaml --branch <version>`.
   Watch it to completion with a bounded background watch
   (`timeout 3600 gh run watch <id> -R liken-sh/liken --exit-status`).
   The run takes much longer when no cached artifact of the OS's
   programs exists, because buildroot must then build them.
4. While the tag's run is in progress, push nothing to `main`. A new
   push does not cancel the tag's run, but its development builds race
   the release's pushes. After the run ends, a fix can go to `main`.

## 5. Repair a failed run

Do not retag. Read the failure with
`gh run view <id> -R liken-sh/liken --log-failed`, and sort each
failed job into one of these cases:

- **The cause is outside the repository**, such as a 5xx from a
  registry or the channel, or a network fault. Report it, and run the
  job again on the same tag.
- **The cause is in the repository.** Report it. Do not run the job
  again, because it fails the same way. The fix is a commit on `main`
  after the run ends, and the next tag, with the next serial, releases
  the component. A label check that fails with `the labels of the
  pushed <image> differ from the labels the publish wrote` is in this
  case. "What an image says about itself" in the `releases` skill
  explains the check and the labels that it compares.
- **A label check could not read the image.** `checkLabels` in
  `ci/imagemeta.go` fails with `reading the labels of <image>` when
  the registry did not answer, and with `the registry has no image at
  <image>` when the pushed digest was still missing after the retries.
  Each is a fault of the registry, so the cause is outside the
  repository, as in the first case.
- **You cannot tell**, such as a smoke drill that failed once. Report
  what the log shows, and ask.

To run every failed job of the run again, use
`gh run rerun <id> -R liken-sh/liken --failed`. When one job failed
outside the repository and another failed in it, run only the first
one again. Find its job id with
`gh run view <id> -R liken-sh/liken --json jobs --jq '.jobs[] | select(.conclusion == "failure") | [.databaseId, .name] | @tsv'`,
and then run `gh run rerun --job <job id> -R liken-sh/liken`.

A rerun of an image's publish job pushes nothing for an image whose
tag is already on ghcr.io, because a published version never changes.
It checks that image's labels and signs it. It builds and pushes each
image that the failed run did not push.

A rerun of the OS's publish job works only when the failure came
before the upload of `release.yaml`. `liken/releases/publish.sh`
uploads in this order:

1. The corresponding sources, under `sources/`.
2. The artifacts, the signature bundles, and `coverage.html`, under
   `<version>/`.
3. `<version>/release.yaml`.
4. `channel.yaml`, which names the newest release, and
   `favicon.ico`.
5. A fetch of `release.yaml` from the channel, which it compares with
   the built file, and a check that `channel.yaml` names this release.
6. `<version>/notes.md`.
7. The index pages.

The script stops at its start with "release `<version>` is already
published; releases are immutable" when `release.yaml` is on the
channel. To find which side of that upload the failure was on, look
in the log for the upload of `release.yaml` to
`s3://releases.liken.sh/<version>/release.yaml`, or run
`curl -fsI https://releases.liken.sh/<version>/release.yaml`.

- **`release.yaml` is not on the channel.** Run the job again. The
  sources step skips each file that the channel already has, and the
  artifacts are uploaded again.
- **`release.yaml` is on the channel.** The release exists, and a
  `Cluster` can adopt it by its digest, but a rerun stops at the
  guard. The publish of the next release writes `channel.yaml` and
  every index page again, so a failure in step 4 or 7 is repaired by
  the next tag. Until then, `channel.yaml` does not name this release
  when step 4 failed. No step writes `notes.md` of this version again.
  Report which step failed, and ask the maintainer.

## 6. Verify

1. `gh release view <version> -R liken-sh/liken` shows the GitHub
   release for the tag. It lists every component and its version.
   Each component that released shows the tag's version. The `record`
   job writes this release even when a publish job failed, so read it
   again after a rerun.
2. If the OS released, fetch
   `https://releases.liken.sh/<version>/release.yaml`, compute its
   sha256, and compare it with the digest in the GitHub release. The
   two must match exactly. The catalog entry pins the release by this
   digest.
3. To check the signatures too, run
   `liken verify release -digest sha256:<hex> <version>` for the OS
   and `liken verify image ghcr.io/liken-sh/<image>:<version>` for an
   image. The `sbom-and-scans` skill explains what each one checks.

## 7. Report

Give the maintainer the version, the components it released, and the
catalog entry if the OS released.

A release changes no cluster. Roll a release onto a cluster only when
the maintainer asks, as a separate step after this report, by that
cluster's own procedure.
