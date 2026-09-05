# git-csi-driver

`git-csi-driver` mounts a git repository as a Kubernetes volume. A pod
sees a plain directory. The driver keeps a read-only volume current
with its ref, and for a writeable volume it commits what the
application writes and pushes it to the repository.

Two things want this:

- **Data a repository already holds.** A tree of YAML, a set of
  templates, a static site. Any pod in any namespace mounts it as an
  inline volume and the driver follows the ref.
- **Application configuration.** Many self-hosted applications keep
  their configuration as text in one directory and edit it through
  their own user interface. On this driver that directory is a
  repository with history and a restore path: delete the claim, make a
  new one against the same repository, and the application starts from
  its last push.

The driver defines no custom resources. A `PersistentVolume` names the
repository, a `VolumeAttributesClass` names the commit and push
policy, and a `PersistentVolumeClaim` binds the two. A read-only
volume needs neither, only the `csi` block in a pod spec.

The manual is at [git.liken.sh](https://git.liken.sh/). The design is
[`plans/00-design.md`](plans/00-design.md), and
[`plans/README.md`](plans/README.md) indexes the plans that build it.

`liken` is at [liken.sh](https://liken.sh/).
