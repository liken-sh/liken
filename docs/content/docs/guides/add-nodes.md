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

The stick's menu now lists the new machine. Pick its entry. The
machine installs itself and powers off. Unplug the stick and power
the machine back on.

## 5. Watch it join

    kubectl get machines

The new machine appears and walks to Ready. It also appears in
`kubectl get nodes`. The other machines' rows do not change.
