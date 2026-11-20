---
name: add-nodes
description: "Add one declared machine to a running liken cluster: write its manifest, rebuild the install stick, boot the machine from it, and watch it join. Use when a cluster needs another node."
---

This skill is the guide at https://liken.sh/docs/guides/add-nodes/, emitted for agents. Before the first command that reaches a cluster, run `kubectl config current-context` and confirm that it names the cluster the person means. A `liken` command that takes a deployment directory reaches that directory's cluster, whatever the context is.

# Add machines to a cluster

You add one machine at a time. Describe the new machine, build the
install stick again, and boot the machine from it. A new follower does
not change the running machines. A new leader reboots every machine,
one at a time, as step 2 explains.

## 1. Describe the new machine

Add a manifest file to your deployment directory, with the manifests
that `liken new` wrote. Copy an existing machine's file and change the
name, the addresses, and the storage for the new hardware. The comments
in the file describe each field.

If an interface takes its address from DHCP, reserve a fixed address
for the machine on your DHCP server. k3s reads the node's address only
when it starts, so when the lease changes, the node keeps advertising
its old address until k3s starts again.

### A machine on a wireless network

If the machine joins over wifi, give its interface a
[`wireless`](https://liken.sh/docs/reference/machine/#specnetworkinterfaceswireless)
entry. The manifest has only the SSID and the security type, which is
`wpa-psk` by default or `open`:

```yaml
interfaces:
  - name: wlan0
    wireless:
      ssid: mynetwork
    address: 10.0.0.30/24
    gateway: 10.0.0.1
```

Do not put the passphrase in the manifest, because copies of the
manifest are on sticks and in git. Write it as one line to a file in
your identity directory, with the same name as the network:

    mkdir -p mycluster/identity/psk
    echo 'the-passphrase' > mycluster/identity/psk/mynetwork

`liken layer` packs this file onto the stick beside the join token,
and the machine reads it at boot. If a manifest has a network with no
passphrase file, `liken layer` fails and prints the path of the file
to create. A network with `security: open` needs no file.

A machine with a working wired interface does not wait for its radio.
It boots on the wired interface and joins the wifi in the background.
Until the join finishes, the `WirelessJoined` condition is `False` with
the reason `Joining`, and the machine stays `Ready`. Such a machine
reports a wrong passphrase in that condition. If the radio is the
machine's only interface and the passphrase is wrong, the machine
stops booting and waits, and it reports the failure on the console and
in `kubectl get machines`. In both cases the supplicant keeps retrying,
and the machine configures the interface's address when the radio
joins.

A network can have several access points with the same SSID. The
machine scans for a stronger one every 30 seconds while the signal is
below -70 dBm, and once an hour above that, and moves when a scan
finds one. An access point controller can also tell the radio to
move, with an 802.11v BSS transition request, and the radio moves. When the
access points support 802.11r fast transition, the radio uses it, so a
move does not repeat the full handshake. To see which access point a
machine is associated with, read
[`status.network.interfaces[].wireless`](https://liken.sh/docs/reference/machine/#statusnetworkinterfaceswireless).
Its `bssid`, `frequency`, and `band` identify the access point, and
`kubectl describe machine` lists a `Roamed` or `Associated` `Event`
for each move. When the radio loses its access point, `WirelessJoined`
is `False` with the reason `Joining` until the supplicant joins again,
and the message gives the 802.11 reason code. Some access point
controllers refuse a client for a minute or two after they disconnect
it. During that time the machine tries every access point of the
network at least every 30 seconds, and the console shows each access
point that refused it, with the 802.11 status code that it sent.
The signal level is the metric `liken_wireless_signal_dbm`
([Metrics](https://liken.sh/docs/reference/metrics/#the-radios)).

## 2. If the new machine is a leader

If the new machine will run a control plane, add its name to
[`spec.leaders`](https://liken.sh/docs/reference/cluster/#spec) in two places:

* On the live cluster, with `kubectl edit cluster`. Each machine reads
  the live Cluster to find its role.
* In your `mycluster/cluster.yaml`. This file applies only when you
  create a new cluster, but keep it current so that the install media
  you build later matches the cluster that you run.

Make the live edit before you install the machine. Keep an odd number
of leaders, so that the datastore can always reach a majority.

A change to `spec.leaders` reboots every machine, one at a time,
because each machine reads the leaders only when it boots. A follower
uses the leaders as its time sources, and each leader joins the
datastore at the address of the founding leader, the first name in the
list. When a
machine's `rebootPolicy` is `Manual`, its `ClusterConverged` condition
reports the reason `RebootPending`, and the APPROVED column of
`kubectl get disruptions` is `False` for its request. Approve each one
with
[`liken approve-reboot`](https://liken.sh/docs/reference/cli/#liken-approve-reboot), as
in [Upgrade the fleet](https://liken.sh/docs/guides/upgrade/).

## 3. Rebuild the install stick

A change to the deployment layer needs new install media. Use the
release that your fleet runs, which `kubectl get clusters` shows in the
VERSION column. Pack the layer and the stick again with
[`liken layer`](https://liken.sh/docs/reference/cli/#liken-layer) and
[`liken stick`](https://liken.sh/docs/reference/cli/#liken-stick):

    ./liken layer mycluster mycluster/identity mycluster/deployment.cpio
    ./liken stick channel/<version> mycluster/deployment.cpio mycluster/install.img

Check the device name before the next command. The command
overwrites the device.

    sudo dd if=mycluster/install.img of=/dev/YOUR-STICK bs=4M oflag=direct status=progress

[Write an install stick](https://liken.sh/docs/guides/write-an-install-stick/) checks
the copy before a machine boots from it.

## 4. Boot the new machine from the stick

The stick's menu now lists the new machine, with an `install as
<name>` entry and a `wipe and reinstall as <name>` entry. If this is
hardware you have not used with `liken` before, boot
`liken hardware report` first. It writes
`hardware-report.yaml` to the stick, so you can correct the machine's
disks, interfaces, and drivers before you install. [Install a
cluster](https://liken.sh/docs/guides/install/#5-boot-each-machine-from-the-stick)
describes the report and each console message that waits for you to
press Enter.

Select `install as <name>`. The machine installs itself and waits at the
console:

    liken: installed to slot A. Remove the stick, then press Enter to power off. The next power-on boots from the disk.

Remove the stick, then press Enter. The machine powers off. Power it on
again, and it boots from its own disk.

## 5. Watch it join

    kubectl get machines

The new machine appears in the list, and its STATUS becomes `Ready`. It also
appears in `kubectl get nodes`. The rows of the other machines do not
change.
