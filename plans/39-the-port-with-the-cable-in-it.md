# The port with the cable in it

Milestone 39 — Reverted

This milestone was designed, built, measured in the lab, and then
taken back out. What it built worked. The problem it was written for
had already been solved by milestone 36, and the cost of the field was
larger than the benefit that remained. The request is issue #1, closed
as won't do. This document stays because the reasoning is worth more
to a reader than a gap in the numbering.

## What it argued

A machine being prepared for a cluster rotation has two Realtek
RTL8111/8168 ports on one driver, one wired and one not. liken calls
them `eth0` and `eth1`, in the order the kernel probed them. The
argument was that no field in the Machine document could say which of
the two had the cable in it, that naming the wrong one gives a machine
with no network, and that a liken machine with no network has no shell
and no SSH, so the recovery is a person walking to it with a stick.

The milestone conceded the counter-argument in its own opening and
then argued around it. liken runs no udev, so kernel names follow
hardware enumeration order, and that order holds while the hardware
does. `InterfaceSpec.Name` said so, and it was right. The milestone
answered that the order can be reassigned by a card added, a card
moved, or a firmware update that changes the probe. Every one of those
is a change a person makes to the machine.

## What it built

`mac` sat beside `name`, and `name` became optional. An entry could
carry either or both, and an entry with both asked the boot to refuse
the interface when the two identified different ports. Addresses were
compared through `net.ParseMAC` on both sides, so a manifest could
spell one with colons, with hyphens, or as the dotted quads a switch
console prints.

The list of interfaces became `x-kubernetes-list-type: atomic`,
because a map list needs one field that identifies an entry and an
entry now had two. A CEL rule on each item refused an entry that
declared neither field. `NetworkSpec.Validate` arrived to refuse two
entries for one port, which the API server could no longer refuse on
its own.

Resolution moved to a pure function over a plain list of names and
addresses, rather than over netlink links, and it gained an error that
lists every port the machine has. The hardware report wrote `mac:` as
a real field on a machine with more than one port on the same driver,
which meant reading each port's driver from its sysfs symlink.

## What the lab measured

The lab installed a three-machine cluster from blank disks. node-1
declared its uplink by name and its cluster segment by address, and
came up with `liken: MAC 52:54:00:4c:4c:01 is eth1 on this machine` on
its console. An address no port carried produced the listing of the
guest's real ports, and the machine came up on its remaining
interface. The report's same-driver rule ran on real cards rather than
fabricated ones and declared both ports by address.

The lab also staged a change of enumeration order, by reversing the
two cards QEMU attaches to a guest. The entry that gave an address
followed its card and the machine rejoined the cluster. The entry that
gave a name followed the name onto the other card, sent its DHCP
discovery from the cluster port, and left the real uplink dark.

That measurement is the one to read carefully. Reversing the order in
which QEMU attaches two cards is a hardware change, made by a person,
to a machine that person is holding. It proved that the mechanism
worked. It did not prove that the mechanism was needed, because
nothing in the lab or on any liken machine has ever reordered its
ports on its own.

## Why it was backed out

Enumeration order is stable for fixed hardware. That is what the
original doc comment on `Name` said, and nothing since has contradicted
it. The failure mode the milestone described needs an operator to
change the hardware, and an operator who changes the hardware can
re-survey the machine.

The authoring problem was already solved. Milestone 36 boots the
machine, raises every link, reads each carrier, and writes a proposed
manifest that declares the connected ports by kernel name and leaves
the dark ones commented out beside them. The question "which port has
the cable in it" is answered by the machine itself, in the file it
writes to the stick, before anybody types a name. The milestone was
written as though a person had to guess.

The cost was real and it fell on every machine, not only on the
ambiguous ones. An optional `name` cannot be a merge key, so
`spec.network.interfaces` became an atomic list. Server-side apply
then owns the whole list as one field, so two appliers conflict over
every entry rather than over the entry they both write. Flux and a
person editing one interface collide on the list. That is a real
regression in how a fleet is managed, paid for a field that one
machine shape would have used.

## What survives

Two things from this milestone are independent of the field and stay.

The refusal that names the machine's real ports stays. A manifest that
names a port the machine does not have produces `no interface is named
eth2; this machine has eth0, eth1`. Nobody can see these machines, so
the console message is the whole diagnosis, and a message that names
what is really there turns a site visit into an edit of the manifest.

`NetworkSpec.Validate` stays, reduced to the two rules that still
apply: an interface with no name, and two entries naming one port.
With the list keyed on name again, the API server enforces both for
anything applied through it. Init also reads manifests written by hand
and carried in on a stick, which no API server ever saw, so the
validator still earns its place. It runs where the storage validator
runs: init before it touches a link, the operator before it stages a
spec, and the scaffold before it writes a manifest it generated.

The shape those two need also stays. The boot reads the kernel's link
list once and reduces it to a plain list of ports, so the rules a
manifest meets are pure functions with tests that run on any machine
rather than only under QEMU.

## What would bring it back

One machine, with ports that are genuinely reassigned by a hardware
change the operator cannot re-survey. A blind swap in a remote rack
where nobody can run the hardware report, or a firmware update that
reorders the probe on a machine already in service, would be the case.
Until such a machine exists, the report answers the question and the
merge key is worth more than the field.
