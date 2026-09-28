# 15, Credentials return by republish

Built on 2026-09-28. The drill on liken-1 is still owed, and the last
section of this plan lists it. This plan closes the open problem
"Credentials after a restart".

## The problem

A volume's `Secret` reaches the driver only inside a kubelet call. The
driver keeps the credential in memory for the life of the volume, and
writes it to the node's disk only for the length of one git invocation.
When the driver's node pod restarts, each volume resumes from its
record with no credential. Every fetch and every push that needs the
credential fails until the kubelet calls the driver again with the
`Secret`.

The open problem named writeable volumes and their pushes. The fault is
wider. A read-only volume of a private repository, inline or a claim,
loses its fetch the same way. On a home cluster, all five volumes of
private repositories stopped fetching after the 2026-09-28 driver
upgrade. Each of the five is a read-only claim whose
`PersistentVolume` names `nodeStageSecretRef` alone, so each needs the
migration below before this design covers it.

## The design

### The kubelet sends the Secret again

The CSIDriver sets `requiresRepublish: true`. The kubelet then calls
NodePublishVolume again for every mounted volume on each pod sync. For
each call it reads the volume's `nodePublishSecretRef` `Secret` again
and passes the data in the call (Kubernetes v1.36.4,
`pkg/volume/csi/csi_mounter.go`, `SetUpAt`). The field is mutable on
an existing CSIDriver, so an apply of `deploy/` updates it in place.

A restarted driver takes each credential back from the next of those
calls. The driver needs no grant to read `Secret`s, and no key reaches
the node's disk beyond one git invocation. Both designs the open
problem weighed are set aside below.

### A repeated publish does nothing but take the credential

A NodePublishVolume for a target the volume is already published at
mounts nothing and runs no git. It parses the call, takes the
credential the call carries, and answers success. A first publish
behaves as it did before this plan. The rule covers all three kinds:

- An inline volume is found by its handle, at the same target.
- A read-only claim is found by the target in its set of targets.
- A writeable volume is found by its handle, at the same target.

The credential is read and replaced under the volume's lock, because a
republish replaces it while the fetch loop and the watch read it. The
driver writes nothing and posts nothing when the call's credential is
the one the volume holds. A repeated publish with no `Secret` keeps
the credential the volume holds.

### A staged volume names one Secret twice

A staged volume receives its `Secret` in two calls. The stage carries
`nodeStageSecretRef`, and the stage fetches with it. Each publish
carries `nodePublishSecretRef`, and only that one returns after a
restart. So a `PersistentVolume`, writeable or a read-only claim,
names one `Secret` in both references. The guides and the examples say
so.

The driver provisions no volume, so no `StorageClass` parameter such as
`csi.storage.k8s.io/node-publish-secret-name` applies. A person writes
both references on the static `PersistentVolume`.

A first publish whose `Secret` differs from the credential the volume
holds is refused with `InvalidArgument`, and the message names both
references and says the credential came from the stage or from an
earlier publish. The driver does not choose one of two credentials in
silence. A repeated publish is not compared: it carries the `Secret`
the kubelet read a moment ago, which after a rotation is the one to
use. A new pod's first publish on a read-only claim is compared with
the credential of the last republish, so a rotation does not refuse
it. A new pod whose publish arrives after a rotation, and before the
first republish of the pods already on the node, is refused. The
kubelet retries the publish, and a retry after that republish
succeeds.

One case does not heal. A writeable volume whose `Secret` rotates
between its stage and its first publish holds the old credential, and
no republish comes before the first publish works. Every retry is
refused until the pod is deleted, which unstages the volume, and the
next stage takes the new `Secret`. The window is the seconds between
the two calls.

### A volume that no publish can serve says so

A `PersistentVolume` made before this plan may name
`nodeStageSecretRef` and no `nodePublishSecretRef`. Its `csi` block is
immutable, so it cannot gain the reference. At the first publish of
such a volume, the driver posts `GitVolumeNoPublishSecret` on the pod
and the claim, and writes a log line. A resumed volume that waits for
its credential and receives a republish with no `Secret` posts the
same Event, and its report and `git_csi_volume_abnormal` say what the
`PersistentVolume` lacks. Each of these happens once for each stage and
once for each restart, not on every republish.

The guides give the migration: stop the pods, delete the claim and the
`PersistentVolume`, create both again with the same `volumeHandle` and
both references, and start the pods. A writeable volume pushes at the
unpublish, and the node keeps its work tree, so a stage on the same
node starts from it. The migration comes before the upgrade that
carries this plan, because that upgrade restarts the driver. After the
restart, the unpublish of such a volume pushes nothing. It posts
`GitVolumePushFailed` on the claim and writes a warning, and its
commits stay in the node's work tree until a stage on the same node.

A resumed volume's report puts the missing credential ahead of a failed
fetch or push from before the restart, so the older failure does not
hide the reason.

### The resume waits for the credential and then works at once

A volume whose record says it had a credential resumes with the report
"the driver restarted and holds no credential for this volume until
the kubelet publishes it again". The resume skips the pull a restart
makes for every other following volume, because that pull fails
without the credential. The record keeps saying the volume needs a
credential, so a second restart before the republish also waits.

The republish that carries the credential wakes the volume, and
nothing else does:

- A read-only volume's repository loop runs a pass at once. The wake
  has a channel of its own on the loop, so it passes
  `--demand-min-interval`, and it counts no demand. The pass wants the
  volume, so a fetch that fails retries on the backoff a failed demand
  takes.
- A writeable volume's watch pushes what the tree committed while the
  credential was missing. It commits nothing, because the class's
  quiesce decides when a write is finished.

The wake happens only when the volume waited for a credential. A
rotation reaches the next timed fetch or push with no extra fetch.

### A volume that waits fetches and pushes nothing

A volume that waits for its credential runs no fetch and no push, and
starts none for another volume:

- The volumes of one URL share one repository and one loop, and each
  volume fetches with its own credential. A pass passes over a volume
  that waits, so a pass for another volume of the repository leaves its
  tree where it is. Each volume waits for its own republish. A fetch
  with another volume's credential is not used, because two volumes of
  one URL can name different `Secret`s, and the one a volume names is
  the one it has the right to use.
- A demand for a volume that waits is dropped, and wakes no pass. This
  covers a demand stamped less than a minute before the restart, which
  the first read of `PersistentVolume`s after the restart reads again.
  The republish that returns the credential runs a pass, and that pass
  answers every demand stamped before it.
- A writeable volume that waits commits what the class allows and
  keeps the commits in its work tree. The republish pushes them.

### A resumed tree names its commit

A read-only volume's tree holds no git directory. Each placement writes
the commit it placed to a file `commit` beside the tree, with one
rename, and a resumed volume reads it. So the report names the commit
the tree holds from the resume on, and a volume that pulls never names
it after a restart. A tree placed by a driver without this file has no
record of its commit, and its report names no commit until the next
placement.

### The cost

With `requiresRepublish`, the kubelet reads each volume's
`nodePublishSecretRef` `Secret` from the API server and calls
NodePublishVolume once per volume on each pod sync. The kubelet's clock
sets that rate, and the driver adds no timer. On the driver's side, a
repeated call is a parse and a comparison under one lock, with no git
and no mount. A volume with no `nodePublishSecretRef` costs the kubelet
no `Secret` read.

The driver's interceptor writes one log line at the info level for
every call it answers, except a repeated publish that changed nothing.
The handler marks that call quiet, and the interceptor leaves its line
out. A repeated publish that replaces the credential, or returns one a
restart lost, logs that and its call line, and every error is logged.
The kubelet may ask for the node's capabilities around a publish, and
those calls keep their lines. The drill counts the lines.

## Measurements

`BenchmarkARepeatPublish` calls NodePublishVolume in the process, with
no gRPC, for a published volume of each kind. On an Intel Core Ultra 7
165H with Go 1.27.1, three runs each:

| Kind | Time per call | Memory per call |
|---|---|---|
| Inline | 382 to 399 ns | 263 B, 3 allocations |
| Read-only claim | 406 to 414 ns | 262 B, 3 allocations |
| Writeable | 391 to 400 ns | 262 B, 3 allocations |

The gRPC call over the node's socket and the kubelet's own work cost
more than this. The drill below measures the rate and the API reads on
a cluster.

## Tests

- `TestARepeatPublishTakesTheFreshCredentialAndDoesNothingElse`: for
  each kind, a repeated publish with the same `Secret`, a rotated
  `Secret`, and no `Secret`. The volume holds the right credential, and
  a `git` on `PATH` that logs each invocation, with the mount fake,
  shows no git command and no mount.
- `TestAFirstPublishRefusesASecretThatDiffersFromTheStage`: for a
  read-only claim and a writeable volume, a first publish with the
  stage's `Secret`, a different one, a `Secret` at the publish alone,
  and a `Secret` at the stage alone, which posts
  `GitVolumeNoPublishSecret`.
- `TestAResumedVolumeFetchesAtTheRepublishThatReturnsItsCredential`:
  an inline volume with `pull: 1h` and a claim with `pull: on-demand`
  resume after a new commit upstream, and the tree takes it within
  seconds of the republish.
- `TestAResumedWriteableVolumePushesAtTheRepublish`: a commit made
  before the restart is not on the remote until the republish, and is
  on it within seconds after, with the sweep set to an hour.
- `TestAPrivateRepositoryFetchesAgainAtTheRepublishAfterARestart`: the
  same over SSH to an sshd of the test's own, where the fetch fails
  without the key.
- `TestARepeatPublishLogsOnlyWhatItChanged`: for each kind, a
  repeated publish with the same `Secret`, or with no publish `Secret`
  on a volume that already said so, writes no log line, and a rotated
  `Secret` writes its change and the call line.
  `TestARepeatPublishThatReturnsACredentialIsLogged` checks the line a
  republish writes when it returns a credential after a restart.
  `TestTheCallLogKeepsEveryErrorAndEveryUnmarkedCall` checks that a
  quiet call that fails is still logged.
- `TestAVolumeThatWaitsForItsCredentialFetchesNothingWhenAnotherVolumeOfItsRepositoryFetches`:
  two inline volumes of one URL resume, and the republish of one
  moves its tree and leaves the other's tree and report as they were,
  until the other's own republish.
- `TestADemandForAVolumeThatWaitsForItsCredentialWakesNoPass` and
  `TestAWriteableVolumeThatWaitsForItsCredentialPushesNothing`: a
  demand wakes no pass, and a timed push and an unpublish push leave
  the remote as it was. The unpublish push posts `GitVolumePushFailed`.
- `TestAResumedReadOnlyVolumeReportsTheCommitItsTreeHolds`: after the
  republish, the report of an inline volume and a claim names the
  commit, with no fetch.
- `TestAResumedVolumeWithNoPublishSecretSaysWhatItNeeds`: after a
  failed fetch, three republishes with no `Secret` post one Event, the
  report names
  `nodePublishSecretRef`, and the record still says the volume needs a
  credential.

Removing the wake fails the three restart tests on their deadlines.
Coverage stays at 100 percent.

## Considered and set aside

- **Read the `Secret` through the API at resume.** The node's
  `ClusterRole` would need `get` on every `Secret` in the cluster,
  because RBAC cannot narrow the rule to the ones a volume names. The
  kubelet already holds the narrow grant: the node authorizer lets it
  read only the `Secret`s the pods on its node reference.
- **Keep the credential in the store.** It survives a restart with no
  grant, and it keeps a private key on the node's disk for the life of
  the volume instead of one git invocation.
- **Compare the `Secret` names through the node's `PersistentVolume`
  cache.** The names would tell a rotation from a second `Secret`. The
  comparison of data at a first publish needs no API server, and the
  rotation case costs at most one retried publish.

## The drill still owed

Run it on liken-1 after the build that carries this plan rolls there.
Use no real forge and no repository of anyone's home cluster.

1. **A private repository in the cluster.** In a namespace `drill-15`,
   generate an ed25519 host key and an ed25519 deploy key on the
   workstation with `ssh-keygen`. Run a `Deployment` of a pinned Alpine
   image that installs `openssh-server` and `git`, makes the user `git`
   with `git-shell` as its shell, and puts the deploy key's public half
   in that user's `authorized_keys`. An init container creates a bare
   repository `/srv/git/drill.git` with one commit on `main`. A
   `Service` `git-server` serves port 22. The Secret `drill-deploy-key`
   holds `ssh-privatekey` and `known_hosts`, a line for
   `git-server.drill-15.svc.cluster.local` with the host key. Check
   that a fetch with no key fails: the repository needs the credential.
2. **The volumes.** A pod with an inline volume of
   `ssh://git@git-server.drill-15.svc.cluster.local/srv/git/drill.git`,
   `pull: 1h`, and `nodePublishSecretRef: drill-deploy-key`. A
   writeable `PersistentVolume` of the same URL with both references
   naming `drill-deploy-key`, its claim, a class with
   `push.quiesce: 10s`, and a writer pod.
3. **The republish interval.** Record the times of the
   NodePublishVolume calls from
   `git_csi_reconcile_duration_seconds_count{kind="NodePublishVolume"}`,
   read every few seconds for ten minutes. Report the interval, its
   spread, and whether the kubelet calls publish for a volume whose pod
   is idle.
4. **A restart.** Commit to the in-cluster repository, and write a file
   in the writer pod and wait for its commit. Delete the driver's node
   pod on the volumes' node. Time from the delete until the inline
   volume's tree holds the new commit, and until the writer's commit is
   on the in-cluster repository. Restart no consumer pod. Check that
   the report says the volume waits for its credential until the
   republish, and that nothing fetches or pushes without it.
5. **A rotation.** Put a second deploy key in `authorized_keys`, move
   `drill-deploy-key` to it, and remove the first key. Check that the
   next fetch and the next push work with the second key, with no pod
   restart.
6. **No publish Secret.** A second writeable `PersistentVolume` with
   `nodeStageSecretRef` alone. Check the `GitVolumeNoPublishSecret`
   Event at its first publish, then delete the driver's node pod and
   check the volume's report names `nodePublishSecretRef` after the
   republish. Then follow the guide's migration step and check a push
   works after the next restart.
7. **Differing Secrets.** A third writeable `PersistentVolume` whose
   `nodeStageSecretRef` and `nodePublishSecretRef` name two `Secret`s
   with different keys. Check that the pod stays in `ContainerCreating`
   with `GitVolumeRefused` naming both references.
8. **The cost per minute.** Over ten minutes with the three volumes
   mounted, read `apiserver_request_total{resource="secrets",verb="get"}`
   from the API server's `/metrics`, and the publish count from the
   driver's metrics. Report the `Secret` reads and the publish calls
   per volume per minute, the node plugin's log lines per minute by
   `rpc`, and the node plugin's CPU from `kubectl top` beside a run
   with `requiresRepublish: false`.
