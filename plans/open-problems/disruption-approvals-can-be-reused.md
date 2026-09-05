# Make disruption approvals one-shot

Open bug and design question. Review priority: low. The documented
approval contract permits one disruption. The implementation leaves the
approval annotation in place and matches it against a deterministic
configuration hash. A later return to identical configuration can reuse
that approval.

## Current behavior

`ApproveDisruptionAnnotation` in [status.go](../../machine/status.go)
defines `liken.sh/approve-disruption`. Its comment says the hash makes
an approval one-shot without consuming the annotation.

The [CLI](../../cli/approve.go) writes the first 12 hexadecimal
characters of the selected pending hash. `ApprovalGrants` accepts a
matching full hash or a prefix of at least that length. `gateDisruption`
in [converge.go](../../machine-operator/converge.go) uses the match to
allow a `Manual` machine into the ordinary disruption path, including
the conductor's turn and, for reboots, draining.

A content hash identifies bytes. It does not identify the occasion on
which those bytes were approved. No component consumes this annotation.

## Replay sequence

1. A person approves configuration A. The annotation contains `H(A)`.
2. The machine applies A. The annotation remains unchanged.
3. The machine later applies B through a direct reboot or an `Auto`
   path without replacing the annotation with `H(B)`.
4. A is requested again under `Manual`, with the same canonical bytes.
   Its pending hash matches the old annotation and permits another
   disruption without approval for this occasion.

B must actually have been applied. Merely staging B and reverting while
the machine still runs A creates no drift back to A and no replay.
Explicitly approving B normally replaces the annotation and prevents
reuse of A's grant. Any policy changes in the sequence must also return
the hashed document to A's exact bytes.

This reuses approval for previously authorized content. It does not
demonstrate a hash-collision attack or permission to apply different
unapproved content. The risk is an unexpected repeat disruption.

`liken.sh/request-reboot` uses a different rule: it names the current
`BootID`. It is not the deterministic manifest-hash replay described here.

## Evidence

The annotation comparison, CLI write, and disruption gate were inspected
statically. No live-cluster replay or full lifecycle fixture was run.
A regression test must exercise the whole sequence, including B being
applied without replacing the annotation.

## Candidate remedies

Bind approval to a pending operation as well as its document, or consume
it through an authorized writer after the operation completes. A boot
identity, generation, or nonce could contribute to an operation identity,
but none should be chosen without checking restart-only changes and
multiple operations in one boot.

Local bookkeeping could prevent some repeats without new write
permissions. It cannot simply block a hash forever: a person must still
be able to approve those same bytes again explicitly. The current
annotation value alone cannot distinguish that new approval from the
old value still present.

Clearing annotations through the machine operator would require additional
`machines` write permission. Granting every node broad spec-write access
to solve approval consumption would introduce a different security issue.

## Remedy scope

**An approval-representation and compatibility decision.** Restoring the
promised one-shot behavior does not require redefining the user's intent.
The design question is how to represent and consume each approval while
preserving explicit repeat approvals and narrow permissions.

A new annotation format or CLI output affects existing automation and
GitOps documents. A new consuming writer affects RBAC. The design must
state how old approvals behave and how reboot and restart operations
share the mechanism. Treating approvals as permanent authorization for
bytes would instead change the documented contract and needs an explicit
product decision; it is not assumed as the fix.

## Tests needed

Exercise the full replay sequence and both non-replay cases above. Verify
that a fresh approval of the same bytes works later, including after a
restart-only operation. Cover legacy annotations, repeated GitOps applies,
and interruption between applying a change and recording consumption.
