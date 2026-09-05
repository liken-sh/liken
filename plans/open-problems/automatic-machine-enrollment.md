# Automatic enrollment of undeclared machines

Open design question. `liken.machine=` selects a machine that already
has a declaration. An undeclared machine has no equivalent path to choose
its identity, receive an address, and join without operator approval.

## Candidate approach

A `Machine` template on the `Cluster` could supply the configuration for
first boot. An unknown machine would combine that template with its
hardware facts to produce a declaration.

The original candidates were a name derived from a MAC address and an
address selected from a pool through ARP probing. The storage-claiming
analogy is useful for conflict handling: inspect what exists and refuse
an ambiguous claim. It does not establish that the same identity or
allocation rules are safe over a network.

A MAC address is a possible naming input, not an authentication
credential. It can be changed, duplicated, or spoofed. ARP probing can
detect some address conflicts, but a silent device or simultaneous claims
can defeat a simple probe-then-assign sequence. ARP replies are not
authenticated either. Neither candidate is a selected design.

## Dependency on supervised enrollment

Milestones [50](../50-netboot-for-a-declared-machine.md) and
[51](../51-enrollment-over-the-network.md) propose the supervised flow:
a machine boots over the network, submits a report, and a person applies
the proposed `Machine`. The proposed `Enrollment` record could also be
useful to automation.

Automatic enrollment replaces that final approval with a rule. It should
wait until the supervised flow is proven, so its identity and admission
requirements are understood before unattended admission is added.

## Remedy scope

**Broader identity, admission, and address-allocation design.** This is
new behavior, rather than a missing guard in an existing enrollment path.

The decisions are which machines may enroll automatically, what evidence
establishes their identity, and which network participants are trusted.
The design must also define address ownership, duplicate identities,
concurrent enrollment, and when a person must resolve an ambiguous case.

Duplicate checks, bounded retries, conflict detection, and an audit
record are implementation safeguards. They cannot decide who is allowed
to join or make a spoofable identifier into proof of authorization.

## Verification needed

After selecting a design, test duplicate and spoofed identifiers,
simultaneous address claims, an unreachable existing address owner, and
retries after interrupted enrollment. Verify that an unauthorized
machine receives no cluster credentials and that supervised enrollment
still requires its existing approval step.
