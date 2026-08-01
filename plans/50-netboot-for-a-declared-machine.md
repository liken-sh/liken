# Netboot for a declared machine

Milestone 50 — Proposed. It would let a new machine boot from an
existing leader over the network: the report image when nobody
declared it, the installer when somebody did, and its own disk once
it is installed. No stick is involved.

## The problem

Every machine today starts from a stick. The CLI composes one per
machine, a person carries it to the hardware, and the firmware boots
it. That works, and milestone 36 made the stick teach the operator
what it found. But the carry does not scale past a handful of
machines, and a machine in a rack far from the drawer waits on a
person with media. The cluster already holds everything the stick
carries: the artifacts are on every boot slot, and the declarations
are in the API. This milestone serves both from the leaders.

## One Go program, not dnsmasq

The serve decision needs the cluster. An unknown MAC gets the report
image, a declared MAC gets the installer, and an installed machine
gets its own disk. dnsmasq answers from a configuration file, and a
configuration file cannot ask the API server which of those three a
MAC is. The controller is therefore one Go program in the shape of
pixiecore: it answers proxyDHCP, serves the bootloader over TFTP,
serves the artifacts over HTTP, and keeps the Machine list current
with a watch. One program also reads better than a daemon plus the
scripts that would steer it, and this repository is written to be
read.

## proxyDHCP

A PXE firmware asks DHCP for two things: an address, and boot
instructions. They do not have to come from the same server. A
proxyDHCP server answers only the boot half, in a reply the firmware
merges with the address it got from the network's real DHCP server.
The controller never assigns an address, so it cannot conflict with
the server the network already has, and an operator turns the
feature on without a change to the router.

The proxy shape has one honest limit. It supplements a DHCP server;
it does not replace one. A segment with no DHCP server gives the
firmware no address, and no boot instructions repair that. The
feature stays a pure proxy anyway, because a wrong DHCP server
breaks every machine on the segment, not only the enrolling one. A
dedicated cluster switch with no router is therefore out of this
milestone's scope, and the lab section below says what the drill
does about it.

## iPXE

PXE firmware loads files over TFTP, and TFTP moves a large file
badly. The standard bridge is iPXE: the firmware chainloads a small
iPXE binary over TFTP, and iPXE pulls the kernel and the initrds
over HTTP. The controller serves `undionly.kpxe` to BIOS firmware
and `snponly.efi` to UEFI firmware, which covers both boot dialects
the fleet already supports. iPXE becomes a vendored pin with a
`fetch.sh` and a `latest.sh` of its own. It is GPL, so
`licensing/sources.sh` and `licensing/NOTICES.md` gain an entry, and
the release mirrors its source.

## The payload comes from the slot

The feature pod mounts the leader's boot slot read-only and serves
the same pieces a stick boot loads: the kernel, the microcode cpio,
and the whole-OS payload, concatenated as initrds the way the
stick's boot entries load them. The command line carries
`rootfstype=ramfs`, because this is a boot whose initrd carries the
OS. Serving from the slot gives two properties without further
machinery. The version a machine joins on is exactly the version the
leader runs, so a join can never pull a version the cluster is not
on. And a join needs no internet.

**Verify the slot's contents before building this.** The design
assumes every piece the installer boot needs is on the slot,
including the microcode cpio, and that the CLI's stick composition
is reusable for the payload. Read `cli/` and the slot layout first.
If a piece is missing from the slot, the design must say where it
comes from instead.

## The three serve states

The rule that decides what a MAC boots has three states, and the
controller resolves them from the Machine list alone.

* **Unknown.** No Machine names this MAC. The controller serves the
  report image, which changes no disk and writes what it found to
  the console, exactly as milestone 36 built it. The operator reads
  the console and declares the Machine by hand. Milestone 51 makes
  the report travel instead.
* **Declared, not installed.** A Machine names this MAC and no
  install has completed. The controller serves the installer, the
  same whole-OS boot the stick's install entry runs.
* **Installed.** The Machine's status records a completed install.
  The controller serves an iPXE script that exits, and the firmware
  falls through to the local disk. This state is what makes
  "network first" a safe firmware boot order: a machine that always
  asks the network first still lands on its own slots every day.

A reinstall requested on the Machine spec returns the machine to the
installer state, with the same erase rules milestone 37 defined.

**Verify the installed signal before building this.** The controller
reads it from the Machine's status. Name the exact field when the
work starts, and confirm it survives the machine's own reboots.

## A feature slug

Netboot ships as a `netboot` feature on the Cluster, off by default,
like flux and traefik. It runs as a DaemonSet on the leaders with
host networking, because proxyDHCP answers broadcasts and a pod
network does not carry them. Every leader answers, the firmware
takes the first answer, and the serve rule is deterministic, so any
answer is the same answer. The feature states its memory cost the
way every bundled component does, because the 1GB machines account
for every megabyte.

## Trust

The installer payload carries the material a machine needs to join,
so netboot trusts the layer-2 segment the way the operator trusts
the drawer that holds the sticks. Anyone who can plug into the
segment and present a declared MAC can receive what that machine
would receive. The report image carries no secret; it is the same
public bytes the release channel serves. The manual states this
plainly, so an operator can decide whether a segment deserves the
feature.

## The netboot-cluster

The drill lives in a new `netboot-cluster` domain beside
`dev-cluster` and `gitops-cluster`, which already set the precedent
for a lab cluster built around one behavior. It uses its own
multicast group, so the two labs never share a segment. The topology is
one leader, or two when a drill needs two answers, and one enrollee
guest with a blank disk that boots from the network.

The lab segment has no router, so the drill supplies the DHCP server
that the product refuses to be: a throwaway pod with host networking
on the leader hands the enrollee an address, and the recipe deletes
it when the drill ends. The product ships none of it.

## Verification

Unit tests cover the serve rule: each of the three states, the
reinstall intent, and a MAC that two Machines name, which is refused
with a report, not resolved by a guess.

On the netboot-cluster: an undeclared guest boots to the report
console. The operator declares its MAC, and the same guest installs
and joins with no media. The joined guest then boots again with the
network first and lands on its own disk. A reinstall request takes
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
controller to own a lease range, and leases are state that needs one
active server where proxy answers need none. If a real deployment
runs a routerless cluster switch, a range knob is the shape to
consider, with leader election as its cost.

**The first machine.** Netboot here requires a leader that already
runs. Booting the first machine of a new cluster from the release
channel, iPXE against the internet, is a different trust story and
its own milestone.

**Cloud images.** A VPS that cannot PXE from a liken leader needs a
published disk image instead. That is a release-channel artifact
question, not a serve-rule question.
