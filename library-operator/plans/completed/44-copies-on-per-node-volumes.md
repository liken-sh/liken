# Copies on per-node volumes

Plan 44. Every claim the operator writes can bind to a `per-node`
volume, the class `per-node-csi-driver` serves. A per-node volume has
one copy on every node a pod visits, and it pins no pod to a node. The
durable copies of a store share one claim, a screen keeps its two
claims, and a Library's Jobs land on any node. The heal from plan 43's
follow-up shrinks to a pod delete.

## The problem

A `local-path` claim pins its pod to the node that holds the
directory. Every copy of a store therefore holds a claim of its own,
the operator deletes a stranded copy's claim before it can stand
elsewhere, and a screen's claim that the scheduler refuses past a
grace is deleted and made again. All of that is the operator working
around a volume that follows a node. `per-node-csi-driver` gives a
volume that follows the pod instead, and the operator can stop.

The driver provisions nothing. Whoever wants a per-node volume writes
the PersistentVolume up front and a claim that names it. The operator
is that writer for its own claims.

## The contract

**The operator learns the class from the StorageClass.** Before it
writes a claim, the operator reads the claim's StorageClass. A class
whose `provisioner` is `per-node.liken.sh` is a per-node class. The
operator never matches the class by name.

**A per-node claim gets a volume first.** For a claim on a per-node
class the operator writes a PersistentVolume named
`<namespace>-<claim>`, with `spec.csi.driver: per-node.liken.sh`,
`volumeHandle` equal to the volume's name, the class, `ReadWriteMany`,
the claim's size as capacity, `Retain`, a `claimRef` to the claim, and
the operator's labels naming the namespace and the claim. Then it
writes the claim with `ReadWriteMany` and `spec.volumeName` set to the
volume, so the claim binds to that volume and waits on no provisioner.
A claim on any other class is written as today, `ReadWriteOnce`, with
no volume.

**One claim per store on a per-node class.** The catalog copies of a
`Catalog` share one claim, `<catalog>-catalog`, and the progress
copies share `<catalog>-progress`. Every copy mounts the same claim
and gets its own node's directory. `spec.storage.claimName` names a
claim a person made in place of `<catalog>-catalog`, and the operator
writes no volume for it.

**More than one copy needs a per-node class.** A `Catalog` that asks
for `replicas` above one on a class that is not per-node is Blocked,
with a message that says so. One copy on any class is unchanged,
with one claim of its own name.

**The heal deletes the pod alone.** A store copy stranded on a node
NotReady past the grace is force-deleted, and the next pass stands it
again on another node. On a per-node class the claim is shared and
stays. On any other class the operator deletes the claim after the
pod, as today.

**A screen's claims and a Library's claims take the same path.** The
screen catalog claim, the art claim, the scanner claim, and the
enrichment claim each go through the same writer, so each is
`ReadWriteMany` on a volume when its class is per-node and unchanged
otherwise. A screen claim on a per-node class is never refused by the
scheduler for its volume, so the unschedulable-past-grace delete does
not fire for it.

**The operator removes the volumes it wrote.** A PersistentVolume is
cluster-scoped, so no owner reference can tie it to a `Catalog`, a
`Library`, or a `Player`. Instead the operator lists the volumes that
carry its labels once per pass. When a claim goes, its volume turns
`Released`, and the operator deletes a `Released` volume of its own on
the next pass. The driver then removes every copy on every node.

**RBAC.** `storageclasses` get; `persistentvolumes` list, create, and
delete beside the get it has.

## What changes for a cluster

A `Catalog` that moves a class to `per-node` needs the driver
installed. Its old claims are not migrated: delete the numbered
`local-path` claims and pods by hand after the shared claim's copies
are up, as with the copy rename. The catalog re-syncs from the
screens and scanners; the progress store loses whatever the old copies
alone held.

## Not in this plan

- A caught-up signal for a fresh copy. Readiness still means the
  containers are ready.
- A Deployment in place of the numbered copies. Copy 0 alone carries
  the reporter or recorder, so the copies are not one template yet.
- A size budget on the shared partition.
