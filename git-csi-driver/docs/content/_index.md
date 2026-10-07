---
title: git-csi-driver
---

# `git-csi-driver`

`git-csi-driver` mounts a git repository as a Kubernetes volume on a
[`liken`](https://liken.sh/docs/) cluster. The pod sees a plain
directory. A read-only volume follows its branch or tag as it moves.
On a writeable volume, the driver commits what the application writes
and pushes it to the repository.

It fits two kinds of use:

- **Data that a repository already holds**, such as a tree of YAML, a
  set of templates, or a static site. Any pod in any namespace can
  mount it as an inline volume, or through a `ReadOnlyMany` claim, and
  the driver keeps it current.
- **Application configuration.** Many self-hosted applications keep
  their configuration as text files in one directory and edit it from
  their own user interface. On this driver that directory is a
  repository, so every change has history. A new claim on the same
  repository starts from the last configuration that the driver
  pushed, so you can restore it.

Start here:

* [Install the driver](/docs/guides/install/).
* [Mount a repository read-only](/docs/guides/read-only/).
* [Give an application a repository to write](/docs/guides/writeable/).
* [Give many applications one repository](/docs/guides/one-repository-many-apps/),
  one directory each.

The driver defines no custom resources. A `PersistentVolume` names the
repository, a `VolumeAttributesClass` sets when the driver commits and
pushes, and a `PersistentVolumeClaim` binds the two. A read-only
volume needs no class, and an inline one needs only the `csi` block in
the pod spec.

`git-csi-driver` is one of the extension operators for
[mounting storage](https://liken.sh/docs/concepts/mounting-storage/).

* [The source](https://github.com/liken-sh/liken/tree/main/git-csi-driver)
* [The `liken` manual](https://liken.sh/docs/)
