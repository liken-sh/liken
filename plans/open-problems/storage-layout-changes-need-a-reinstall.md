# Change a machine's storage layout without a stick

Open design question. Review priority: low. A machine's storage roles
are partitions, and their sizes are set at installation. The only way
to change the split, or to shrink any role, is `wipe and reinstall`
from a stick, one machine at a time, with a person at each keyboard.

## Current behavior

The manifest declares each storage role and its size, and the
installation lays those roles down as partitions. After that, the
machine reports the sizes it booted with, and the staging rule in the
machine-operator accepts a spec change only when every role grows.
Anything else is `StagingRejected`.

The install guide's answer is a reinstallation with the new layout.
That works, but the order is awkward. The person edits the manifest,
builds a stick, drains the machine, powers it off, deletes the Node,
walks the stick over, selects `wipe and reinstall as <name>`, waits for
the boot, and only then can edit the Machine resource to the layout the
machine now reports. Until that last edit, the cluster's document and
the machine disagree.

Nothing here is a bug. Every step exists for a reason, and a
reinstallation is the honest name for what happens to the disk. The
problem is that the first layout is a guess, and a fleet outgrows its
guesses. When it does, the cost of correcting one is a visit to every
machine.

## Why it comes up

A role sized for the workloads of the day fills when a new kind of
workload arrives, and the space it needs is often right next door in
another role with room to spare. Moving that boundary is a reinstall.
So people over-provision the role they expect to grow, which means the
first guess is wrong in the other direction on the next machine.

## Constraints on a remedy

- The manifest stays the source. Whatever changes the layout must leave
  the manifest, the Machine resource, and the disk agreeing, without a
  post-boot edit the person has to remember.
- A reinstallation erases cluster state and every volume on the
  machine. That is fine for a machine whose data rebuilds from peers,
  and it must stay a conscious choice. The person acknowledges it once,
  and that acknowledgement is the whole confirmation.
- One machine at a time, under the rollout conductor, the way a reboot
  already is.

The ideal version needs no stick and no keyboard: a person declares the
new layout, acknowledges that this machine's disks will be erased, and
the machine does its own `wipe and reinstall` from the cluster, then
rejoins under the same name. What that reinstall boots from, and how
the machine proves it has the new layout before it takes workloads
again, are the design questions.
