# Stuck pods are grouped by the rank of their reason

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`Gather::stuck` in `crates/transect-ground/src/loss/next.rs` keys its
groups by `Stranded::rank()`, and keeps the first `Stranded` value of
each rank as the group's reason. `rank` in
`crates/transect-ground/src/loss/stranded/mod.rs` gives the same number
to every `Disk`, every `Attached`, and every `Device` variant, whatever
their `machine`, `kind`, or `device` fields hold.

So when a loss covers two machines, a workload stuck on a disk of
`alpha` and a workload stuck on a disk of `beta` go into one group. The
sidebar then names only the first machine for both. The same happens to
two `Attached` volumes of different kinds and to two different devices.

## What would confirm it

A test of the loss view with two lost machines, each with a pod whose
volume is on that machine's own disk. The "what happens next" text
names one machine.

## What a fix has to decide

Whether the group key is the whole `Stranded` value, which gives one
line for each machine, or the rank with a list of machines in one line.
