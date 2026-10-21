---
name: add-nodes
description: "Add one declared machine to a running liken cluster: write its manifest, rebuild the install stick, boot the machine from it, and watch it join. Use when a cluster needs another node."
---

This skill is the guide at https://liken.sh/docs/guides/add-nodes/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Add machines to a cluster

You add one declared machine at a time. Describe the new machine, build
the install stick again, and boot the machine from it. A new follower
makes no changes to the running machines. A new leader reboots every
machine one at a time, as step 2 explains.

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
[`wireless`](https://liken.sh/docs/reference/machine/#specnetworkinterfaceswireless)
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

The passphrase is never in the manifest, because sticks and git hold
copies of the manifest. Write it to a file in your identity directory,
named exactly like the network, as one line:

    mkdir -p mycluster/identity/psk
    echo 'the-passphrase' > mycluster/identity/psk/mynetwork

`liken layer` packs this file onto the stick beside the join token,
and the machine reads it at boot. If a manifest names a network that
has no passphrase file, `liken layer` refuses and names the file to
create. A network with `security: open` needs no file.

A machine with a working wired interface does not wait for its
radio. It boots on the wired path and joins the wifi in the
background. The interface's status appears in `kubectl get machine`
when the join settles. During that window the `WirelessJoined`
condition is `False` with the reason `Joining`, and the machine stays
`Ready`. If the machine's only interface is the radio and the
passphrase is wrong, the machine blocks the boot and waits. It says so on the
console and in `kubectl get machines`. A machine with a working wired
interface joins anyway and reports the wifi failure as a condition.
The supplicant keeps trying, and when the radio joins later, the
machine addresses it then.

A network can have several access points with the same SSID. The
machine scans for a stronger one every 30 seconds while the signal is
below -70 dBm, and once an hour above that, and moves when a scan
finds one. An access point controller can also ask the radio to move,
with an 802.11v BSS transition request, and the radio follows it. When
the access points offer 802.11r fast transition, the radio uses it, so
a move does not repeat the full handshake. To see which access point a
machine holds, read
[`status.network.interfaces[].wireless`](https://liken.sh/docs/reference/machine/#statusnetworkinterfaceswireless).
Its `bssid`, `frequency`, and `band` name the access point, and
`kubectl describe machine` lists a `Roamed` or `Associated` event for
each move. When the radio loses its access point, `WirelessJoined`
goes `False` with the reason `Joining` until the supplicant rejoins,
and the message gives the 802.11 reason code. An access point
controller can refuse a client for a minute or two after it
disconnects it. During that time the machine tries every access point
of the network at least every 30 seconds, and the console names each
access point that refused it and the 802.11 status code it gave. The signal level is the
metric `liken_wireless_signal_dbm`
([Metrics](https://liken.sh/docs/reference/metrics/#the-radios)).

## 2. If the new machine is a leader

If the new machine will run a control plane, add its name to
[`spec.leaders`](https://liken.sh/docs/reference/cluster/#spec) in two places:

* On the live cluster, with `kubectl edit cluster`. The machines read
  this document, and each machine gets its role from it.
* In your `mycluster/cluster.yaml`. The file applies only to a new
  cluster. But if you keep it correct, a new build gives the cluster
  that you run.

Make the live edit before you install the machine. Keep an odd number
of leaders, so that the datastore can always make a majority.

A change to `spec.leaders` reboots every machine, one at a time,
because each machine reads the leaders only when it boots. The leaders
give each follower its time sources, and the founder's address gives
each leader its join URL. Under `Manual`, each machine reports
`RebootPending`, and its request reads `False` in the APPROVED column
of `kubectl get disruptions`. Approve each one with
[`liken approve-reboot`](https://liken.sh/docs/reference/cli/#liken-approve-reboot), as
in [Upgrade the fleet](https://liken.sh/docs/guides/upgrade/).

## 3. Rebuild the install stick

A change to the deployment layer needs new install media. Use the
release that your fleet runs. `kubectl get clusters` shows it in the
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
<name>` entry and a `wipe and reinstall as <name>` entry. If you do not
know this hardware, boot `liken hardware report` first. It writes
`hardware-report.yaml` to the stick, so you can correct the machine's
disks, interfaces, and drivers before you install. [Install a
cluster](https://liken.sh/docs/guides/install/#5-boot-each-machine-from-the-stick)
describes the report and the held console messages fully.

Select `install as <name>`. The machine installs itself and waits at the
console:

    liken: installed to slot A; remove the stick, then press Enter to power off; the next power-on boots from the disk.

Remove the stick, then press Enter. The machine powers off. Power it on
again, and it boots from its own disk.

## 5. Watch it join

    kubectl get machines

The new machine appears, then it becomes Ready. It also appears in
`kubectl get nodes`. The rows of the other machines do not change.
