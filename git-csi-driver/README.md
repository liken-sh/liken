# git-csi-driver

`git-csi-driver` mounts a git repository as a Kubernetes volume. The
pod sees a plain directory with no `.git` in it. A read-only volume
follows a branch or a tag, so a pod in any namespace can read files
from a repository, such as a tree of YAML or a static site. A
writeable volume records the history of an application's
configuration directory. The driver commits what the application
writes and pushes the commits to the repository. To restore the
configuration, bind a new claim to the same repository, and the
application starts from the last push.

The driver clones, fetches, commits, and pushes. It does not create a
repository, a branch, or a credential, and it does not merge. You
give it a deploy key or a token in a `Secret`. When a push is
rejected because the ref moved, the driver rebases the volume's
commits onto the new ref and pushes again, up to three times. If the
rebase conflicts, or the push is still rejected, the driver moves the
volume to the side branch `<ref>.<volumeHandle>`, where
`volumeHandle` is the ID of the volume in its `PersistentVolume`. It
pushes to that branch until a person merges it on the forge.
While pushes fail, the commits stay on the node, and the volume's
`Event`s and metrics report the failure until a push succeeds. If the
node's disk is lost, every commit that was not pushed is lost with
it. A writeable volume is `ReadWriteOncePod`, so only one pod writes
it. Several applications can write to one ref through separate
volumes, each with its own directory of the repository. The driver is
for text that a person also reads as history. The class sets a size
limit, and the driver leaves out each file larger than that limit. Do
not put a database, a cache, or media on it. A submodule appears as
an empty directory, and a Git LFS file appears as its pointer file.

The driver runs on a `liken` cluster, where it stores its
repositories on the pod-storage partition. The design also fits
another cluster whose kubelet uses the standard CSI paths under
`/var/lib/kubelet`. On such a cluster, change the store path
`/var/lib/liken/pod-storage/git-csi` in `deploy/node.yaml`. The
driver's node plugin runs at the priority `system-node-critical`, so
at shutdown the push that runs when each pod stops happens before the
machine's network stops. In `library-operator`, a `Library` of
franchises can read its files from a read-only claim of this driver.

## The objects

The driver defines no custom resources.

- A `PersistentVolume` sets the repository and the ref in
  `volumeAttributes`. For a private repository, it also sets the
  credential's `Secret` in both `nodeStageSecretRef` and
  `nodePublishSecretRef`. The `Secret` contains an SSH key under
  `ssh-privatekey`, or a token under `token`.
- A `VolumeAttributesClass` sets when a writeable volume commits and
  pushes, the commit author, the size limit, and the files to ignore.
  A writeable volume commits nothing until its claim sets a class.
- A `PersistentVolumeClaim` binds a volume and sets its class. A
  read-only volume needs no class. Its inline form needs only the
  `csi` block in a pod spec.

## A read-only volume

```yaml
volumes:
  - name: data
    csi:
      driver: git.liken.sh
      readOnly: true
      volumeAttributes:
        url: https://code.example.com/data/catalog.git
        ref: main
        pull: 5m
```

The manual is at [liken.sh/git](https://liken.sh/git/), and
[`plans/00-design.md`](plans/00-design.md) is the design.

MIT. See [LICENSE](LICENSE).
