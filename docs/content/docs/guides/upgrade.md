---
title: Upgrade the fleet
weight: 40
---

# Upgrade the fleet

One edit to the Cluster moves every machine to a new release. Each
machine downloads the release, verifies every artifact, and reboots
when the cluster grants its turn. You do not rebuild the media, and
you do not touch the machines.

## 1. Find the release

The channel at [releases.liken.sh](https://releases.liken.sh/) lists
every release. The cluster also polls the channel:

    kubectl get clusters

The AVAILABLE column shows the latest version that the channel
announces. To make
the cluster poll the channel now, set the `liken.sh/check-releases`
annotation to a new value:

    kubectl annotate cluster --all --overwrite liken.sh/check-releases="$(date -Is)"

The value of the annotation has no meaning. The change of the value is
the request.

An upgrade needs two facts: the version, and the digest of that
release's `release.yaml`. The release's page on GitHub gives both, as
a catalog entry you can copy. To compute the digest yourself:

    curl -fsSL https://releases.liken.sh/<version>/release.yaml | sha256sum

## 2. Catalog the release and set the target

    kubectl edit cluster

Add the release to
[`spec.releases.catalog`](/docs/reference/cluster/#specreleasescatalog),
and point [`spec.version`](/docs/reference/cluster/#spec) at it:

    spec:
      version: "2026.07.20-001"
      releases:
        source: https://releases.liken.sh
        catalog:
          - version: "2026.07.20-001"
            digest: sha256:<hex>

If `spec.version` names no catalog entry, the API refuses the change
while your edit is still open. The digest is the start of
[the trust chain](/docs/reference/release-channel/#the-trust-chain).
The digest names the release document, and the release document names
the artifacts. Each machine checks every downloaded byte against one
or the other.

## 3. The rollout

Each machine that runs a different version:

1. Downloads the release into the boot slot it does not run from, and
   verifies each artifact against the digest chain.
2. Stages the change and asks the cluster for a reboot turn.
3. Cordons and drains its node when the cluster grants the turn. The
   PodDisruptionBudgets of the workloads apply during the drain.
4. Reboots into the new slot one time, as a trial. The trial is a
   success when the OS starts and rejoins the cluster, and the machine
   then boots that slot from then on. If the trial fails, the machine
   returns to the other slot without help:
   [Roll back](/docs/guides/rollback/) describes this.

The cluster grants turns within
[`spec.disruption.maxUnavailable`](/docs/reference/cluster/#specdisruption)
(the default is one machine at a time). Only one leader is down at a
time, whatever the budget says, because the datastore needs a majority
of the leaders.

If a machine's [`rebootPolicy`](/docs/reference/machine/#spec) is
`Manual` (the default), the machine stages the change and then waits
for you. Set `rebootPolicy: Auto` on machines that must take their
reboot turn without an operator.

## 4. Watch the rollout

    kubectl get machines

The LIKEN column changes to the new version one machine at a time, and
the phase of each machine shows its step in the rollout. The phase of
the Cluster shows Updating during the rollout, and Ready when the
rollout is complete.

If a machine with a granted turn does not return, the cluster sets its
`Progressing` condition to `False` with the reason `RolloutStalled`,
and grants no more turns until you examine the machine.
