---
title: git.liken.sh
---

`git-csi-driver` mounts a git repository as a Kubernetes volume. A pod
sees a plain directory. The driver keeps a read-only volume current
with its ref. For a writeable volume, it commits what the application
writes and pushes it to the repository.

Two things want this:

- **Data a repository already holds.** A tree of YAML, a set of
  templates, a static site. Any pod in any namespace mounts it as an
  inline volume, and the driver follows the ref.
- **Application configuration.** Many self-hosted applications keep
  their configuration as text in one directory and edit it through
  their own user interface. On this driver that directory is a
  repository with history and a restore path.

The driver defines no custom resources. A `PersistentVolume` names the
repository, a `VolumeAttributesClass` names the commit and push policy,
and a `PersistentVolumeClaim` binds the two. A read-only volume needs
neither, only the `csi` block in a pod spec.

Start with the [manual](docs/). The design and the plans are in the
[repository](https://github.com/liken-sh/git-csi-driver).
