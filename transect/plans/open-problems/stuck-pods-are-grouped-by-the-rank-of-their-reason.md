# Stuck pods are grouped by the rank of their reason

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

`Gather::stuck` in `crates/transect-ground/src/loss/next.rs` keys its
groups by `Stranded::rank()`, and keeps the first `Stranded` value of
each rank as the group's reason. `rank` in
`crates/transect-ground/src/loss/stranded/mod.rs` gives the same number
to every `Disk`, every `Attached`, and every `Device` variant, whatever
their `machine`, `kind`, or `device` fields hold.

So when a loss covers two machines, a workload stuck on a disk of
`alpha` and a workload stuck on a disk of `beta` go into one group. The
sidebar then names only the first machine for both. The same happens to
two `Attached` volumes of different kinds, to two different devices,
and to two `StatefulSet` pods on different machines.

## The reproduction

A test recorded the `Deployment` `web` as stuck on a disk of `alpha`
and the `Deployment` `db` as stuck on a disk of `beta`, and read the
reasons of the groups. It wanted one group for each machine, and
failed with:

```
the stuck groups name [Disk { machine: "alpha" }], want each machine whose disk holds a stuck workload
```

The test chose one line for each machine, which is one of the two
options below.

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When workloads are stuck on two lost machines for the same reason,
should the sidebar give one line for each machine, or one line for
the reason that lists every machine?

Whether the group key is the whole `Stranded` value, which gives one
line for each machine, or the rank with a list of machines in one line.
