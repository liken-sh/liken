---
title: Add machines
weight: 30
---

# Add machines to a cluster

A cluster grows one declared machine at a time. You describe the new
machine, rebuild the install stick, and boot the machine from it.
The running machines are not touched.

## 1. Describe the new machine

Add a manifest file to your deployment directory, next to the ones
`liken new` wrote. Copy an existing machine's file and change the
name, the addresses, and the storage to match the new hardware. The
comments in the file explain each field.

## 2. If the new machine is a leader

If the new machine will run a control plane, add its name to
[`spec.leaders`](/docs/reference/cluster/#spec) in two places:

* On the live cluster, with `kubectl edit cluster`. The running
  fleet reads this document, and a machine derives its role from it.
* In your `mycluster/cluster.yaml`. The file seeds only a brand-new
  cluster, but keeping it in step means a rebuild produces the
  cluster you actually run.

Make the live edit before you install the machine. Keep an odd
number of leaders, so that the datastore can always form a majority.

## 3. Rebuild the install stick

A changed deployment layer means new install media. Repack the layer
and the stick with [`liken layer`](/docs/reference/cli/#liken-layer)
and [`liken stick`](/docs/reference/cli/#liken-stick), using the
release your fleet runs (`kubectl get clusters` shows it in the
VERSION column):

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick channel/<version> mycluster/deployment.cpio mycluster/install.img

Check the device name before the next command. The command
overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 4. Boot the new machine from the stick

The stick's menu now lists the new machine, with an `install as
<name>` entry and a `wipe and reinstall as <name>` entry. If the
hardware is new to you, boot `liken hardware report` first. It writes
`hardware-report.yaml` to the stick, so you can correct the machine's
disks, interfaces, and drivers before you install. [Install a
cluster](/docs/guides/install/#5-boot-each-machine-from-the-stick)
describes the report and the held console messages in full.

Pick `install as <name>`. The machine installs itself and holds the
console:

    liken: installed to slot A; remove the stick, then press Enter to power off; the next power-on boots from the disk.

Remove the stick, then press Enter. The machine powers off. Power it
back on, and it boots from its own disk.

## 5. Watch it join

    kubectl get machines

The new machine appears and walks to Ready. It also appears in
`kubectl get nodes`. The other machines' rows do not change.
