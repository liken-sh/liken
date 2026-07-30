---
name: release
description: Cut a liken release and run it on the liken.sh cluster — tagging, the channel publish, the catalog bump, and the reboot watch. Use when I say /release or ask to cut, tag, or ship a liken release.
---

Cut a liken release, publish it on the channel, and move the liken.sh
cluster to it.

Additional context from me: $ARGUMENTS

If that context names a version, a different target commit, or says to
stop after a step, it wins over the defaults below.

**Core principle: the tag is the release act. Everything after it is
verification and one Cluster edit.**

## 1. Check the ground

1. Be on `main`, clean, with every commit pushed.
2. CI must be green on the commit you will tag. If a push just
   happened, watch its runs first. Do not tag a commit CI has not
   proven.

## 2. Pick the version

The format is CalVer: `yyyy.mm.dd-nnn`, and the tag carries a `v`
prefix, for example `v2026.07.30-001`. `nnn` counts releases within
one day, starting at `001`. Run `git tag -l "v$(date +%Y.%m.%d)*"`
and take the next number. Tags are lightweight, not annotated.

## 3. Tag and publish

1. `git tag v<version> <commit>` and `git push origin v<version>`.
2. The tag triggers the `release` workflow: it builds, runs the smoke
   drills, publishes the artifacts and source mirrors to
   `https://releases.liken.sh`, and prints the catalog entry (version
   and the sha256 of `release.yaml`).
3. Watch the workflow to completion with a bounded background watch
   (`timeout 1500 gh run watch <id> --exit-status`). It takes six to
   seven minutes. If it fails, stop and report; do not bump anything.

## 4. Verify the channel

Fetch `https://releases.liken.sh/<version>/release.yaml`, compute its
sha256, and compare it against the digest the workflow printed. The
two must match exactly. This digest goes into the catalog.

## 5. Bump the liken.sh cluster

Edit `liken.sh/cluster.yaml`:

1. Set `spec.version` to the new version.
2. The catalog holds exactly two entries: the release the machine
   runs and the one before it. Add the new entry, drop the oldest. A
   machine has two slots, so a third entry names a release no
   rollback could reach.

Then apply with the repo's wrapper, never the ambient kubeconfig:

    cd liken.sh
    ./kubectl apply -f cluster.yaml
    ./kubectl annotate cluster liken-sh --overwrite \
        liken.sh/check-releases="$(date -Is)"

## 6. Watch the reboot

Watch `machine node-1` with a bounded poll (20-minute timeout, note
each phase transition with a timestamp; the timestamps become the
commit message's measurement). What a normal cycle looks like:

* The machine downloads and stages, then goes unreachable for about
  90 seconds. That is the reboot, not a fault.
* Mid-cycle it can show `Ready,SchedulingDisabled` with the OLD
  version. That is the trial boot before promotion, not a rollback.
* It reports Ready on the new version two to three minutes after the
  edit.

Then confirm health:

1. Every condition on the Machine is True.
2. No pod is outside Running or Completed.
3. `curl https://liken.sh/` and `https://liken.sh/docs/` both return
   200. traefik and the website pod come up about a minute behind the
   API server, so a refused connection right after the reboot means
   nothing; retry before you call it a failure.

If the machine does not reach Ready inside the timeout, stop and
report what the conditions say. Do not roll back on your own.

## 7. Commit the record

Commit `liken.sh/cluster.yaml` with the measurement in the message:
how long the machine was unreachable, and the time from edit to
Ready. Push, and watch CI to green. The `docs` workflow does not
trigger for this commit; that is path filtering, not a failure.

This skill covers the liken.sh cluster only. Other deployments move
by their own arrangements; do not touch them from here.
