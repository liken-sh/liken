# Make disruption approvals one-shot

Open bug and design question, low priority. The documentation says that
one approval permits one disruption. The implementation leaves the
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

The hash depends only on the configuration bytes. It does not record
which request for those bytes the person approved. No component consumes
this annotation.

## Replay sequence

1. A person approves configuration A. The annotation contains `H(A)`.
2. The machine applies A. The annotation remains unchanged.
3. The machine later applies B through a direct reboot or an `Auto`
   path without replacing the annotation with `H(B)`.
4. A is requested again under `Manual`, with the same canonical bytes.
   Its pending hash matches the old annotation and permits another
   disruption, although nobody approved this new request.

The machine must actually apply B. If B is only staged and then reverted
while the machine still runs A, there is no drift back to A and no
replay. An explicit approval of B normally replaces the annotation and
prevents reuse of A's grant. Any policy changes in the sequence must also
return the hashed document to A's exact bytes.

The replay reuses approval for content that a person already approved.
It does not show a hash-collision attack, and it does not let a machine
apply different unapproved content. The risk is an unexpected repeat
disruption.

`liken.sh/request-reboot` uses a different rule: it names the current
`BootID`. The deterministic manifest-hash replay described here does not
apply to it.

## Evidence

This review read the annotation comparison, the CLI write, and the
disruption gate. It did not run a replay on a live cluster or a full
lifecycle fixture. A regression test must exercise the whole sequence,
including B being applied without replacing the annotation.

## Candidate remedies

Bind approval to a pending operation as well as its document, or consume
it through an authorized writer after the operation completes. A boot
identity, generation, or nonce could be part of an operation identity.
Check restart-only changes and multiple operations in one boot before
choosing any of them.

Local bookkeeping could prevent some repeats without new write
permissions. It cannot simply block a hash forever, because a person
must still be able to approve those same bytes again explicitly. The
current annotation value alone cannot distinguish that new approval from the
old value that is still present.

Clearing annotations through the machine operator would require additional
`machines` write permission. Granting every node broad spec-write access
to consume approvals would introduce a different security issue.

## Remedy scope

The fix is a decision about how to represent approvals, and about
compatibility with existing ones. One-shot behavior is what the
documentation already describes, so the meaning of an approval stays the
same. The design question is how to represent and consume each approval,
while a person can still approve the same bytes again and permissions
stay narrow.

A new annotation format or CLI output affects existing automation and
GitOps documents. A new consuming writer affects RBAC. The design must
state how old approvals behave and how reboot and restart operations
share the mechanism. The alternative is to treat an approval as a
permanent authorization for its bytes. That would change the documented
behavior and needs an explicit product decision. This problem does not
assume it as the fix.

## Tests needed

Exercise the full replay sequence and both non-replay cases above. Verify
that a fresh approval of the same bytes works later, including after a
restart-only operation. Cover legacy annotations, repeated GitOps applies,
and interruption between applying a change and recording consumption.
