# Enrollment over the network

Milestone 51. Proposed. It would send a netbooted machine's
hardware report to the cluster as an Enrollment. An operator would
approve the machine by editing and applying the proposed Machine,
and a CLI verb would do those two steps in one command. It builds on
milestone 50 and changes nothing in its serve rule.

## The problem

Milestone 50 leaves one manual step in the middle of an otherwise
hands-off join: the operator reads the hardware report at the
machine's console and copies facts from a screen into an editor. The
report already exists as structured data. Milestone 36 built it to
propose a Machine document. When there is no stick to write the
proposal to, the report boot can only print it on the console. This
milestone sends the proposal over the network to the cluster, and
gives the operator a resource to act on.

The README names claiming unknown machines as an open problem. This
milestone is the supervised half of the answer: the unknown machine
sends its report, and a person decides. The unsupervised half, a
Machine template that lets a node claim an identity with no person
in the loop, stays open, and it would build on the same Enrollment
records.

## The Enrollment CRD

An Enrollment contains one machine's proposal. It is a new
cluster-scoped kind in `liken.sh/v1alpha1`, one per enrolling
machine, named from the machine's MAC. The controller writes it and
the operator only reads it, so the proposal is in `status`,
following the rule that the system owns status. Status contains the
proposed Machine document and the hardware report behind it, so the
operator can read why the proposal says what it says.

There is no `approved` field. The operator approves a machine by
applying a Machine for it: the serve rule from milestone 50 then
finds a declared MAC, and the next boot installs. A flag would be a
second copy of that fact, and the two copies would disagree the
first time someone applied a Machine directly.

## The endpoint

The controller gets a fourth listener beside proxyDHCP, TFTP, and
HTTP for artifacts. It accepts one POST: a hardware report. The
controller validates the report's structure, converts it to an
Enrollment, and sends a response that the machine does not act on.
A repeat POST from the same MAC updates the same Enrollment, so a
machine that reboots five times while it waits does not create extra
objects in the API.

The POST is unauthenticated. That is safe because an Enrollment
changes nothing by itself: nothing acts on it until an operator
applies a Machine. So the worst an intruder on the segment can do is
create Enrollments, which the operator can list and delete. The trust
boundary is the layer-2 segment, the same boundary milestone 50
states for the installer payload.

## Send the hardware report to the boot server

The report boot writes to the console and, when it booted from a
stick, to the stick. When it booted from the network there is no
stick, so it posts the report to the server it booted from, using the
server address recorded during boot. It still prints the report to
the console, so the person at the machine and the person reading the
API receive the same facts.

## The CLI verb

`liken enrollments` lists what waits, with the MAC, the proposed
name, and the age of each. An approve verb opens the proposed
Machine in the operator's editor and applies the result, because a
proposal is rarely correct as it is: storage roles and addresses need
choices a report cannot make. The verb only combines the edit and the
apply, so an operator without the CLI can do the same with `kubectl`.

After the Machine exists, the controller marks the Enrollment
adopted and keeps it as a record of what the machine reported on the
day it arrived.

## Verification

Unit tests cover the conversion from report to Enrollment, the
update on a repeat POST, and a malformed report. The controller
discards a malformed report with a log line and does not store it.

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
machine does not have yet, because the machine gets its identity
only through enrollment. That question belongs to the hardening tier.
