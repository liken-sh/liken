# Every node watches every PersistentVolume

## The problem

The node plugin holds one list and watch on `PersistentVolume`s, in
`demand.go`. The watch has no selector, so each node pod lists every
`PersistentVolume` in the cluster, keeps a copy of each one in its
store, and receives every change to any of them. The driver acts only
on the `PersistentVolume`s whose `spec.csi.driver` is
`git.liken.sh`. On a cluster with many volumes of other drivers, each
node pays memory for objects it never reads, and the API server sends
each change once for each node.

The controller has the same cost on a smaller scale. It holds no
watch, by the choice `match.go` states, and each webhook request lists
every `PersistentVolume` to find the ones of the repository that moved.

## Why the node watches them

The watch carries two things:

- The demand annotation `git.liken.sh/pull-requested-at`, which the
  controller or a person writes on a `PersistentVolume` of the driver.
- The claim that a staged volume is bound to. `arming.go` reads it from
  the watch's store by volume handle, and lists `PersistentVolume`s
  from the API server when the store holds no bound copy.

## What is known

- A field selector cannot name the driver. The API server accepts
  `metadata.name` and `metadata.namespace` alone for
  `PersistentVolume`s. Kubernetes v1.36.4 answers
  `"spec.csi.driver" is not a known field selector: only "metadata.name", "metadata.namespace"`.
- A label selector can narrow the watch, but only to a label that the
  driver's `PersistentVolume`s carry. A person creates each static
  `PersistentVolume`, and the driver provisions none, so the driver
  does not control its labels. A `PersistentVolume`'s labels are
  mutable, so a label can be added to an existing one.
- A watch per volume, selected by `metadata.name`, is narrow, and it
  costs one connection to the API server for each volume on the node.
  Plan 10 chose one watch for the node for that reason.
- A transform on the informer can keep only the name and the
  `resourceVersion` of a `PersistentVolume` of another driver. That
  lowers the memory the store holds. It does not lower the watch
  traffic, and the node still decodes each object once.

## What a fix could look like

1. The controller watches every `PersistentVolume` and writes a label,
   for example `git.liken.sh/driver: "true"`, on each one of this
   driver. The node plugins watch with that label selector. One watch
   in the cluster pays the full cost instead of one on each node. This
   reverses the controller's choice to hold no watch.
   A `PersistentVolume` that a node stages before the label arrives is
   found by the list that `arming.go` already falls back to. A demand
   written before the label arrives is read when the labeled object
   enters the node's watch as an add.
2. The guides tell a person to write the label on each
   `PersistentVolume`, and the node watches with the selector. This
   needs no controller, and a volume without the label gets no demand
   and costs one list at each stage.
3. The transform alone, as a first step that lowers memory.

## What is not known

- The cost on a real cluster: the memory of the node plugin's store
  and the watch events each minute, against the number of
  `PersistentVolume`s of other drivers. No measurement exists.
- Whether the controller should write labels on objects a person
  created and may manage through GitOps. A GitOps tool that owns the
  `PersistentVolume` may remove a label it did not declare, and the
  controller would write it again.
