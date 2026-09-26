# Change a machine's storage layout without a stick

Open design question, low priority. A machine's storage roles
are partitions, and their sizes are set at installation. The only way
to change the split, or to shrink any role, is `wipe and reinstall`
from a stick, one machine at a time, with a person at each keyboard.

## Current behavior

The manifest declares each storage role and its size, and the
installation lays those roles down as partitions. After that, the
machine reports the sizes it booted with, and the staging rule in the
machine-operator accepts a spec change only when every role grows.
Anything else is `StagingRejected`.

The install guide says to reinstall the machine with the new layout.
That works, but the order of steps is awkward. The person edits the manifest,
builds a stick, drains the machine, powers it off, deletes the `Node`,
walks the stick over, selects `wipe and reinstall as <name>`, waits for
the boot, and only then can edit the `Machine` resource to the layout the
machine now reports. Until that last edit, the cluster's document and
the machine disagree.

This is not a bug. Each step has a reason, and the disk really is
reinstalled. The problem is that the first layout is an estimate, and a
fleet's needs grow past its estimates. When that happens, a person must
visit every machine to correct the layout.

## Why layouts need to change

A role sized for the current workloads fills when a new kind of
workload arrives. Another role often has unused space, but moving the
boundary between the two roles needs a reinstall. So people make the
role they expect to grow larger than it needs to be. On the next
machine, the first estimate is then wrong in the other direction.

## Constraints on a remedy

- The manifest stays the source of the layout. Whatever changes the
  layout must leave the manifest, the `Machine` resource, and the disk in
  agreement, with no edit after boot that the person has to remember.
- A reinstallation erases cluster state and every volume on the
  machine. That is acceptable for a machine whose data rebuilds from
  peers, but it must stay a deliberate choice. The person acknowledges
  it once, and no other confirmation is needed.
- The conductor changes one machine at a time, as it already does for
  reboots.

The best design needs no stick and no keyboard. A person declares the
new layout, acknowledges that this machine's disks will be erased, and
the machine does its own `wipe and reinstall` from the cluster, then
rejoins under the same name. The design questions are what that
reinstall boots from, and how the machine shows that it has the new
layout before it takes workloads again.
