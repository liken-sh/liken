# 13, Shared endpoints

Plan 13. Built on 2026-10-25. It closes the open problem "Every
published sink is exclusive, though PipeWire can share one". The drill
on the `liken-1` testbed has not run yet: two shared claims on one
speaker, an exclusive claim that waits for both, and a shared claim
that states a codec.

A cluster owner can let several claims hold one endpoint at the same
time, with `spec.sharedClaims` on its `Sink` or `Source`. PipeWire
mixes the streams of every claim into the sink. An endpoint with no
count stays exclusive, as every endpoint was before this plan.

## The problem

PipeWire mixes streams, so one sink can serve several consumers at
once. Every device this operator published was exclusive, so the
second pod to claim a monitor's speakers stayed `Pending` while the
first pod held them.

Milestone 59 chose exclusive for two reasons, and both still apply.
The other two device operators are exclusive, so one owner for one
piece of hardware gives a claim a clear meaning. And a claim on a
shared sink gives a workload no control over what else plays through
it: a video player that shares a sink with a notification sound is a
worse default than a video player that waits. These reasons make
exclusive the right default. They do not make a shared sink wrong.

## The decision

Exclusive stays the default for each endpoint and for each claim. A
share happens only when the cluster owner allows it on the endpoint
and the claim asks for it.

- **The cluster owner opts in, per endpoint.** `spec.sharedClaims` on
  a `Sink` or a `Source` is the number of claims that can hold the
  endpoint at once, 2 or more. An HDMI output to a television and the
  analog jack to a desk speaker can need different answers, and only
  the cluster owner knows which. No default ships, because no limit
  of the hardware or of PipeWire sets the number. PipeWire opens the
  ALSA device once and mixes every stream into it, and a Bluetooth
  speaker's sink mixes before it encodes.
- **The scheduler enforces the count.** A device whose resource
  declares the count publishes `allowMultipleAllocations: true` and a
  capacity named `claims` with that value, and no `requestPolicy`.
  This is Kubernetes' consumable capacity, KEP-5075, behind the
  `DRAConsumableCapacity` gate. The gate is beta and on by default
  since Kubernetes 1.36, and 1.37.1 leaves it unchanged
  (`pkg/features/kube_features.go` at `v1.37.1`). `liken` already
  depends on it, because its GPU devices set
  `allowMultipleAllocations`.
- **A claim asks for a share.** With no request policy, the scheduler
  charges a request that asks for none of a capacity all of it. So a
  claim written for an exclusive endpoint takes the whole device on a
  shared one, and no share starts while it holds the device. A request
  with `capacity.requests.claims: "1"` shares the device with the
  other requests that ask for one. This answers the question of an
  exclusive claim and a shared claim on one device: they never hold
  it at the same time, and the scheduler refuses the second one.
- **No second `DeviceClass`.** Milestone 59 named a second class over
  the same devices as the obvious extension. A class cannot make a
  device shared or exclusive, and the request's capacity can, so the
  existing classes stay.

What a share guarantees: its stream is mixed with the other shares'
streams. It guarantees nothing about levels. The endpoint's level is
one value, on the `Sink`'s spec, and each share sets its own stream's
volume.

## What changes in the operator

- **The slice.** `shares.go` reads the count from each of the
  machine's `Sink`s and `Source`s, from the watches' stores, and
  marks each device before the pass writes the slice. A spec change
  wakes the pass, as every spec change does. A read that fails keeps
  the counts of the last read, so a refused list does not turn a
  shared device exclusive for one pass.
- **The record of holders.** `preparedClaims` records a set of
  claims for each device. `status.claim` became `status.claims`, a
  list sorted by namespace and name, and the printer column became
  `CLAIMS`. The CRD is `v1alpha1`, so the field changed with no
  period of deprecation.
- **The unity write.** A prepare on a Bluetooth speaker writes unity
  on its sink, because any level it finds is a leftover from an
  earlier tenant. A share that arrives while another claim holds the
  speaker skips the write, because the other claim's stream plays at
  the level the speaker holds now.
- **The codec.** A share cannot state a codec, from the claim or from
  its `DeviceClass`. A codec switch rebuilds the speaker's sink, and
  every share plays through that sink. The prepare fails with a
  message that names the reason. A share is a claim whose request
  for the device asks for the `claims` capacity, which the driver
  reads from the claim's spec, so the rule holds before the slice is
  read again after an operator restart.

## Alternatives set aside

- **A shared endpoint by default.** Every endpoint could publish the
  capacity, since a claim that asks for none stays exclusive. That
  leaves no person who chose the count, and the count is policy.
- **A `requestPolicy` with a default of one claim.** Every claim with
  no capacity request would then share, which changes the meaning of
  every claim already written.
- **Sharedness from `consumedCapacity`.** The allocation records how
  much of the capacity each claim took, and a share takes less than
  all of it. The driver would need the device's count to compare,
  and the count it holds after a restart is empty until the first
  pass. The claim's own request answers without it.
- **Per-share volume.** A level for each share needs a mixer stage
  for each claim, and no workload asked for it.
