# Wifi

Milestone 62. A Machine joins the cluster over a wireless interface,
declared in `spec.network` the way a wired one is.

## The problem

liken cannot put a machine on a wireless network. The gap has three
parts:

* The interface schema is wired-only. An entry takes `name`,
  `address`, `gateway`, and `nameservers`, and nothing names a network
  to join or a credential to join it with.
* The image ships no supplicant. The kernel side is already there:
  milestone 32 brought the whole module tree and its firmware, so
  `iwlwifi` and its ucode are on every machine. Nothing in userspace
  can complete a WPA handshake.
* A passphrase is a secret, and the manifest travels on install
  sticks and in deployment git. The secret needs a channel of its
  own.

The field case is `stick-1` at 44 Stony Point: an Apollo Lake HDMI
stick PC with an Intel Wireless 7265, meant to sit where no ethernet
run reaches. It installs to eMMC and joins as a follower. Its
hardware report loaded `iwlwifi` and then had nothing to configure
the radio with.

## The supplicant

The image vendors `wpa_supplicant` from the hostap project, and init
runs it as a child process. Three alternatives lost:

* The kernel can run the WPA handshake itself when the firmware
  offers the `4WAY_HANDSHAKE_STA_PSK` offload, and a pure Go client
  could then join with one netlink call. But only full-MAC firmware
  offers it, mostly Broadcom parts. `iwlwifi` and the other soft-MAC
  drivers do not, so the field case cannot take this path.
* A supplicant written in Go is possible. WPA2-PSK needs EAPOL frames
  over a packet socket and key installs over netlink, with crypto
  from the standard library. But WPA3 adds SAE, a full elliptic-curve
  exchange, and the project would own that protocol code forever.
  `wpa_supplicant` handles both, so a mixed WPA2/WPA3 network, the
  common home configuration today, works without a version check.
* `iwd` also runs the handshake in userspace, but it requires a D-Bus
  system bus, and the image has none.

hostap is BSD-licensed. The pin, the notice in
`licensing/NOTICES.md`, and the source mirror follow the same
machinery every vendored component uses. The release build counts the
new binary against the 1Gi slot budget.

## The split of the work

The supplicant owns only the 802.11 session: association, the
four-way handshake, rekeys, and reconnection. Init keeps everything
it already owns. Once the link associates, init runs its own DHCP
client or applies the static address on that interface, exactly as on
ethernet. No DHCP hook scripts, no second address manager, one new
binary with one job.

Init starts the supplicant only when `spec.network` names a wireless
interface. The caller enforces this, not a configuration knob. A
machine with no wireless entry never runs the process.

## Supervision

`init/supervisor.go` is the service manager of this OS: one reaper,
one death registry, and a restart loop with backoff around k3s. The
supplicant becomes the second resident of that supervisor. When the
process dies, the association dies with it, and the supervisor
restarts it the way it restarts k3s. The address survives on the
interface, so the outage is seconds, the same class as a k3s bounce.

## The control socket

Init attaches to the supplicant's UNIX control socket and reads the
events it pushes: connected, disconnected, and the credential
failures. The events answer the question the kernel cannot. Netlink
reports "no carrier" identically for a wrong passphrase, a powered-off
AP, and a machine parked out of range. The supplicant's
`CTRL-EVENT-SSID-TEMP-DISABLED reason=WRONG_KEY` names the one case
that no amount of waiting will fix.

The protocol is plain text over one datagram socket, so init carries
its own small client instead of vendoring `wpa_cli`. The events
drive three things: the gate that tells init the join finished, the
console lines, and the facts that become `Machine` status. The
console parity rule holds: a machine with no shell can still say "the
passphrase is wrong" in `kubectl get machine`.

## The spec

An interface entry grows a `wireless` object with two fields: the
SSID, and the security type. Both are public facts and belong in the
manifest. The first security values are `wpa-psk`, which covers
WPA2 and WPA3 personal networks through the supplicant's own
negotiation, and `open`. The human writes the SSID directly. The
report proposes nothing for a radio, because a scan says what exists,
not what the machine may join.

## The passphrase

The passphrase rides where the join token rides. The image already
carries `/etc/liken/token` and the cluster CA under the rule "same
image means same cluster", and a wifi passphrase is the same class of
fact: a cluster-level credential the machine needs before the cluster
can give it anything. The installer writes one file per network at
`/etc/liken/psk/<ssid>`. Spec validation refuses an SSID that cannot
name a file.

Init resolves the passphrase the way it resolves the cluster
document: the staged copy on `machineState` first, then the image's
file. Nothing writes the staged copy yet, so the image always wins
today. Rotation later becomes a writer of that staged copy, not a
redesign of the read path.

Init joins the spec's SSID with the file's passphrase to generate the
supplicant configuration under `/run`, never on durable storage. The
manifest stays publishable and deployment git never sees the secret.

Until rotation exists, a wrong passphrase on installed media is fixed
by the install media, not in place. The plan states this instead of
letting an operator discover it.

## When a join fails

A deterministic credential failure parks the boot only when the
machine has no other path to the cluster. After every interface
settles, init asks whether any healthy interface gives a route toward
the cluster's endpoint. If one does, the boot proceeds, the node runs
degraded, and a condition carries the supplicant's reason. If none
does, and the supplicant reported a deterministic failure such as
`WRONG_KEY`, the boot parks with the reason on the console.

The park is an attended hold, not a halt. The supplicant keeps
retrying and init keeps listening on the same event socket, so a fix
on the network side resumes the boot without a power cycle.

Absence never parks. An AP that is off, rebooting, or out of range
looks the same as one that is about to answer, and a machine must not
refuse to boot because its AP rebooted first. Only the supplicant's
explicit failure events qualify.

## The radio before the supplicant

Two facts about the platform come before any of the above:

* liken does not touch rfkill. The kernel starts radios unblocked, a
  soft block does not survive a reboot, and nothing in liken writes
  one, so there is no block to clear. An earlier draft unblocked the
  radio through `/dev/rfkill` before the supplicant started; the
  first metal boot hung in that code (a non-blocking fd handed to
  `os.NewFile` parks the reader in Go's poller instead of returning
  `EAGAIN`), and the guard it implemented had nothing to guard.
* The kernel limits 5GHz channels until it loads `regulatory.db`
  from firmware. The linux-firmware release does not carry that
  file; it comes from the wireless-regdb project, so the
  linux-firmware domain carries a second pin and the derivation
  ships both the database and its signature. Without it, a
  5GHz-only network may be invisible.

## The ciphers the kernel cannot load itself

The 802.11 stack encrypts with ciphers it instantiates at the moment
the supplicant installs a key: `ccm(aes)` for CCMP, `gcm(aes)` for
GCMP, and `cmac(aes)` for protected management frames. On an ordinary
distribution these arrive by autoload: the crypto API asks the kernel
to run `modprobe`, and `modprobe` loads the cipher module. liken
ships no `modprobe`, so the request fails silently and the cipher
never exists.

The failure this produces is misleading. The four-way handshake
completes, the supplicant derives the keys, and then the kernel
refuses the key install with `ENOENT`. The supplicant reports
`WRONG_KEY`, the one failure this plan treats as deterministic. A
machine with a correct passphrase and a missing cipher would park as
if the passphrase were wrong, and no edit to the passphrase could
release it.

So the wireless bring-up loads the cipher modules itself, before the
supplicant starts, through the same loader that `spec.modules` uses.
It loads each of the three ciphers above that the kernel build ships
as a module, and it skips the ones built in. The loads run only when
a spec declares a wireless interface, which keeps the rule that
nothing wireless runs unless the spec asks for it.

The lab measured the failure and the fix on `liken-1`'s RTL8821CE on
2026-08-26: with no `ccm(aes)`, the handshake succeeded and the key
install failed with `set_key failed; err=-2`; with `ccm.ko` loaded,
the same configuration associated in 5 seconds and pulled a DHCP
lease.

## An address from DHCP must be stable

k3s reads the node's address at start. The kubelet registers with
it, flannel builds its overlay tunnels to it, and peers cache it. A
lease that comes back different does not take effect live; the
machine needs a restart, and until then the results are
unpredictable. This is true for every interface, wired or wireless.
DHCP stays fine for a node, with one instruction for the operator:
configure the DHCP server to reserve a fixed address for the
machine's MAC. The manual carries this in two places: the
`spec.network` schema description, and the add-nodes guide
(`docs/content/docs/guides/add-nodes.md`).

## What a wireless join does not change

An edit to the wireless fields drifts, stages, and applies at the
next boot, because milestone 41 gave every `spec.network` edit that
path. Time sync keeps its place, after the network and before
anything that needs a wall clock; a WPA join needs no correct clock.
The sweep sees a machine that roamed out of range as a machine that
is down, and the conditions carry the difference; no special case is
added for radios.

## The drill

`liken-1` has a wireless NIC, an RTL8821CE on the `rtw88` driver. Its
Machine adds a second interface entry: the wifi, `wpa-psk`, with a
static address one above its wired address on the same network. Both
paths reach the cluster's endpoint, so the drill exercises the join,
the status story, and the degraded-not-parked rule on real hardware.
Lab releases serve from the development laptop over the LAN with the
existing `-9xx` channel recipe.

This chip cannot run with ASPM enabled. Powering the radio on with
ASPM active deadlocks the kernel against its own PCIe error handler,
and the 2026-08-26 incident took the machine down hard enough to need
a reinstall. The drill therefore depends on plan 55: `liken-1`
declares `rtw88_pci` in `spec.modules`, ordered before the chip
module so the parameter string reaches it, and sets
`rtw88_pci.disable_aspm: "1"` in `spec.moduleParameters`. The lab
proved the parameter on 2026-08-26: with it, the same power-on that
deadlocked the kernel returned in under a second, and the radio
carried a join and a DHCP exchange with no PCIe error.

## Deferred, by name

* Rotation: the operator stages a new passphrase file onto
  `machineState` and the next boot reads it. The read path is already
  in place.
* A virtual lab drill: `mac80211_hwsim` simulates radios and
  `wpa_supplicant` can serve as the AP, so the join path can run
  under QEMU in CI. Until then, wifi drills are metal drills.
* DHCP lease renewal. Found while reading, and out of scope here:
  init requests a lease once at boot and nothing renews it. A machine
  that runs past its lease time keeps an address the server may
  reassign. A wifi session keeper and a lease renewer are the same
  shape of resident work in init.
