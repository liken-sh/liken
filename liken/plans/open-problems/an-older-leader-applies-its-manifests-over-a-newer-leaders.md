# An older leader applies its manifests over a newer leader's

Open problem, recorded on 2026-11-21. A `lab` drill on 2026-11-21
showed it, in the form that it had before milestone 101.

Each leader runs its own k3s deploy controller over the auto-deploy
directory on its own disk. `init` fills `liken`'s subdirectory of that
directory from the booted image at each boot (`seedClusterState` in
`init/k3s.go`). So during a rolling upgrade, the leaders that have not
upgraded have the older release's manifests, and the leaders that
have upgraded have the newer release's. No part of the system decides
which copy the cluster runs. The last leader to apply wins.

After milestone 101, a leader applies its files in a forced pass when
its k3s starts, and after that only when a file changes on its disk.
A leader on a release before milestone 101 on a cluster without
`helm` stays in the forced mode and applies every file on every pass.

During a rollout, an older leader's k3s starts when:

- `init` restarts k3s for a restart-class change to the `Cluster`
  (`init/restart.go`), such as an edit of `spec.runtime`.
- k3s exits, and `init`'s supervisor starts it again.
- The machine reboots outside its reboot turn, such as after a power
  loss or a watchdog reset.
- A leader's first boot of the newer release fails, and the machine
  falls back to its older slot.
- A person rolls the fleet back to the older release.

## What goes backward

When an older leader's forced pass runs, every object in each file
that both releases ship is written back to the older release's copy:

- **The operators.** The `liken-cluster-operator` `Deployment`, the
  `machine-operator` `DaemonSet`, and the `logs` `DaemonSet` go back to
  the older images, because `image/build.sh` writes the release into
  each image tag. Each one starts new pods. The operators' roles go
  back too, so a newer operator pod that still runs loses each
  permission that only the newer release grants.
- **The admission policy.** `disruption-approvals-policy.yaml` goes
  back to the older release's rules.
- **The CRDs.** Each CRD in `liken`'s files goes back to the older
  schema. The API server then drops each field that only the newer
  schema declares, from each object that it returns and from each
  write. A controller that reads an object and writes it back stores
  the object without those fields. `cluster-operator` sees this for
  `status.rebootTurns` and grants no turn until the newer schema is
  served (`schemaPrunedTurns` in `cluster-operator/fleet.go`). The
  `liken.sh/schema-revision` check does not prevent it.
  `newerCRDsOnDisk` in `init/schemarevision.go` compares the image's
  copy with the copy on the same machine's disk, so it protects a
  machine that booted the newer release and then fell back. A leader
  that never booted the newer release has only the older copy.
- **k3s's packaged manifests**, when the two releases ship different
  k3s versions: `coredns`, `local-storage`, `rolebindings`,
  `runtimes`, `ccm`, and `metrics-server` when it is on.

## What is pruned

The deploy controller applies each file through wrangler's `apply`,
with the file's `Addon` as the owner (`watcher.deploy` in k3s's
`pkg/deploy/controller.go`). wrangler lists the objects that carry the
owner's set label and deletes each one that the file does not contain
(`desiredSet.process` in `pkg/apply/desiredset_process.go`). The kinds
that it lists include the kinds of the last successful apply, which
the `Addon`'s `addon.k3s.cattle.io/gvks` annotation records. Both
leaders' applies use the same `Addon`, so:

- An object that the newer release added to a file that both
  releases ship is deleted. If that object is a CRD, the API server
  deletes every object of that kind with it.
- An object that the newer release removed from such a file is
  created again.
- A file that only the newer release ships is not touched, because
  the older leader's directory does not contain it.

## What restores the newer objects

A leader that left the forced mode skips each file whose modification
time on its own disk is unchanged (`listFilesIn` in
`pkg/deploy/controller.go`). It does not read the objects or the
`Addon`, so it does not see that another leader wrote over them. The
older objects stay until a leader on the newer release starts k3s
again. In a rollout, that is usually the older leader's own upgrade
boot, when it takes its turn.

The window can have no end. If a leader falls back to its older slot
and the rollout stops on it, the cluster runs the older operators and
CRDs while every other leader runs the newer release, until a person
acts or a newer leader's k3s restarts.

## Evidence

- k3s v1.37.2+k3s1, `pkg/server/server.go`: `runControllers` calls
  `stageFiles`, which starts the deploy controller, on every server
  and outside leader election.
- `pkg/deploy/controller.go`: `start` sets `force := true` and clears
  it after a pass with no error. `listFilesIn` skips a file whose
  modification time equals the one recorded after its last successful
  apply, unless `force` is set. `deploy` merges the `Addon`'s recorded
  kinds into the apply's kinds and applies with the `Addon` as owner.
- wrangler v3.7.0, `pkg/apply/desiredset_process.go`: `process` lists
  the existing objects of each kind with the set's selector, and
  deletes each one that `compareSets` finds missing from the desired
  set.
- `init/k3s.go`: `seedClusterState` replaces `liken`'s subdirectory
  from the booted image at each boot, and keeps a CRD only when the
  same disk has a higher revision.

## The reproduction

No unit test reproduces this, because the fault is in how the deploy
controllers of two k3s servers interact. The `lab` drill on
2026-11-21 showed the form before milestone 101: two leaders, node-1
on a lab build and node-2 on 2026.11.19-002, both without `helm`. A
sample of the `Addon`s' `spec.checksum` every few seconds, from
00:56:12 to 01:37:44 UTC on 2026-11-22, found two values each for
`cluster-operator`, `clusters-crd`, `logs`, `machine-operator`, and
`machines-crd`, one from each leader's release.

The form after milestone 101 has not run. A drill for it: on a `lab`
fleet with two leaders on a release that has milestone 101, upgrade
node-1 to a later build whose `cluster-operator` image differs. While
node-2 waits for its turn, make node-2 restart k3s, for example with
a restart-class edit of the `Cluster` (`cluster/changes.go` names the
fields). The `liken-cluster-operator` `Deployment` then shows node-2's
image until node-2 takes its turn. Whether `machine-operator` applies
a restart-class change while the machine waits for an update was not
checked.

## What a fix has to decide

The question for the maintainer: during a rollout, whose manifests
should the cluster run? The two answers are the release that
`spec.version` names, which also gives the right answer for a
rollback, or the newest release that any leader has booted.

The possible remedies:

- **Refuse the older write at admission.** A
  `ValidatingAdmissionPolicy` that `liken` ships refuses an update
  that lowers `liken.sh/schema-revision` on a CRD, or lowers a
  release annotation on each of `liken`'s objects, when the request
  comes from k3s's deploy controller. This uses a Kubernetes
  mechanism that `liken` already ships in
  `disruption/manifests/disruption-approvals-policy.yaml`. A refused
  apply fails the older leader's pass, so that
  leader stays in the forced mode and retries every pass until it
  upgrades. Each retry is refused, so nothing changes, but the
  retries load the API server. The policy must be in a file that
  only newer releases ship, so that no older leader prunes it. A
  rollback must also be allowed, so the policy would compare with the
  release that `spec.version` names, not only with the stored object.
- **Restore after the write.** `machine-operator` on each leader
  watches the `Addon`s of `liken`'s files. When a checksum differs
  from its own file's, and this machine runs the release that
  `spec.version` names, it changes the file's modification time, so
  its deploy controller applies the file again in its next pass. The
  older copy is then live for at most one pass. During that pass the
  operators restart and the CRDs drop the newer fields, and an object
  that the older file lacks is deleted and created again. A CRD that
  is deleted takes its objects with it, so this remedy is not enough
  for a CRD that a newer release adds to a shared file.
- **Keep the older leader from seeding.** `init` writes `liken`'s
  manifests only when this machine's release is at least the one that
  the cluster last applied. `init` has no API client when it seeds, so
  it would need that fact on its own disk, for example a record that
  `machine-operator` writes from the `Cluster`. A machine that is
  rolled back then needs the record to name the older release.
- **Apply `liken`'s objects from one place.** `cluster-operator`, or
  a Job that it starts from the release that `spec.version` names,
  applies the operators and CRDs, and `init` seeds only what a new
  cluster needs to start that component. This removes the race, and
  it is the largest change. A cluster whose control plane cannot start
  that component would have no way to recover its operators.

Whichever remedy is chosen, one rule removes the CRD case of the
pruning: give each CRD a file of its own, as the CRDs have now, and
never add a CRD to a file that an older release ships.
