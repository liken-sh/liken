# An edit nobody applied

Milestone 41 — Done

Milestone 39 made `spec.network` a field that people are told to
edit. The schema said so: init applies it at boot, and a change made
in-cluster takes effect at the next boot. The drill that closed that
milestone found the sentence untrue. A valid edit was force-applied to
a running node, the operator reported `SpecConverged True Converged`
at the edited generation, nothing was staged, and the node rebooted
under the same proven manifest it had before. The field accepted
edits and did nothing with them.

Convergence measured drift in storage and in the module list, and a
network-only edit produces neither. It stopped at the first case of
the decision table and reported the machine converged. Everything
after that case was unreachable for such an edit, including milestone
39's own operator-side validation, so a manifest that declared one
port twice also read as converged instead of refused.

Storage does not have this problem because the boot records the
storage it actuated, and the operator compares the spec against that
record. There was no such record for the network, so there was
nothing to compare a network spec against. **A field the cluster can
edit needs a record of what the boot actually did with it, or the
operator has nothing to judge and every verdict it gives is a
guess.**

## The boot records the network it came up under

`status.boot.network` joins `status.boot.storage`: the network spec
that the winning manifest declared, recorded as actuated whatever the
outcome of each interface was. Booting again under this manifest
would ask for the same network, so this is what the spec is compared
against. What each interface actually got is a different question,
and `status.network` has always answered that one.

Init records it where the manifest wins, not where the links come up,
for that reason. The record is what the boot ran under. An interface
that failed to resolve is still part of what this boot was asked to
do, and rebooting would ask for it again.

The fact travels the same path storage does: one directory per
interface under `boot/network/` in the facts tree, keyed by position
in the declared list. Position is the only key every entry has, since
an entry names its port by a kernel name, a MAC address, or both, and
the order of the list is part of what it asks for.

## Absent and empty are different facts

An empty network spec means the zero-configuration default: DHCP on
the first port that looks like real hardware. A boot that recorded no
network at all means something else entirely, and the difference
decides whether a fleet reboots. Reading a missing record as an empty
spec would report every declared interface as drift on every machine
whose facts lack the record, and each one would stage a manifest and
ask for its turn.

So the record is a pointer, and `boot/network` is a directory that
exists whenever a boot recorded anything, even with no file under it.
This is the one place in the facts tree where an empty directory
carries meaning, and it earns the exception. A machine whose boot
recorded nothing is a machine the operator cannot judge, and the
verdict for something unjudgeable is the one `FactsIncomplete`
already takes: report no drift rather than reboot on a guess.

## A network change converges the way a storage change does

Nothing else about the convergence changes shape. `NetworkDrift`
joins the drift the decision table measures, the manifest stages, and
the same gate decides who starts the boot: Manual waits for a person,
a cluster member on Auto waits for its turn, and only then is a
reboot requested. The condition reasons are the ones the phase table
already knows, so a staged network change reads as UpdatePending in a
fleet listing, exactly like a staged storage change.

Two rules the storage drift carries do not travel with it. Storage
roles are grow-only, and a network has no such rule: any spec may
replace any other, and the only question is whether the machine
matches the one the cluster asks for now. And storage compares role
by role, by name, while the network compares by position, because
that list is atomic and its order is the order each interface's
nameservers reach `resolv.conf`.

The live-load path refuses a network change on both sides. Adding a
module needs no disruption, and a manifest that also changes the
network must not ride along on that exemption: nothing in a running
kernel re-addresses an interface, and promoting such a manifest would
leave the boot record claiming a network the machine is not running.
The operator will not ask for the load, and init re-derives the same
refusal from the same shared function before it acts on any intent.

## The validation gate is reachable now

Milestone 39 wired `NetworkSpec.Validate` into the operator, ahead of
staging, so that a spec init would refuse at boot is refused in the
cluster instead. Finding out at boot costs a reboot and returns the
machine on its old manifest with a rejection record. The gate sat
after the drift check and no network-only edit ever reached it. With
drift measured, it does its job.

## What the lab proved

The drill that found the bug was run again on a two-machine cluster
installed from blank disks, and it now behaves the way the schema
says. Adding a nameserver to node-4's uplink was staged, not
declared converged. On `rebootPolicy: Auto` the operator staged the
manifest, cordoned the node, drained it, and rebooted it inside a
minute; the console showed the next boot running under the staged
manifest, and `status.network` carried the new nameserver on the
interface that asked for it.

The Manual case is the one that matters more, because Manual is the
default. With the policy set to Manual, the same edit reported
`RebootPending` with the diff in the message: `network: eth1:
nameservers 10.10.0.1 declared, (none) actuated`. The machine held
that verdict for as long as it was watched, with its boot time
unmoved, and the fleet listing read UpdatePending rather than
Degraded. Nothing rebooted until a person changed the policy.

The invalid spec is refused where it costs nothing. A manifest with
three interfaces, two of them naming `eth1`, produced
`StagingRejected: interfaces 1 and 2 both declare eth1; declare each
port once`, and the machine read Blocked. Nothing was staged, and the
running machine kept the network it booted with.

The diffs speak in the words the manifest used. node-1 names its
cluster port by MAC address, and its staged edit reported `network:
MAC 52:54:00:4c:4c:01: nameservers 10.10.0.4 declared, (none)
actuated`, not a kernel name the person never wrote. Reverting that
edit withdrew the staged manifest on the next pass, with the operator
reporting that the cluster's copy matches this boot again, and the
leader never rebooted at all.

The passes are quiet when nothing changed. A machine with no network
edit stayed Converged for the whole drill, and re-applying a spec
that was already staged wrote nothing: the operator logged one
staging line per distinct set of bytes, and none for the repeats.

## The manual

The Machine reference regenerates from the schema, so the schema is
where the false promise lived and where the correction goes.
`spec.network` now says what happens to an edit: the cluster cannot
apply it live, because it reaches the machine over the addresses the
edit changes, so the edit stages like a storage edit and the next
boot applies it, with `rebootPolicy` saying who starts that boot. The
spec's own summary listed network apart from the staged fields, and
now lists it with them. `status.boot.network` documents itself as the
drift reference, including what its absence means, because an
operator reading a status needs to know that a field which is not
there is a different claim from a field that is empty.
