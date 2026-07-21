---
title: Upgrade the fleet
weight: 40
---

# Upgrade the fleet

One edit to the Cluster moves every machine to a new release. Each
machine downloads the release itself, verifies every byte, and
reboots when the cluster grants its turn. You do not rebuild media,
and you do not touch the machines.

## 1. Find the release

The channel at [releases.liken.sh](https://releases.liken.sh/) lists
every release. The cluster also watches the channel for you:

    kubectl get clusters

The AVAILABLE column shows the latest version the channel announces.
To make the cluster poll the channel now, set
[`spec.releases.check`](/docs/reference/cluster/#specreleases) to any
new value.

An upgrade needs two facts: the version, and the digest of that
release's `release.yaml`. The release's page on GitHub publishes
both, as a ready-made catalog entry. To compute the digest yourself:

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

The API refuses a `spec.version` that names no catalog entry, while
your edit is still open. The digest is the root of
[the trust chain](/docs/reference/release-channel/#the-trust-chain):
it names the release document, the document names the artifacts, and
each machine checks every downloaded byte against one or the other.

## 3. What happens next

Each machine that runs a different version:

1. Downloads the release into the boot slot it is not running from,
   and verifies every artifact against the digest chain.
2. Stages the change and asks the cluster for a reboot turn.
3. Cordons and drains its node when the turn is granted. Workloads'
   own PodDisruptionBudgets hold during the drain.
4. Reboots into the new slot once, as a trial. The boot proves
   itself when the OS comes up and rejoins the cluster, and the
   machine then boots that slot from then on. A failed trial falls
   back on its own: [Roll back](/docs/guides/rollback/) describes it.

The cluster grants turns within
[`spec.disruption.maxUnavailable`](/docs/reference/cluster/#specdisruption)
(default one machine at a time). Only one leader is ever down at
once, whatever the budget says, because the datastore needs a
majority of leaders.

If a machine's [`rebootPolicy`](/docs/reference/machine/#spec) is
`Manual` (the default), it stages the change and then waits for you.
Set `rebootPolicy: Auto` on machines that should take their reboot
turn unattended.

## 4. Watch the rollout

    kubectl get machines

The LIKEN column flips to the new version one machine at a time, and
each machine's phase walks through the rollout. The Cluster's phase
shows Updating while the rollout runs, and Ready when it is done.

If a granted machine never returns, the cluster sets its
`Progressing` condition to `False` with reason `RolloutStalled`, and
stops granting turns until someone looks at the machine.
