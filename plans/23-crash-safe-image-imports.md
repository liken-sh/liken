# Crash-safe image imports

Milestone 23 — Designed, not started

liken machines are meant to be killed without ceremony. The whole
design assumes it: the OS is an initramfs that rebuilds itself from
two files, the documents that matter live in a staged/proven
lifecycle, and a power cut is supposed to cost a machine nothing but
the reboot. Milestone 17's lab work found the one place that promise
doesn't hold, and it found it the hard way, twice: a machine killed
in the wrong few seconds after boot can be left permanently unable to
run its own operator.

The flaw is in the layer we don't own. At startup, k3s's embedded
containerd imports every OCI tarball in agent/images, and for each
one it extracts the layers into snapshot directories on clusterState
and records the unpack in its metadata database. Those two writes are
not crash-ordered: kill the machine between the database commit and
the extracted files reaching disk, and the metadata says unpacked
while the files are torn. Containerd trusts its own record, so the
same digest is never unpacked again, on any later boot, no matter how
many times the tarball is re-imported. Every container started from
that image dies with `exec format error`, forever. When the torn
image is the machine operator's, the machine has lost the program
that would have reported the problem, and when it happens on several
machines at once (a whole fleet restarting during a storm, say), the
rollout conductor rightly freezes and the fleet wedges.

The fix is not inside containerd. Its unpack can't be made
transactional from outside, and reaching into its metadata database
to delete individual snapshots would couple init to another program's
private schema, which some k3s upgrade would eventually break. But
the OS already has a vocabulary for exactly this problem, and it
applies cleanly: put the *imports* through the same
staged/proven/fallback lifecycle that documents and system releases
already ride.

The design:

* Init keeps a **proven imports record** on machineState: the sha256
  of each image tarball whose unpack this disk has proven. Unpacking
  only ever happens when a digest is new (an upgrade, a feature's
  first image), so most boots compare equal and pay nothing.
* When the tarballs on a booting image differ from the proven record,
  init writes an **on-trial marker** beside the record before it
  starts k3s. This is the same move staging.go makes for documents: a
  change is on trial until something proves it.
* The **proof** is the OS pods actually running. The machine operator
  is positioned to see that (it is one of them, and it can see its
  node's others), and when the node's OS pods are all healthy it
  promotes the record to the current digests and clears the marker,
  the same way its own existence promotes a staged cluster document.
* A boot that finds the marker **still standing** knows the previous
  boot died before its imports proved out, and containerd's store is
  not to be trusted: init deletes the agent/containerd directory
  entirely before starting k3s. Every OS image unpacks fresh from the
  tarballs the initramfs carries; workload images re-pull from their
  registries. The machine comes up clean instead of crashlooping
  forever.

The wipe is deliberately coarse. The surgical alternative (delete
only the torn snapshots) requires editing containerd's database, and
precision that depends on someone else's internals is worse than
bluntness that depends only on our own state. The cost is bounded
and rare: a node re-pulls its workload images only when it died
inside the vulnerable window, which is minutes long and only open on
boots that had something new to unpack. A machine that loses power
at 3am on day forty has no marker standing and boots normally.

Two things to research when this milestone runs, either of which
narrows the window without changing the design above. Containerd has
grown fsync-on-unpack behavior in places (the CRI plugin's
image_pull_with_sync_fs, for one), and k3s lets the OS ship a
containerd configuration template, so if any of those options cover
the startup import path, one config line makes the tear itself
unlikely, with the trial machinery as the backstop for everything
else that can dirty a store. And the proof condition needs a precise
definition: the set of OS pods a node expects varies with the
cluster's declared features, and the operator should prove exactly
that set, not a hardcoded list.

The lab proof: hard-kill a machine mid-unpack (a QEMU kill a second
or two after k3s starts, with a fresh image to import) and watch the
next boot detect the standing marker, wipe, re-unpack, and come up
healthy with no human touch. Then the negative: a hard kill on a
settled machine leaves no marker and the next boot wipes nothing.
The failure this milestone exists for was produced by exactly that
first drill, unintentionally, during milestone 17; producing it on
purpose and watching the machine heal itself is the milestone
banking.
