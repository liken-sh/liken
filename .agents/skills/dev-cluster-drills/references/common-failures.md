# Common failures

Each item starts with what you see, then gives the cause and what to
do.

* **The drill tests an older spec than your working tree, and nothing
  warns you.** `make` inside `dev-cluster/` does not rebuild the OS
  image. `make -C dev-cluster install` and `make -C dev-cluster run`
  boot whatever `dev-cluster/image/install.cpio` already contains.
  Build from `liken/`, or run a smoke drill's root target, which
  builds the artifacts first.
* **`node-1` is back to blank disks.** A smoke drill factory-reset it.
  The smoke drills are the `smoke-*` targets of `liken/Makefile`,
  each run from `liken/`. Each one runs `dev-cluster/smoke.sh`, which
  starts with `rm -rf guests/node-1` in `dev-cluster/`. Each drill
  uses `node-1`, so a UEFI smoke drill wipes a disk installed under
  BIOS, and the reverse. Never run a smoke drill while you work on
  `node-1` by hand. CI runs `smoke-uefi` and `smoke-bios`.
  `smoke-emmc` runs only on a workstation, because QEMU's `emmc`
  device needs QEMU 9.1.
* **A node never becomes Ready after a boot.** A guest from an earlier
  drill is still running. It has the write locks on the qcow2 files,
  and it is still on the multicast cluster segment. The next boot of
  that node fails to open its own disks, and the report says only that
  the node never became Ready, so you look at the machine and not at
  the leftover process. List the guests with
  `pgrep -x qemu-system-x86 -a`, and kill the leftover one by PID.
* **A qcow2 file or a variable store is damaged after a change.**
  `make -C dev-cluster grow-pods NODE=node-2` resizes a qcow2 file,
  and it corrupts the file if the guest is running. The same applies
  to `grow-state`, which enlarges a guest's state disk to
  `STATE_SIZE`. It also applies to `reset-nvram`,
  because QEMU writes the variable store back when the guest exits.
  Resize a disk or reset the variable store only while the machine is
  off.
* **A smoke drill stops with a timeout.** Each kind of boot has its
  own deadline, in seconds. `SMOKE_DEADLINE` limits only the time
  that the disk boot waits for Ready. The attended boots use
  `HOLD_DEADLINE`. `dev-cluster/smoke.sh` sets the default of each,
  and the failure message gives the number of seconds that ran out. A target
  of `dev-cluster/Makefile` can set larger values, as `smoke-emmc`
  does because QEMU's `emmc` device writes slowly. Check which
  deadline applies to the boot that timed out.
* **A conductor or operator fix shows no effect in the rollout that
  delivers it.** System pods run each node's own `:installed` image.
  So during that rollout, the binaries of the old release make every
  decision that the fix changes. Put the fix in lab release N, roll
  the fleet onto N, and then drill N to N+1 to see the fixed code act.
* **Every machine reboots after an edit to the `Cluster`.** Machines
  apply the cluster document by rebooting. A field that the cluster
  operator reads live, such as `spec.disruption`, still marks every
  machine as out of date when it changes. So each edit costs one
  reboot round of the fleet, and a drill that raises the budget and
  then restores it costs two. Allow for one round for each edit.
