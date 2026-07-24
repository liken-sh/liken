# The port with the cable in it

Milestone 39 — Done

A machine being prepared for a cluster rotation has two Realtek
RTL8111/8168 ports on one driver, one wired and one not. Ubuntu called
them `enp1s0` at PCI 01:00.0 with a carrier and `enp3s0` at 03:00.0
without one. liken calls them `eth0` and `eth1`, in the order the
kernel probed them, and no field in the Machine document could say
which of the two had the cable in it. Naming the wrong one gives a
machine with no network, and a liken machine with no network has no
shell and no SSH: the recovery is a person walking to it with a stick.

`InterfaceSpec.Name` rested on an argument that was true and not
enough. liken runs no udev, so kernel names follow hardware
enumeration order, and that order holds while the hardware does. It
holds for one port. It says nothing about two ports of one model, and
it can be reassigned between them by a card added, a card moved, or a
firmware update that changes the probe.

The line this milestone works from: **position and identity are
different questions about a port, and a manifest must be able to ask
either one.** A name is a position: whichever port enumerates in that
place. A MAC address is an identity: that exact port. Neither answer
replaces the other, so the field that asks for a name stays, and a
field that asks for an address joins it.

## A manifest names a port by name, by address, or by both

`mac` sits beside `name`, and `name` is now optional. An entry with
both asks the boot to check a fact rather than to rank a preference,
and the boot refuses the interface when the two identify different
ports. Guessing a winner there would put the machine's whole network
on a coin toss, settled by a person who cannot reach the machine
except on foot.

Addresses are compared through `net.ParseMAC` on both sides, so a
manifest may spell one in any form a person copies: a Linux tool's
colons, a firmware screen's hyphens, or a switch console's dotted
quads. Two spellings of one address are one port, and the validator
knows it.

`NetworkSpec` gains a `Validate`, modelled on the storage one, and it
runs at the same three places: init before it touches a link, the
operator before it stages a spec, and the scaffold before it writes a
manifest it generated. It refuses an entry that names no port at all
and an address that is not an address. Whether an address belongs to a
port of this machine is not a question the manifest can answer, so
resolution answers it, against the links that exist.

## The list of interfaces is atomic

A map list needs one field that identifies an entry, and an entry now
identifies its port by either of two fields, so the merge key had to
go. Atomic is also what this list means: the set of ports a machine
uses is one decision, replaced whole, the same as the nameservers list
beside it.

The API server no longer refuses a repeated key, so `Validate` refuses
two entries for one port instead. A CEL rule on each item refuses an
entry that declares neither field, which keeps that mistake from ever
reaching a machine.

## A refusal names every port the machine has

Nobody can see the machine. The console message is the whole
diagnosis, so it carries the evidence the person is missing:

    no interface has MAC e0:51:d8:aa:bb:99; this machine has
    eth0 e0:51:d8:aa:bb:01, eth1 e0:51:d8:aa:bb:02

That is a drive to the site turned into an edit of the manifest. A
name that matches nothing gets the same listing, and for the same
reason: the addresses in it are what the person should have written.

Resolution takes a plain list of names and addresses rather than
netlink links, because the decision is where the mistakes are, and a
pure function is tested on every machine instead of only in the lab.
Once a port is resolved, the boot prints the translation once and
speaks in kernel names from there, so the rest of the log and the
machine's published status still agree with each other.

## The report writes the address when a name would be a guess

The hardware report already read every port's address and printed it
as a comment. It now writes `mac:` as a real field when the machine
has more than one port on the same driver, which is the case where a
name cannot identify a port: two ports on one driver are two cards of
one model, ordered by a probe. A machine whose names are unambiguous
keeps `name:`, because that manifest still describes every machine
built to the same recipe.

Reading the driver needed one more fact carried from the boot: the
report walks each port's driver symlink in sysfs, the same way it
reads a disk's transport. The kernel names stay in the proposal as
comments, next to the driver that made them ambiguous.

## What the lab proved

The lab drilled resolution end to end. node-1's manifest now declares
its uplink by name and its cluster segment by MAC address, so one
install exercises both ways of saying which port. `make smoke-uefi`
installed the guest from blank disks and it was Ready in ten seconds,
with `liken: MAC 52:54:00:4c:4c:01 is eth1 on this machine` on its
console, followed by the static address applied to eth1.

The failure was drilled the same way. A manifest with an address no
port carries produced exactly the sentence above, naming both of the
guest's ports and their addresses, and the machine went on to come up
on its remaining interface.

Two things stay unproven. The lab's guests have one port per segment
on distinct drivers, so nothing here has exercised the report's
same-driver rule against real hardware; the unit tests drive it from
fabricated ports, and the two-port Realtek board that prompted this
milestone is the machine that will settle it. And an enumeration order
that actually changes between boots is not something the lab can
stage, so the claim that an address survives one rests on the kernel's
contract, not on a measurement.

## The manual

The Machine reference regenerates from the schema, so both fields
teach their own trade-off there. The install guide gains the choice
itself: what a name means, what an address means, which one the report
writes and why, and the cost of an address. That cost is worth stating
plainly, because it lands at the worst moment. A MAC address ties a
manifest to one physical machine, so a replacement card or motherboard
needs an edit, and that is exactly when the machine cannot get on the
network to tell you its new address. The hardware report covers it: it
runs offline and writes to the stick. Name matching stays for anyone
who would rather describe a position than an identity.
