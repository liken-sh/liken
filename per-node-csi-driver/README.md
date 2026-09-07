# per-node-csi-driver

`per-node-csi-driver` gives a pod a directory on the node it runs on.
The directory stays on that node when the pod leaves, and the same pod
finds it again if it comes back to that node. A pod that lands on a
different node finds an empty directory there. One volume is a set of
copies, one per node, and the driver keeps none of them in agreement.

Two workloads want this:

- **A replicated store.** Several pods each hold a full copy of one
  data set and keep the copies in agreement over the network. A fresh
  copy catches up from its peers. Corrosion is the first-class case.
- **A named cache.** Pods fill a directory with items they can make
  again, such as decoded art or fetched files. A copy that lacks an
  item makes it.

A workload that is neither has no use for this class. A plain file
written on one node is not on the next, and nothing here carries it
over.

The driver defines no custom resources and has no controller. A
`PersistentVolume` names the driver and the handle, and a
`PersistentVolumeClaim` binds to it.

The manual is at [per-node.liken.sh](https://per-node.liken.sh/). The
design is [`plans/00-design.md`](plans/00-design.md), and
[`plans/README.md`](plans/README.md) indexes the plans that build it.

## Building and testing

`make test` runs every check CI runs: `gofmt`, `go vet`, the race
tests, the coverage gate, the manual's link check, and the site build.
`docker build .` builds the image. `lab/` boots one `liken` machine in
QEMU and runs the drills against this checkout's own build. Its
`Makefile` names the targets.

`liken` is at [liken.sh](https://liken.sh/).
