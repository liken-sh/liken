# Netboot for a declared machine

Milestone 50. Proposed. It would let a new machine boot from an
existing leader over the network: the report image when nobody
declared it, the installer when somebody did, and its own disk once
it is installed. No stick is involved.

## The problem

Every machine today starts from a stick. The CLI composes one per
machine, a person brings it to the hardware, and the firmware boots
it. That works, and since milestone 36 the stick shows the operator
the hardware it found. But carrying media does not scale past a
handful of machines, and a machine in a rack far from the sticks
needs a person to bring the media. The cluster already contains everything that is on
the stick: the artifacts are on every boot slot, and the declarations
are in the API. This milestone serves both from the leaders.

## The controller as one Go program

The controller must read the cluster to decide what to serve. An
unknown MAC gets the report
image, a declared MAC gets the installer, and an installed machine
gets its own disk. dnsmasq answers from a configuration file, and a
configuration file cannot ask the API server which of those three a
MAC is. The controller is therefore one Go program modeled on
pixiecore: it answers proxyDHCP, serves the bootloader over TFTP,
serves the artifacts over HTTP, and keeps the Machine list current
with a watch. One program is also easier to read than a daemon plus
the scripts that would control it, and this repository is written to
be read.

## proxyDHCP

A PXE firmware asks DHCP for two things: an address, and boot
instructions. They do not have to come from the same server. A
proxyDHCP server answers only the boot half, in a reply the firmware
merges with the address it got from the network's real DHCP server.
The controller never assigns an address, so it cannot conflict with
the server the network already has, and an operator turns the
feature on without a change to the router.

A proxy has one limit: it adds to a DHCP server and cannot replace
one. On a segment with no DHCP server, the firmware gets no address,
and boot instructions cannot fix that. The feature is still only a
proxy, because a wrong DHCP server breaks every machine on the
segment, not only the enrolling one. A
dedicated cluster switch with no router is therefore out of this
milestone's scope, and the lab section below says what the drill
does about it.

## iPXE

PXE firmware loads files over TFTP, and TFTP moves a large file
badly. The standard solution is iPXE: the firmware chainloads a small
iPXE binary over TFTP, and iPXE pulls the kernel and the initrds
over HTTP. The controller serves `undionly.kpxe` to BIOS firmware
and `snponly.efi` to UEFI firmware, which covers both boot dialects
the fleet already supports. iPXE becomes a vendored pin with its own
`fetch.sh` and `latest.sh`. It is GPL, so
`licensing/sources.sh` and `licensing/NOTICES.md` gain an entry, and
the release mirrors its source.

## Serving the payload from the leader's boot slot

The feature pod mounts the leader's boot slot read-only and serves
the same pieces a stick boot loads: the kernel, the microcode cpio,
and the payload that holds the whole OS, concatenated as initrds the
way the stick's boot entries load them. The command line includes
`rootfstype=ramfs`, because the initrd of this boot contains the OS.
Serving from the slot gives two results with no extra code. A machine
joins on exactly the version the leader runs, so a join can never
pull a version the cluster is not on. A join also needs no internet
access.

**Verify the slot's contents before building this.** The design
assumes every piece the installer boot needs is on the slot,
including the microcode cpio, and that the CLI's stick composition
is reusable for the payload. Read `cli/` and the slot layout first.
If a piece is missing from the slot, the design must say where it
comes from instead.

## The three serve states

The rule that selects what a MAC boots has three states. The
controller resolves them from the Machine list, with no other input.

* **Unknown.** No Machine names this MAC. The controller serves the
  report image, which changes no disk and writes what it found to
  the console, exactly as milestone 36 built it. The operator reads
  the console and declares the Machine by hand. Milestone 51 sends
  the report over the network instead.
* **Declared, not installed.** A Machine names this MAC and no
  install has completed. The controller serves the installer: the
  same boot, with the whole OS in the initrd, that the stick's install
  entry runs.
* **Installed.** The Machine's status records a completed install.
  The controller serves an iPXE script that exits, and the firmware
  falls through to the local disk. This state makes "network first"
  a safe firmware boot order: a machine that always tries the network
  first still boots from its own slots on every ordinary boot.

A reinstall requested on the Machine spec returns the machine to the
installer state, with the same erase rules milestone 37 defined.

**Verify the installed signal before building this.** The controller
reads it from the Machine's status. Name the exact field when the
work starts, and confirm it survives the machine's own reboots.

## The `netboot` feature

Netboot ships as a `netboot` feature on the Cluster, off by default,
like flux and traefik. It runs as a DaemonSet on the leaders with
host networking, because proxyDHCP answers broadcasts and a pod
network does not carry them. Every leader answers, and the firmware
takes the first answer. The serve rule is deterministic, so every
leader sends the same answer. The feature states its memory cost the
way every bundled component does, because the 1GB machines have
little memory to spare.

## Trust

The installer payload contains the material a machine needs to join.
Netboot exposes that material to the layer-2 segment. For installation
from a stick, physical access to the stick controls access to the same
material. Anyone who can plug into the
segment and present a declared MAC can receive what that machine
would receive. The report image contains no secret; it is the same
public bytes the release channel serves. The manual states this
risk, so an operator can decide whether to turn the feature on for a
segment.

## The netboot-cluster lab

The drill runs in a new `netboot-cluster` domain beside
`dev-cluster` and `gitops-cluster`. Those two are already lab
clusters built to test one behavior each. It uses its own
multicast group, so the two labs never share a segment. The topology is
one leader, or two when a drill needs two answers, and one enrollee
guest with a blank disk that boots from the network.

The lab segment has no router, and the netboot feature is not a
DHCP server. So the drill provides one: a throwaway pod with host
networking on the leader gives the enrollee an address, and the
recipe deletes the pod when the drill ends. `liken` does not ship
this DHCP server.

## Verification

Unit tests cover the serve rule: each of the three states, the
reinstall intent, and a MAC that two Machines name. The controller
refuses that MAC and reports it. It does not guess.

On the netboot-cluster: an undeclared guest boots to the report
console. The operator declares its MAC, and the same guest installs
and joins with no media. The joined guest then boots again with the
network first and boots from its own disk. A reinstall request takes
it through the installer again. Both firmware dialects run the
drill, because the two iPXE binaries are two different code paths.

## The manual

A new guide under `docs/content/docs/guides/` covers adding a
machine over the network, beside the stick path. The Cluster
reference regenerates from the schema, so the `netboot` feature's
description is written as manual text. The CLI does not change in
this milestone.

## Not in this milestone

**Enrollment over the network.** The unknown machine's report
travels by console in this milestone. Milestone 51 sends it over the
network.

**Serving addresses.** A segment with no DHCP server would need the
controller to own a lease range. Leases are state, so they need one
active server. Proxy answers hold no state, so they need no single
server. If a real deployment runs a cluster switch with no router, a
setting for a lease range is the design to consider, and its cost is
leader election.

**The first machine.** Netboot here requires a leader that already
runs. Booting the first machine of a new cluster from the release
channel, iPXE against the internet, raises a different trust
question and needs its own milestone.

**Cloud images.** A VPS that cannot PXE from a liken leader needs a
published disk image instead. That work adds a release-channel
artifact and does not change the serve rule.
