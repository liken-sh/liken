# 103. The next boot runs the Cluster document that the machine runs

Milestone 103. Proposed and built on 2026-11-22. The drills ran on
the `lab` fleet on 2026-11-22, and "Drills" gives the results.

A `Cluster` edit that a k3s restart applies, such as turning a
feature on, can be lost from a machine's store while the machine runs
it. The machine's `proven.yaml` then stays on the older document, and
the next boot runs that older document. For the seconds until the
operator restarts k3s under the current document, k3s removes the
manifests of each feature that the older document turns off. On a
leader that deletes cluster-wide objects: a feature's Helm release,
and the Gateway API CRDs together with every `Gateway` and
`HTTPRoute`. No field of the `Machine` showed that `proven.yaml` was
older than the running document.

## How the document is lost

The machine operator stages the edit, and requests a restart.
`restartState.apply` in `init/restart.go` writes the `attempted`
marker, actuates the features, writes the boot record
`boot/clusterManifest` as `Staged/<hash>`, and then publishes the
document to `/run/liken/cluster.yaml`. The operator's next pass
promotes the staged copy to `proven.yaml` in `settleClusterLifecycle`
(`machine-operator/cluster.go`) when the boot record names it.

A pass reads the facts once, at its start. When that read comes
before init writes the boot record, `settleClusterLifecycle` finds a
hash that does not match the staged copy, and it promotes nothing.
Later in the same pass, `convergeClusterDocument` reads
`/run/liken/cluster.yaml`, which init has published by then. The
pass finds that the machine runs the desired document, and
`convergedWithCleanup` withdraws the staged copy:
`ManifestStore.WithdrawStaged` deletes `staged.yaml` and `attempted`.
No later pass has a staged copy to promote.

On a single-leader cluster with a follower, 3 of 6 restarts of the
leader lost the document this way, and 0 of 4 restarts of the
follower did. In the case observed live, the operator logged the
withdraw 56 ms after init published the document, during the 74 ms
that init took to actuate the features, which was mostly the load of
the `iscsi` and `nfs` kernel modules. The pass that lost the document
was the operator's own pass right after the pass that requested the
restart. The `lab` fleet promoted 8 of 8 restarts, because its guests
load no modules for those features.

## The design

The proven copy follows the document that the machine runs, and the
pass decides from one read of it.

1. **One read of the running document.** The pass reads
   `/run/liken/cluster.yaml` once, right after the facts, and the
   promotion and the convergence both decide from that read. Init
   writes that file only after it has applied the document, so the
   file is the record of what k3s runs or is about to restart under.
2. **Promotion compares the staged copy with the running document.**
   When the bytes of `staged.yaml` equal the bytes that init
   published, the pass promotes the staged copy. The boot record in
   the facts no longer decides the promotion, so a pass whose facts
   are older than the running document still promotes it.
3. **The proven copy is repaired from the running document.** When
   the boot ran a document from the store (its boot record is
   `Staged` or `Proven`), nothing staged equals the running document,
   and `proven.yaml` differs from it, the pass writes the running
   document as `proven.yaml`. This heals a machine that an earlier
   release left in the bad state, and it covers a withdraw that init
   overtakes, as in 4. The pass posts a `ClusterProvenRepaired`
   `Event` for the repair. A boot from the image's seed records the
   seed only when no `proven.yaml` exists, as before. A boot runs the
   seed while a `proven.yaml` exists only when that file does not
   parse, for example after a rollback to a release that does not know
   a field in it. That file is still the newest document that a boot
   proved, and a roll forward reads it again.
4. **A withdraw never removes a document that runs or is being
   applied.** The pass withdraws the staged copy only when the
   desired document is the running one, the staged copy is not the
   running document, and the staged copy's hash is not in `attempted`.
   Init writes `attempted` before it applies the document, so a
   staged copy whose hash is in `attempted` is running or is about to
   run. If the pass reads `attempted` before init writes it and the
   withdraw then runs while init applies the document, the next pass
   repairs `proven.yaml` by 3.
5. **The boot record is the last write of a restart.** Init publishes
   `/run/liken/cluster.yaml` before it writes `boot/clusterManifest`.
   The operator watches the facts tree and not `/run/liken`, so with
   this order the wake that the boot record sends starts a pass that
   reads the published document. The correctness of 2 to 4 does not
   depend on this order. Without it, a pass could still run between
   the two writes and find nothing to promote, and no other event
   would start the next pass.

So whatever order the pass's reads take relative to init's writes,
the pass either reads the older running document, and promotes and
withdraws nothing, or reads the new one, and promotes it.

### What the status shows

`status.nextBoot` records the copy of the `Cluster` document that the
next boot would choose, with the rule of `chooseCluster` in
`init/cluster.go`: a staged copy that parses and is not in
`attempted`, then a proven copy that parses, then the seed.
`clusterManifestSource` and `clusterManifestHash` have the same
values and the same hash as `status.boot.clusterManifestSource` and
`status.boot.clusterManifestHash`, so a person compares the two
fields. The pass computes it after its own writes to the store.

The condition `ClusterProven` is `True` when the next boot would run
the document that the machine runs now:

- `True`, `Current`: the copy that the next boot chooses has the
  bytes of the running document.
- `False`, `ChangeStaged`: the next boot applies a staged document
  that the machine does not run yet. `ClusterConverged` reports how
  it is applied. The phase is `UpdatePending`, the phase that
  `ClusterConverged` already gives for a staged document, so this
  reason does not change the phase of a machine during a rollout.
- `False`, `NextBootDiffers`: the next boot would run a proven copy or
  the seed that is not the running document. This is the fault that
  this milestone repairs, so it lasts only while the repair cannot
  write. The phase is `Degraded`.
- `Unknown`, `FactsIncomplete`: the facts or the running document
  cannot be read. `Unknown`, `StoreUnreadable`: the store cannot be
  read.

A machine with no `Cluster` gets no `ClusterProven` condition, as it
gets no `ClusterConverged`.

## Promotion before the restart

Init publishes the running document and the boot record before the
supervisor restarts k3s, so the operator promotes a restart's
document before k3s has run under it. The `attempted` marker then
protects nothing: if k3s does not start again under the document, the
next boot runs the proven copy, which is that same document. Milestone
20 says that a restart keeps the one-trial rule. That is true only
while the operator promotes after k3s starts again.

This milestone does not change that order. The repair in 3 promotes
on the same evidence as the promotion in 2, so this milestone neither
adds nor removes a trial. To give a restart a real trial, init would
publish the running document and the boot record only after k3s
answers under the new document, and it would need a rule for a k3s
that does not answer. That is a change to init's supervisor and to
what the operator takes as proof, so it is left for its own
milestone.

## Alternatives set aside

- **Read the facts again before the withdraw.** That makes the window
  smaller, and it does not close it, because init can write between
  any two reads.
- **Only guard the withdraw with `attempted`.** That stops the loss in
  the pass that races init, but a machine that an earlier release left
  in the bad state stays there until a person edits the `Cluster`, and
  a withdraw that init overtakes still loses the document.
- **Report the bad state and do not repair it.** A person would need
  a shell on the machine to copy the file, and a `liken` machine has
  none.
- **Promote in init after the restart.** Init cannot tell whether a
  machine joined its cluster under a document, which is why the
  operator promotes (milestone 08).

## Tests

- A pass whose facts are older than the running document promotes
  the staged copy that init applied. On 737f76b13 the same order
  withdrew it, and `proven.yaml` stayed on the older document.
- A staged copy that the machine does not run is withdrawn when the
  `Cluster` returns to the running document, and `proven.yaml` does
  not change.
- A staged copy whose hash is in `attempted` is not withdrawn.
- A machine with nothing staged and an older `proven.yaml` gets the
  running document as `proven.yaml`, and the repair posts its `Event`.
- `status.nextBoot` and `ClusterProven` for a machine that is current,
  that has a change staged, that has an older proven copy, and that
  runs the seed on a memory-backed `machineState`.

## Drills

The drills ran on 2026-11-22 on `node-1` of the `lab` fleet alone,
installed from the published release `2026.11.21-002`, with `iscsi`
and `nfs` turned on, and then upgraded to a lab build of this
milestone, `2026.11.22-903`.

1. **The defect on the published release.** On `2026.11.21-002`,
   `metrics-server` was turned off and on 22 times in 10 minutes. One
   restart lost its document. The operator staged the document and
   requested the restart at 06:10:50.336. Init's console, read by the
   machine's uptime, shows the restart applied between 06:10:50.34 and
   06:10:50.38, and the operator's next pass logged `withdrew the
   staged cluster document, because this boot already runs the
   document that the pass would stage` at 06:10:50.425. After it,
   `/run/liken/cluster.yaml` was `6b9f6bab1782` and `proven.yaml` was
   `10dc368bc9fb`, and `ClusterConverged` was `True`. The next toggle
   overwrote the state before a boot read it.
2. **The upgrade.** The upgrade from `2026.11.21-002` to
   `2026.11.22-903` finished with no fallback: `VersionConverged` was
   `True`, `status.boot.systemRejection` was absent, the machine ran
   slot B, and the `Machine` CRD had `liken.sh/schema-revision` 16.
   `status.nextBoot` named the proven copy, and `ClusterProven` was
   `True` with `Current`.
3. **A restart's document becomes proven, and the reboot runs it.**
   Turning `metrics-server` off applied with one restart, and the
   operator logged `the staged cluster document is now proven
   (6b9f6bab1782)` 5 seconds after it requested the restart. The
   reboot that followed booted `Proven` `6b9f6bab1782`.
4. **The withdraw that the design keeps.** With `rebootPolicy:
   Manual` to hold the restart, turning `metrics-server` on staged
   `10dc368bc9fb`. `ClusterConverged` was `RestartPending`, and
   `ClusterProven` was `False` with `ChangeStaged`, with
   `status.nextBoot` on the staged copy and the phase `UpdatePending`.
   Turning it off again withdrew the staged copy, and `proven.yaml`
   stayed `6b9f6bab1782`.
5. **The repair.** From a privileged pod, `proven.yaml` was replaced
   with the older document `10dc368bc9fb` and made immutable with
   `chattr +i`. The pass failed to write it (`rename ...: operation
   not permitted`), and the status showed `ClusterProven` `False` with
   `NextBootDiffers`, `status.nextBoot` on `10dc368bc9fb`, and the
   phase `Degraded`, while `ClusterConverged` stayed `True`. After
   `chattr -i`, the next pass wrote the running document, posted
   `ClusterProvenRepaired`, and `ClusterProven` returned to `True`,
   with no reboot.
6. **The same toggles on the lab build.** On `2026.11.22-903`,
   `metrics-server` was turned off and on 16 times in 10 minutes, and
   no restart lost its document. In 7 of those restarts the operator's
   next pass, the pass that withdrew the document on
   `2026.11.21-002`, promoted it 48 to 110 ms after the restart was
   requested.

The lab cannot make the losing order happen on demand: it happened
once in 22 restarts on the published release, so 16 restarts without
a loss do not prove the fix by themselves. The unit tests in "Tests"
are the proof of the order.

Both releases also staged the previous document for about 100 ms
right after some `Cluster` edits, and withdrew it in the next pass,
for example at 06:23:00 on `2026.11.22-903`. The staged copy never
ran, so this milestone's withdraw rule allows that withdraw. Its cause
is not known: the passes read the `Cluster` from the watch's copy and
from the API server, and one of the reads looks older than the edit.
