# Enrollment over the network

Milestone 51. Proposed. It would send a netbooted machine's
hardware report to the cluster as an Enrollment, and make approval
an edit-and-apply of the proposed Machine, with a CLI verb as sugar.
It builds on milestone 50 and changes nothing in its serve rule.

## The problem

Milestone 50 leaves one manual step in the middle of an otherwise
hands-off join: the operator reads the hardware report at the
machine's console and copies facts from a screen into an editor. The
report already exists as structured data. Milestone 36 built it to
propose a Machine document; the console is only where the proposal
stops when there is no stick to write it to. This milestone gives
the proposal a network path to the cluster and gives the operator a
place to act on it.

The README names claiming unknown machines as an open problem. This
milestone is the supervised half of the answer: the unknown machine
presents itself, and a person decides. The unsupervised half, a
Machine template that lets a node claim an identity with no person
in the loop, stays open, and it would build on the same Enrollment
records.

## The Enrollment CRD

An Enrollment holds one machine's proposal. It is a new
cluster-scoped kind in `liken.sh/v1alpha1`, one per enrolling
machine, named from the machine's MAC. The controller writes it and
the operator only reads it, so the proposal is in `status`,
following the rule that the system owns status. Status holds the
proposed Machine document and the hardware report behind it, so the
operator can read why the proposal says what it says.

There is no `approved` field. Approval is the Machine's existence:
the operator applies a Machine, the serve rule from milestone 50
reads a declared MAC, and the next boot installs. A flag would be a
second copy of that fact, and the two copies would disagree the
first time someone applied a Machine directly.

## The endpoint

The controller gains a fourth listener beside proxyDHCP, TFTP, and
HTTP for artifacts. It accepts one POST: a hardware report. The
controller validates the report's shape, converts it to an
Enrollment, and answers with nothing the machine acts on. A repeat
POST from the same MAC updates the same Enrollment, so a machine
that reboots five times while it waits does not litter the API.

The POST is unauthenticated, and that is safe because a proposal is
inert. Nobody acts on an Enrollment until an operator applies a
Machine, so the worst an intruder on the segment can do is create
Enrollments, which the operator can list and delete. The trust
boundary is the layer-2 segment, the same boundary milestone 50
states for the installer payload.

## The report gains one output

The report boot writes to the console and, when it booted from a
stick, to the stick. When it booted from the network there is no
stick, so it posts the report to the server it booted from, whose
address the boot already holds. The console print stays in every
case. That keeps console parity: the operator at the screen and the
operator at the API read the same facts.

## The CLI verb

`liken enrollments` lists what waits, with the MAC, the proposed
name, and the age of each. An approve verb opens the proposed
Machine in the operator's editor and applies the result, because a
proposal is rarely perfect: storage roles and addresses need
choices a report cannot make. The verb is sugar over edit-and-apply,
and an operator without the CLI can do the same with `kubectl`.

After the Machine exists, the controller marks the Enrollment
adopted and keeps it as a record of what the machine reported on the
day it arrived.

## Verification

Unit tests cover the conversion from report to Enrollment, the
update on a repeat POST, and a malformed report, which is discarded
with a log line, not stored.

On the netboot-cluster: an unknown guest boots to the report, the
Enrollment appears with its proposal, the approve verb edits and
applies it, and the same guest installs and joins with no media and
no console typing. A second drill posts the same report twice and
counts one Enrollment.

## The manual

The Enrollment schema regenerates into a reference page the way the
Machine and Cluster schemas do, so its descriptions are written as
manual text. The guide from milestone 50 replaces its console step
with the enrollment flow. The CLI reference gains the new verbs.

## Not in this milestone

**Claiming without a person.** A Machine template that turns an
Enrollment into a Machine with nobody in the loop is the open
problem's other half. It waits until the supervised flow is proven.

**Authenticating the POST.** A signed report would need a key the
machine does not have yet, because identity is what enrollment
creates. The hardening tier owns that question.
