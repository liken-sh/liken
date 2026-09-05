# 02, The lab

Proposed. Low fidelity.

## The problem

The driver cannot be installed on a real cluster until a person has
seen it work, so every drill needs a cluster that exists only for this
repository. The lab is one `liken` machine in QEMU, booted from the
public release channel, and a git forge on the host that the machine
can reach.

## The design

- `lab/` holds a `Cluster` manifest named `lab` with one leader,
  `node-1`, and one `Machine` manifest for it, in the shape of
  `liken`'s own single-node `gitops-cluster/`. `releases.source`
  points at `https://releases.liken.sh`.
- A `Makefile` fetches the latest release with that release's own
  `liken` toolkit, mints the identity, layers the manifests, composes
  install media, installs onto blank qcow2 disks, boots from disk with
  the API port forwarded to the host, and mints a kubeconfig. The
  targets, disk names, firmware paths, network devices, and MAC scheme
  copy `liken`'s `dev-cluster/Makefile`.
- The forge is `git daemon` on the host, serving a directory of bare
  repositories with `receive-pack` enabled. The guest reaches it at
  `git://10.0.2.2:9418/<name>.git` over the user-mode network. A target
  creates a bare repository from a directory of fixture files.
- A `smoke` target installs, boots, waits for `Ready` with a deadline,
  applies `deploy/` pinned to a development build, and waits for the
  `DaemonSet`. Drill scripts for later plans build on it.
- The identity directory, the guest disks, and the fetched channel are
  ignored by git.

## Considered and set aside

- **gitea in the lab cluster.** It is the shape the house cluster has,
  but it costs a database and a second image on a 1 GiB guest, and
  `git daemon` exercises the same driver code.
- **A stock distribution with k3s.** Lighter, but the driver's store
  lives on `liken`'s cluster-state partition and its shutdown push
  depends on `liken`'s stop order. The lab has to be the real target.

## Proof

- `make -C lab smoke` ends with one `Ready` node and a `Ready`
  `DaemonSet` inside the deadline, from a clean checkout on `vega`.
- A pod in the lab clones from the host forge and pushes to it.
