# A deleted `Secret` keeps a provider `Ready`

The operator reads a keyed `MetadataProvider`'s `Secret` only when the
provider's check call is due (`providercadence.go`). After a
`Reachable` verdict, the next call is due in an hour. The operator has
no watch on the `Secret`: a person names it, no label selects it, and a
watch that could select it needs list and watch on every `Secret` in
the cluster.

A person who deletes the `Secret`, or removes its key, leaves the
provider `Ready` for up to that hour. Every library `Job` the operator
creates in that time names the `Secret` in a `secretKeyRef`
(`providerKeyEnv` in `providerenv.go`). The kubelet cannot resolve the
reference, so the pod stays `Pending`, and its container waits with the
reason `CreateContainerConfigError`. The operator runs one `Job` of a
`Library` at a time, so that `Job` holds every other `Job` of the
`Library` until its deadline, `libraryJobDeadline`, two hours after it
was created. After five minutes the `Library`'s `Ready` condition names
the pod with `JobNotStarted` and the kubelet's event, so a person can
read the cause. The next check call writes `NoSecret`, and the `Job`s
created after it leave the provider out.

## Why it matters

One edit of a `Secret` can stop every `Library` that names the provider
for up to three hours: up to one hour until the check, and then up to
two hours until the `Job` that was created before the check reaches its
deadline. The same hour applies to a key that a person replaces with a
key the provider refuses. That `Job` starts, and the phase gets `401`
answers.

## What a fix could look like

- **Keep the hour.** This is the current choice, made on 2026-10-07. A
  person rarely deletes or replaces a provider's `Secret` while a
  `Library` that names it is active, and after five minutes the
  `Library`'s `Ready` condition names the stuck pod and the kubelet's
  event.
- **A pod that cannot resolve a key makes the check due.** The
  operator already watches the pods of its library `Job`s. A pod whose
  container waits with `CreateContainerConfigError` names, in its own
  spec, each `Secret` it references. The pass could drop the call note
  of each provider whose `Secret` the pod names, so the next pass reads
  that `Secret` and writes `NoSecret`. The pass must do this once for
  each pod uid. A pod that stays stuck must not make a call on every
  pass, because a provider whose `Secret` exists would then spend its
  allowance on the check. This closes the window for the next `Job` in
  seconds. It does not free the `Job` that is stuck. Deleting that
  `Job` is a separate decision.
- **`optional: true` on each provider's `secretKeyRef`.** The pod then
  starts with an empty variable, and the phase sees no key. Each phase
  must then treat an empty key as a provider it does not ask, and the
  `Library`'s status must still say why a fact was not filled.
- **A label on the `Secret`.** A watch on `Secret`s that carry a label
  the operator names costs nothing for other `Secret`s. It asks the
  person to label every provider `Secret`, and an unlabeled `Secret`
  falls back to the hour.

## What is not known

- How often a person deletes or replaces a provider's `Secret` while a
  `Library` that names it is active. No drill has measured this on
  `liken-1`.
- Whether the kubelet's container reason is the same on every
  Kubernetes version the project supports, which the first option
  depends on.
