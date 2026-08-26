---
title: Add machines
weight: 30
---

# Add machines to a cluster

You add one declared machine at a time. Describe the new machine, build
the install stick again, and boot the machine from it. This procedure
makes no changes to the running machines.

## 1. Describe the new machine

Add a manifest file to your deployment directory, with the manifests
that `liken new` wrote. Copy an existing machine's file and change the
name, the addresses, and the storage for the new hardware. The comments
in the file explain each field.

If an interface takes its address from DHCP, reserve a fixed address
for the machine on your DHCP server. k3s reads the node's address when
it starts. An address that changes later does not take effect until
the machine restarts, and the results in between are unpredictable.

### A machine on a wireless network

If the machine joins over wifi, give its interface a
[`wireless`](/docs/reference/machine/#specnetworkinterfaceswireless)
entry. The SSID and the security type are the only wireless facts in
the manifest:

```yaml
interfaces:
  - name: wlan0
    wireless:
      ssid: mynetwork
    address: 10.0.0.30/24
    gateway: 10.0.0.1
```

The passphrase is never in the manifest, because the manifest travels
on sticks and in git. Write it to a file in your identity directory,
named exactly like the network, as one line:

    mkdir -p mycluster/identity/psk
    echo 'the-passphrase' > mycluster/identity/psk/mynetwork

`liken layer` packs this file onto the stick beside the join token,
and the machine reads it at boot. If a manifest names a network that
has no passphrase file, `liken layer` refuses and names the file to
create. A network with `security: open` needs no file.

If the machine's only interface is the radio and the passphrase is
wrong, the machine holds its boot and says so on the console and in
`kubectl get machines`. A machine with a working wired interface
joins anyway and reports the wifi failure as a condition.

## 2. If the new machine is a leader

If the new machine will run a control plane, add its name to
[`spec.leaders`](/docs/reference/cluster/#spec) in two places:

* On the live cluster, with `kubectl edit cluster`. The machines read
  this document, and each machine gets its role from it.
* In your `mycluster/cluster.yaml`. The file applies only to a new
  cluster. But if you keep it correct, a new build gives the cluster
  that you run.

Make the live edit before you install the machine. Keep an odd number
of leaders, so that the datastore can always make a majority.

## 3. Rebuild the install stick

A change to the deployment layer needs new install media. Use the
release that your fleet runs. `kubectl get clusters` shows it in the
VERSION column. Pack the layer and the stick again with
[`liken layer`](/docs/reference/cli/#liken-layer) and
[`liken stick`](/docs/reference/cli/#liken-stick):

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick channel/<version> mycluster/deployment.cpio mycluster/install.img

Check the device name before the next command. The command
overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

## 4. Boot the new machine from the stick

The stick's menu now lists the new machine, with an `install as
<name>` entry and a `wipe and reinstall as <name>` entry. If you do not
know this hardware, boot `liken hardware report` first. It writes
`hardware-report.yaml` to the stick, so you can correct the machine's
disks, interfaces, and drivers before you install. [Install a
cluster](/docs/guides/install/#5-boot-each-machine-from-the-stick)
describes the report and the held console messages fully.

Select `install as <name>`. The machine installs itself and holds the
console:

    liken: installed to slot A; remove the stick, then press Enter to power off; the next power-on boots from the disk.

Remove the stick, then press Enter. The machine powers off. Power it on
again, and it boots from its own disk.

## 5. Watch it join

    kubectl get machines

The new machine appears, then it becomes Ready. It also appears in
`kubectl get nodes`. The rows of the other machines do not change.
