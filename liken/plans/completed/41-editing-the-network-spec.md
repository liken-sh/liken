# Editing the network spec

Milestone 41. Completed. The boot records the network it came up
under, so an edit to `spec.network` drifts, stages, and applies at the
next boot.

The Machine schema told people to edit `spec.network`: init applies it
at boot, and a change made in-cluster takes effect at the next boot.
That statement was not true. A lab drill force-applied a valid edit to
a running node. The operator reported `SpecConverged True Converged`
at the edited generation, nothing was staged, and the node rebooted
under the same proven manifest it had before. The field accepted edits
and did nothing with them.

Convergence measured drift in storage and in the module list, and a
network-only edit produces neither. Convergence stopped at the first
case of the decision table and reported the machine converged.
Everything after that case was unreachable for such an edit, including
the operator's own network validation, so a manifest that declared one
port twice also read as converged instead of refused.

Storage does not have this problem, because the boot records the
storage it actuated and the operator compares the spec against that
record. There was no such record for the network, so there was nothing
to compare a network spec against. A field that the cluster can edit
needs a record of what the boot did with it. Without that record, the
operator has nothing to judge and every verdict it gives is a guess.

## The boot records the network it came up under

`status.boot.network` joins `status.boot.storage`. It holds the
network spec that the winning manifest declared, recorded as actuated
whatever the outcome of each interface was. A second boot under this
manifest would ask for the same network, so this record is what the
spec is compared against. What each interface actually got is a
different question, and `status.network` has always answered that one.

Init records the network where the manifest wins, not where the links
come up, for that reason. The record states what the boot ran under.
An interface that failed to resolve is still part of what this boot
was asked to do, and a reboot would ask for it again.

The fact travels the same path that storage does: one directory per
interface under `boot/network/` in the facts tree, keyed by position
in the declared list rather than by the name of the port. The order of
the list is part of what the list asks for, because it is the order in
which each interface's nameservers reach `resolv.conf`. Sorted
directory names would lose that order as soon as a machine declared
ten interfaces.

## Absent and empty are different facts

An empty network spec means the zero-configuration default: DHCP on
the first port that looks like real hardware. A boot that recorded no
network at all means something else, and the difference determines
whether a fleet reboots. If the operator read a missing record as an
empty spec, it would report every declared interface as drift on every
machine whose facts lack the record, and each machine would stage a
manifest and ask for its turn.

The record is therefore a pointer, and `boot/network` is a directory
that exists whenever a boot recorded anything, even with no file under
it. This is the one place in the facts tree where an empty directory
has meaning. A machine whose boot recorded nothing is a machine
the operator cannot judge, and the verdict for a case it cannot judge
is the one that `FactsIncomplete` already takes: report no drift
rather than reboot on a guess.

## A network change converges the way a storage change does

Nothing else about the convergence changes shape. `NetworkDrift` joins
the drift that the decision table measures, the manifest stages, and
the same gate decides who starts the boot: Manual waits for a person,
a cluster member on Auto waits for its turn, and only then is a reboot
requested. The condition reasons are the ones the phase table already
has, so a staged network change reads as UpdatePending in a fleet
listing, exactly like a staged storage change.

Two rules from the storage drift do not apply here. Storage
roles are grow-only, and a network has no such rule: any spec may
replace any other, and the only question is whether the machine
matches the spec that the cluster asks for now. Storage also compares
role by role, by name, and the network compares by position, because
the order of that list is the order in which each interface's
nameservers reach `resolv.conf`. The same two ports in the other order
are a different request.

The live-load path refuses a network change on both sides. Adding a
module needs no disruption, and a manifest that also changes the
network must not use that exemption. Nothing in a running kernel
re-addresses an interface, and a promoted manifest of that kind would
leave the boot record with a network that the machine does not run.
The operator does not ask for the load, and init derives the same
refusal from the same shared function before it acts on any intent.

## The validation gate is reachable now

`NetworkSpec.Validate` was already wired into the operator, ahead of
staging, so that a spec which init would refuse at boot is refused in
the cluster instead. A refusal at boot costs a reboot and returns the
machine on its old manifest with a rejection record. The gate was
after the drift check, and no network-only edit ever reached it. With
drift measured, the gate runs.

## What the lab measured

The drill that found the fault ran again on a two-machine cluster
installed from blank disks, and the edit now behaves the way the
schema says. Both machines came up with a boot network recorded that
matched the manifest they booted under, which is the record the
whole milestone depends on.

The Manual case matters most, because Manual is the default. The lab
added a nameserver to node-4's uplink under that policy. The node
reported `RebootPending`, and the message stated the diff:
`network: eth0: nameservers 10.10.0.1 declared, (none) actuated`. The
machine held that verdict with its boot time unmoved, and the fleet
listing read UpdatePending rather than Degraded. Nothing rebooted
until the policy changed.

The policy was then set to Auto. The node rebooted, its new boot
record held the declared nameserver, and its live network status
listed that nameserver beside the one that its DHCP lease supplied.
The machine returned to Converged. The leader stayed Converged
throughout and did not reboot, because a machine with no network edit
has no drift.

The invalid spec is refused earlier than the operator. Because the
list of interfaces is keyed by name, the API server itself rejects a
manifest that declares one port twice and names the duplicate entry,
so no such spec is ever staged or reaches a machine. The validator
that milestone 39 left behind still applies to the manifests that
arrive another way: init also reads a manifest written by hand and
carried in on a stick, which no API server ever checked.

## The manual

The Machine reference regenerates from the schema, so the schema held
the untrue statement and the schema holds the correction.
`spec.network` now says what happens to an edit. The cluster cannot
apply it live, because the cluster reaches the machine over the
addresses that the edit changes. The edit stages like a storage edit,
the next boot applies it, and `rebootPolicy` says who starts that
boot. The spec's own summary listed network apart from the staged
fields, and now lists it with them. `status.boot.network` documents
itself as the drift reference, including what its absence means,
because an operator who reads a status must know that a field which is
not there is a different claim from a field that is empty.
