---
name: upgrade
description: "Move every machine in a fleet to a new release with one edit to the Cluster, and watch the rollout. Use when a new release is on the channel and the fleet should take it."
---

This skill is the guide at https://liken.sh/docs/guides/upgrade/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Upgrade the fleet

One edit to the Cluster moves every machine to a new release. Each
machine downloads the release, verifies every artifact, and reboots
when the cluster grants its turn. You do not rebuild the media, and
you do not touch the machines.

## 1. Find the release

The channel at [releases.liken.sh](https://releases.liken.sh/) lists
every release. The cluster also polls the channel:

    kubectl get clusters

The AVAILABLE column shows the newest version that the channel
lists. To make the cluster poll the channel now, set the
`liken.sh/check-releases` annotation to a new value:

    kubectl annotate cluster --all --overwrite liken.sh/check-releases="$(date -Is)"

The value can be anything. Each new value makes the cluster poll.

An upgrade needs two facts: the version, and the digest of that
release's `release.yaml`. The release's page on GitHub shows both, as
a catalog entry that you can copy. To compute the digest yourself:

    curl -fsSL https://releases.liken.sh/<version>/release.yaml | sha256sum

Before you add the entry to the catalog, check that `liken`'s CI
signed the release with that digest. [Verify a release or an
image](https://liken.sh/docs/guides/verify/) explains each failure:

    liken verify release -digest sha256:<hex> <version>

## 2. Catalog the release and set the target

    kubectl edit cluster

Add the release to
[`spec.releases.catalog`](https://liken.sh/docs/reference/cluster/#specreleasescatalog),
and set [`spec.version`](https://liken.sh/docs/reference/cluster/#spec--version)
to its version:

    spec:
      version: "2026.10.10-001"
      releases:
        source: https://releases.liken.sh
        catalog:
          - version: "2026.10.10-001"
            digest: sha256:<hex>

If `spec.version` is not the version of a catalog entry, the API
server refuses the edit when you save it. The digest starts
[the trust chain](https://liken.sh/docs/reference/release-channel/#the-trust-chain):
the release document lists the digest of each artifact, so each
machine checks every byte that it downloads.

## 3. The rollout

Each machine that runs a different version:

1. Downloads the release into the boot slot it does not run from, and
   verifies each artifact against the digest chain.
2. Stages the change and requests a reboot turn from the cluster, with
   a `Disruption` named `<machine>.machine-operator`.
3. Cordons and drains its node when the cluster grants the turn. The
   drain respects the PodDisruptionBudgets of the workloads. It removes
   a pod that holds a DRA claim before the pod that runs the claim's
   driver, because the kubelet calls the driver to unprepare the
   claim's devices when that pod stops.
4. Reboots into the new slot once, as a trial. The trial succeeds when
   the OS starts and rejoins the cluster, and the machine then boots
   from that slot. If the trial fails, the machine returns to the other
   slot on its own. [Roll back](https://liken.sh/docs/guides/rollback/) describes this.

The cluster grants turns within the limits of the
[`MachineDisruptionBudget`s](https://liken.sh/docs/reference/machinedisruptionbudget/).
The budget `default` allows one machine at a time unless you change
it. Whatever the budgets allow, only one leader is down at a time,
because the datastore needs a majority of the leaders.
[The reboot turn](https://liken.sh/docs/concepts/how-liken-works/#the-reboot-turn)
explains the requests and the budgets.

When another change already waits for a reboot on a machine whose
release is still downloading, the cluster waits for the download
before it grants the turn, so both changes share one drain and one
reboot. A download that failed and waits to retry does not delay the
turn, so a release server that is down does not hold up other
changes.

`cluster-operator` lists each machine that has a turn in the Cluster's
[`status.rebootTurns`](https://liken.sh/docs/reference/cluster/#statusrebootturns),
and each entry counts against the budgets. It writes the entry before
it grants the turn, and the API server refuses that write when another
write to the Cluster came first. So when a second replica of
`cluster-operator` takes over from one that stalled, the two replicas
cannot give the last place in a budget to two different machines. An
entry leaves the list after the machine's turn ends.

When `cluster-operator` starts, for example after the machine that ran
it loses power, it grants no turns for 40 seconds. It has not yet
received a heartbeat from each machine, so a machine that is already
down still shows its last status. After 40 seconds with no heartbeat,
that machine's status is `Lost`, and it counts against the budget. The
message of the Cluster's `Progressing` condition gives the time that
granting resumes.

If a machine's
[`rebootPolicy`](https://liken.sh/docs/reference/machine/#spec--rebootpolicy) is
`Manual` (the default), the machine stages the change and waits for
you. Its STATUS is `UpdatePending`, its `VersionConverged` condition
has the reason `RebootPending`, and the APPROVED column of
`kubectl get disruptions` is `False` for its request. Approve the
reboot with
[`liken approve-reboot`](https://liken.sh/docs/reference/cli/#liken-approve-reboot):

    liken approve-reboot mycluster <machine>

The machine then waits for its turn under the same budgets as an
`Auto` machine, and it drains before it reboots. Set
`rebootPolicy: Auto` on machines that should reboot without your
approval.

You can change `spec.version` again before every machine has
rebooted. A machine that staged the earlier target unstages it, stops
any download in progress, and downloads the new target into the same
slot. If the machine reboots before that download is verified, it
tries neither release and boots its current one. Before each trial,
the machine also checks the slot's release document and every artifact
against the staged release, and it skips the trial when they differ.

## 4. Watch the rollout

    kubectl get machines

The LIKEN column changes to the new version one machine at a time, and
the STATUS column shows each machine's step in the rollout:

* `Downloading`: the machine downloads the release into its other
  slot. Its workloads keep running, so the download does not count
  against the budgets.
* `UpdatePending`: the release is staged, and the machine waits for
  its reboot turn, or for you to approve it when its `rebootPolicy` is
  `Manual`.
* `Updating`: the machine drains, reboots into the new slot, and
  runs the trial of the release.

The STATUS of the Cluster is `Updating` during the rollout and `Ready`
when it is complete.

The TURNS column of the wide listing shows each machine that has a
reboot turn:

    kubectl get clusters -o wide

    NAME   STATUS     MACHINES   VERSION          NEWEST           AVAILABLE        LEADERS                        ENDPOINT   AGE   TURNS
    lab    Updating   4/5        2026.10.12-001   2026.10.12-001   2026.10.12-001   ["node-1","node-2","node-3"]   ...        9d    node-4

Each machine's `Disruption` shows the state of its turn. The GRANTED
column is `True` when the machine has its turn:

    kubectl get disruptions

When a request waits, the message of its `Granted` condition says why,
for example that a budget has no room, that another leader is down, or
that a release is still downloading.

    kubectl describe disruption <machine>.machine-operator

If a machine with a granted turn does not come back within 10 minutes,
the Cluster's `Progressing` condition becomes `False` with the reason
`RolloutStalled`. The cluster grants no more turns until that machine
comes back or you remove it.
