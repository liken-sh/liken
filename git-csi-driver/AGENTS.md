# Working on git-csi-driver

`git-csi-driver` is a Go CSI driver. `README.md` says what it is for
and describes the objects that configure a volume.

## How it runs

The driver name is `git.liken.sh`, and one image runs the node plugin
and the controller plugin.

The node plugin runs on every node and owns the store, its directory
on that node. The store contains one bare repository for each URL and
one work tree for each writeable volume. A work tree is the checkout
that a pod mounts. In CSI, to stage a volume is to prepare it on a
node, and to publish it is to mount it into one pod. The plugin
stages and publishes each volume, and it fetches, commits, and
pushes. It watches each writeable work tree with inotify, and runs
`git status` on a timer as a backstop. It also watches the
`PersistentVolume`s and claims of its volumes, and reads from them
each claim's class and each request for a pull. When a
`PersistentVolume` with the `Delete` reclaim policy is deleted, the
plugin removes its work tree.

The controller plugin validates the parameters of a
`VolumeAttributesClass`. It does not implement `CreateVolume`. The
same process receives push webhooks from a forge and verifies each one
with its `Secret`. It then writes the annotation
`git.liken.sh/pull-requested-at` on each read-only `PersistentVolume`
that follows the pushed ref. The node plugin of the volume's node
reads the annotation and fetches.

The kubelet reads a volume's credential `Secret` and sends its data in
the stage or publish call. The node plugin keeps the credential in
memory for the life of the volume. If the plugin restarts, it has no
credential until the next publish call.

## Design

The driver is the only process that commits. The repository is in the
node's store, and the pod gets a bind mount of the work tree with no
`.git` inside it. While the pod runs, the driver moves the tree of a
read-only volume to each new commit of its ref, unless the volume's
`pull` is `never`. Upstream changes reach a writeable volume's work
tree at stage, before the pod starts. While a pod uses the work tree,
only the application changes it. The exception is a rebase that the driver
applies after a rejected push. The driver never merges and never
loses a commit. It either fast-forwards or rebases the local commits
onto upstream. When a rebase conflicts, the driver aborts it and
moves the volume to its side branch. A volume is only as durable as
its last push, so every unpublish pushes, whatever the class says. A
writeable volume commits and pushes nothing until its claim names a
class. This gives a person time to write the ignore list before a
commit can include a secret.

A credential is on the node's disk only while one git command runs.
A token reaches git through a credential helper, not the command
line, so it does not appear in the process list. Nothing in this
directory is installed on a real cluster until a person has seen it
work, so every drill runs in `lab/`. `lab/` boots one `liken` machine
in QEMU and serves a git repository to it from the host.
