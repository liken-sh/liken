# git-csi-driver

`git-csi-driver` mounts a git repository as a Kubernetes volume, and
the pod sees a plain directory. The driver pulls new commits of the
branch or tag into a read-only volume. On a writeable volume, it
commits what the application writes and pushes it to the repository.

The driver fits these uses:

- **Data that a repository already holds**, such as a tree of YAML, a set
  of templates, or a static site. Any pod in any namespace can mount it
  as an inline volume, or through a `ReadOnlyMany` claim, and the
  driver keeps it current with the ref.
- **Application configuration.** Many self-hosted applications keep
  their configuration as text in one directory and edit it through
  their own user interface. On this driver that directory is a
  repository, so every change has history. To restore the
  configuration, delete the claim and create a new one on the same
  repository. The application then starts from the last push.

The driver defines no custom resources. A `PersistentVolume` sets the
repository, a `VolumeAttributesClass` sets the commit and push
policy, and a `PersistentVolumeClaim` binds the two. A read-only
volume needs no class, and in its inline form it needs only the `csi`
block in a pod spec.

The manual is at [liken.sh/git](https://liken.sh/git/). The design is
in [`plans/00-design.md`](plans/00-design.md), and the other plans are
in [`plans/`](plans/).

`liken` is at [liken.sh](https://liken.sh/).
