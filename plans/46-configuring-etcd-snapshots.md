# Configuring etcd snapshots

Milestone 46 — Proposed. It would let a Cluster state the schedule,
the retention, and the off-machine destination for the snapshots that
k3s already takes of the cluster's datastore.

## What already runs

Every liken leader that runs embedded etcd is taking snapshots right
now. init renders no etcd snapshot key into the k3s configuration, so
k3s runs its own defaults: one snapshot every twelve hours, five
retained, written under `/var/lib/rancher/k3s/server/db/snapshots`,
which is on the `clusterState` filesystem.

Two of those defaults are already useful. The third is not a backup. A
copy of the database on the same disk as the database survives a
corrupt datastore and a bad upgrade. It does not survive the disk, the
machine, or the site.

## What an operator can already do

A drill on the dev cluster, against k3s v1.36.2+k3s1, established what
is reachable today without this milestone.

The snapshots are visible. k3s installs the
`etcdsnapshotfiles.k3s.cattle.io` CRD and publishes one cluster-scoped
`ETCDSnapshotFile` for each snapshot, naming the node, the location,
the size, and the creation time. `kubectl get etcdsnapshotfiles` is
the inventory, and it needs nothing from liken.

An on-demand snapshot is also reachable, which is the surprise. `k3s
etcd-snapshot save` is not a node-side act. It calls the supervisor
API, so it runs from a workstation:

```
k3s etcd-snapshot save \
  --etcd-server https://127.0.0.1:16443 \
  --etcd-token "$(cat identity/token)" \
  --name drill
```

That command produced `drill-node-1-1785233987`, 7,917,600 bytes, and
the matching `ETCDSnapshotFile` appeared within six seconds. `ls`,
`delete`, and `prune` take the same two flags. An operator who holds
the server token and a copy of the k3s binary can take, list, and
remove snapshots today.

## What no operator can do

Three things stay out of reach, and each is out of reach for the same
reason: init writes the whole k3s configuration from the Cluster
document, and liken has no shell.

* **The schedule, the retention, and the directory.** These are k3s
  server configuration keys. A key that liken does not render cannot
  be set by anybody.
* **An off-machine destination.** The `etcd-s3` keys are server
  configuration too.
* **A restore.** `--cluster-reset` and `--cluster-reset-restore-path`
  are flags on the server's own start. Only init starts the server.

So this is not an exercise for the reader. The reader has no exercise
available. This milestone takes the first two. The third is its own
work, described at the end.

## The spec surface

```yaml
spec:
  datastore:
    snapshots:
      schedule: "0 */12 * * *"
      retention: 5
      compress: false
      s3:
        endpoint: s3.us-east-1.amazonaws.com
        bucket: liken-backups
        region: us-east-1
        folder: dev-cluster
        retention: 30
        insecure: false
        skipSSLVerify: false
        bucketLookupType: auto
        endpointCA: ""
```

`spec.datastore` names what is snapshotted. The Cluster document does
not describe its datastore anywhere else today: `len(spec.leaders) > 1`
is what selects embedded etcd over sqlite, and that rule is written in
`cluster/cluster.go` rather than in the document. A `datastore`
section is where that choice and its settings belong, and snapshots
are its first entry.

An unset section renders nothing, so k3s keeps its own defaults and
this milestone changes no existing cluster. `schedule: "off"` renders
`etcd-disable-snapshots`, which is the only way to turn the snapshots
off. An unset `s3` object means local snapshots only.

### Any S3-compatible store, not just AWS

k3s uploads snapshots through minio-go, the client SDK whose reference
implementation is MinIO. It builds the client from the declared
endpoint and region, so any object store that speaks the S3 API works:
MinIO, Ceph RGW, or a public cloud that is not AWS. This is why
`endpoint` is a plain host and why the extra fields exist. Four of them
answer the differences between stores and a self-hosted deployment:

* `insecure` speaks plain HTTP, for a store with no TLS.
* `skipSSLVerify` ignores a bad certificate on an HTTPS store.
* `bucketLookupType` names how the client finds the bucket. The
  default (`auto`) and `dns` use virtual-host addressing, which AWS and
  some others need; `path` uses path addressing, which MinIO and most
  self-hosted stores need.
* `endpointCA` carries a PEM bundle for a store behind an internal CA,
  which neither of the two flags above covers.

The k3s key names are `etcd-s3-endpoint`, `etcd-s3-region`,
`etcd-s3-bucket`, `etcd-s3-folder`, `etcd-s3-insecure`,
`etcd-s3-skip-ssl-verify`, `etcd-s3-bucket-lookup-type`, and
`etcd-s3-endpoint-ca`. `etcd-s3-retention` defaults to
`etcd-snapshot-retention` when it is unset, which is what lets the two
retentions (local and off-machine) work without an operator having to
repeat the number.

Every key here is read when the k3s server starts, so an edit
converges by restarting k3s in place. That is the restart tier
milestone 20 built for registry credentials, not a reboot.

### Why this is not a feature slug

The feature vocabulary in `cluster/features.go` is for capabilities a
cluster may not need: bundled components, embedded controllers,
vendored binaries, and workload manifests. Snapshots fit none of those
kinds, and they are already running. A slug would claim the cluster
opts into something that not declaring does not remove.

There is a mechanical reason as well. `ValidateParams` requires every
feature parameter to be a string, so a retention count, a boolean, and
a nested S3 object would all become strings, validated in Go rather
than at the door. A real schema gets CEL rules at admission and a
generated page in the manual.

## Where the credentials live

The Cluster document names the destination. It never carries the
access key or the secret key, because it is the document a deployment
keeps in git.

k3s offers `--etcd-s3-config-secret`, which reads the whole S3
configuration from a Secret in `kube-system`. It exists, and this
plan does not use it. The paragraph below on the config-secret rule
states why.

liken already solved this shape once. The fleet's registry credentials
live in a `registry-credentials` Secret in `liken-system`, the
machine operator reads exactly that one Secret by name, and the
credentials are rendered into the configuration file that consumes
them. Snapshots take the same path: an `etcd-snapshot-credentials`
Secret in `liken-system` holds `accessKey` and `secretKey`, and init
renders them into the k3s configuration drop-in beside the destination
that came from the document.

The keys land in a file on `clusterState`, mode 0600. That is the same
trade `registries.yaml` already makes, and the plan should not pretend
otherwise. A missing Secret on a cluster that declares an `s3` block
is a reported condition, not a silent fall back to local-only.

**The config-secret rule is settled, and it keeps the secret out of
the document.** k3s's `--etcd-s3-config-secret` is all-or-nothing,
not a merge. In `pkg/etcd/s3/s3.go`, `GetClient` loads the whole S3
configuration from the secret only when no other S3 option is set, and
ignores the secret with a warning the moment any option comes from the
command line or a config file. Rendering the endpoint and the bucket
from the Cluster document therefore rules that path out: an operator
would have to move the destination too, and the document would no
longer say where the snapshots go. The keys land beside the
destination in the drop-in instead, for the same reason
`registries.yaml` does: a Secret in `liken-system` holds the
credentials, init renders them, and the document is the single place
that answers where the snapshots go.

### How init renders it

k3s reads its configuration from `config.yaml` and every `*.yaml` in
the `config.yaml.d` directory beside it, in sorted order, merging
later keys over earlier ones. init already writes one drop-in there
(`boot.yaml`). The snapshot keys go in their own drop-in,
`etcd-snapshot.yaml`, owned by init and written at mode 0600. Three
things force the separate file rather than a section in `boot.yaml`:

* `boot.yaml` is written at 0644, and its lines are echoed to the
  console at boot. The S3 keys are credentials, so both the readable
  file and the serial port must not carry them.
* A separate file gives the credentials one author and one mode, the
  same arrangement `registries.yaml` already makes.
* Only leaders render it, and a cluster that retracts the section or a
  machine that is no longer a leader removes it.

The drop-in carries the server-side keys: the schedule, the
retention, the directory, the S3 destination, and the access and
secret keys. `boot.yaml` stays as it is, and no key collides between
the two files.

## What the fleet multiplies

Each leader takes its own snapshot, on its own schedule, and applies
retention to its own files by name prefix. `etcd-s3-retention` is per
node as well. A three-leader cluster with `retention: 30` therefore
keeps ninety objects, not thirty.

The manual has to say this, because an operator sizing a bucket will
read `retention: 30` and plan for thirty. At the measured 7.9 MB of an
idle cluster that is about 700 MB, and a real datastore is larger.

The local cost is smaller than it looks. The conventional
`clusterState` is 6Gi and its floor is 2Gi
(`init/reportlayout.go`), so five snapshots of an idle cluster take
under one percent of the conventional size. It matters at the floor
and it matters when the datastore is large. Naming the retention in
the document is what lets an operator make that call.

## Refusing what cannot work

A cluster with one leader runs sqlite through kine. It has no etcd and
nothing to snapshot. A CEL rule on the Cluster CRD can see
`spec.leaders`, so the section is refused at admission when the
cluster names fewer than two leaders, with a message that says to add
leaders first. The same judgment runs at the file doors, where init
reads a document that never passed an API server.

Followers render none of these keys. They run no datastore.

## What status reports

liken reports the policy it resolved, not the snapshots themselves.
k3s already publishes an `ETCDSnapshotFile` for every snapshot, and a
second inventory in the Machine status would be a second source for
one fact.

The resolved schedule, the resolved retention, and the resolved
destination belong in the facts tree and in the Machine status, beside
the resolved `spec.runtime.k3s` values that already report there. That
answers the question this milestone exists for: what
is this machine actually doing about backups. The manual points at
`kubectl get etcdsnapshotfiles` for the inventory.

## Verification

`cluster` package tests cover the new section: an unset section
renders nothing, `schedule: "off"` renders the disable key, an `s3`
block without a bucket is refused, and a one-leader cluster is refused.

`init/k3s_test.go` covers the drop-in: a leader on a multi-leader
cluster renders the keys, a follower renders none, and a leader on a
one-leader cluster renders none.

On the dev cluster: the `storage` guest already runs stock Debian for
the iscsi and nfs drills, so it can serve a MinIO bucket for this one.
Set `schedule: "*/5 * * * *"` and a bucket, confirm k3s restarts in
place without a reboot, confirm an `ETCDSnapshotFile` appears every
five minutes, confirm objects land in the bucket, and confirm both
retentions prune. Then delete the credentials Secret and confirm the
machine reports the gap instead of quietly writing local snapshots
only.

The MinIO drill also proves the "any S3-compatible store" claim,
because it exercises exactly the fields that self-hosted stores need:
`insecure` (http), `bucketLookupType: path`, and a non-AWS region.
Rerun the same objects against an HTTPS endpoint behind a private CA
to cover `skipSSLVerify` and `endpointCA`. A store that needed only
AWS defaults would be a weaker test of the same sentence.

## The manual

`docs/content/docs/reference/cluster.md` regenerates from the schema,
so the schema's own descriptions are the fix. Write them knowing they
become the page.

A new guide under `docs/content/docs/guides/` covers backups: what k3s
takes without being asked, how to read `kubectl get etcdsnapshotfiles`,
how to take one on demand from a workstation with the k3s binary and
the server token, how to send them to a bucket, and the per-leader
multiplier on retention.

## Not in this milestone

**The restore.** `--cluster-reset --cluster-reset-restore-path=<file>`
runs on one leader, and the other leaders must then be rebuilt against
it. In liken's terms that is a boot mode, close to the install boot
and the report boot: a one-shot instruction that init acts on and then
clears, with the proof and fallback machinery around it. It also has
to handle the etcd membership of the leaders that did not restore. It
is a milestone, not a section. A backup with no restore is not yet a
recovery plan, and this milestone should not be called done in a way
that suggests otherwise.

Whether a restore can read directly from S3 is an open question worth
settling when that milestone starts.

**A `liken snapshot` command.** Milestone 45 gives the CLI a cluster
client. Once it exists, a command that takes an on-demand snapshot
before a risky change is a small addition, and it would remove the
need for an operator to carry the k3s binary. It waits on 45.
