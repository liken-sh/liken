# per-node-csi-driver

`per-node-csi-driver` gives a pod a directory on the node it runs on.
The directory stays on that node after the pod leaves, and a pod that
comes back to that node finds it again. A pod that starts on a
different node gets an empty directory there. So one volume is a set
of copies, one on each node, and the driver never copies data between
them.

That fits a workload that can fill an empty copy by itself:

- **A replicated store.** Several pods each keep a full copy of one
  data set and keep the copies in agreement over the network. A new
  copy catches up from the others. Corrosion is a store of this kind,
  and `library-operator` keeps its catalog in it.
- **A cache.** Pods fill a directory with items they can make again,
  such as decoded art or fetched files. When a copy lacks an item, the
  pod makes it.

Do not use the driver for any other workload, because a file written
on one node is not there on the next.

The driver defines no custom resources and has no controller. You
write a `PersistentVolume` that names the driver and the volume
handle, and a `PersistentVolumeClaim` that binds to it.

The manual is at [liken.sh/per-node](https://liken.sh/per-node/). The
design is in [`plans/00-design.md`](plans/00-design.md), and the other
plans are in [`plans/`](plans/).

## Building and testing

`make test` runs every check that CI runs: `gofmt`, `go vet`, the
tests with the race detector, the coverage gate, the manual's link
check, and the site build. `docker build .` builds the image. `lab/`
boots one `liken` machine in QEMU and runs the drills against a build
of this checkout. Its targets are in `lab/Makefile`.

`liken` is at [liken.sh](https://liken.sh/).
