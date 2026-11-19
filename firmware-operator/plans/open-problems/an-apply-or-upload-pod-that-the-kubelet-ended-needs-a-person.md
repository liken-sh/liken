# An apply or upload pod that the kubelet ended needs a person

Open design question, recorded on 2026-11-19. A unit test reproduced
it on 2026-11-19.

When the kubelet ends a scan pod or a rehearsal pod, the operator runs
the same scan or rehearsal again in a next pod, and the person who
approved the update does nothing. When the kubelet ends an apply pod
or an upload pod, the operator never runs it again, because each of
them writes to a device.

So when the kubelet rejects an apply pod at admission, for example
with `OutOfcpu`, or evicts it before the operator sends the signal to
write, the machine's record in `status.applies` is `Failed`, and the
machine leaves its reboot turn. The pod wrote nothing, and the machine
still needs the update, but the record stays `Failed` until a person
withdraws the approval and approves it again. An upload pod that the
kubelet ended leaves its entry in `status.devices` `Failed` until the
device enumerates again, after a power cycle or a replug, or until a
person changes the `Firmware`'s spec.

## The reproduction

A test built the world that the existing apply tests use,
`rehearsedWorld`: a machine whose rehearsal passed and whose apply pod
waits for the turn, with no signal. In a `synctest` bubble, the fake
API server ended the apply pod as the kubelet does at admission, with
the phase `Failed`, the reason `OutOfcpu`, and no log. The test waited
5 minutes on the bubble's clock, which is longer than the longest wait
before a next scan pod. It asserted that the record was not `Failed`,
and that one apply pod existed. It failed with:

```
the record is Failed after the kubelet rejected the apply pod before the signal: the apply pod ended before the signal to write, so nothing was written: the apply pod wrote no log: OutOfcpu: Node didn't have enough resource: cpu, requested: 10, used: 3990, capacity: 4000, want the apply to run again
0 apply pods after the rejection, want a next pod
```

The test is not in the tree, because a failing test cannot land on
`main`. The tests that show the behavior as it is now pass:
`TestAPodThatEndsBeforeTheSignalWritesNothing` in
`applysignal_test.go`, and the tests in `kubeletend_test.go`.

## The cause

- `retryable` (`retry.go:64`) is read only by the scan follower
  (`scanfollow.go:138`) and the rehearsal follower
  (`rehearsal.go:368`). The comment at the top of `retry.go` gives the
  reason that apply and upload pods are not run again.
- `judgeUnsignalled` (`applyfollow.go:261`) judges every end of an
  apply pod before the signal as `Failed`, whatever `endedByKubelet`
  (`pod.go:259`) reports.
- A `Failed` apply record stays until a person withdraws the approval
  (`apply.go:41`).

## The hazard

A review of an earlier attempt proved this: at a node shutdown, the
kubelet ends the pod before the `Machine` reports the new boot. A next
pod that the operator creates at once belongs to the old boot. It
waits through the reboot, and it can then get the turn and the signal
on a boot that no rehearsal ran against. `endedAtShutdown`
(`pod.go:274`) reads the reasons of a graceful shutdown, and a scan or
a rehearsal with one of them is not run again. A pod that the kubelet
evicted or rejected for another reason just before the node shuts
down carries none of them, and its next pod meets the same reboot.
`signal`
(`applyfollow.go:74`) and `startWithSignal` (`applyfollow.go:104`) do
not compare the record's `boot` with the boot that the `Machine`
reports. Any design must handle this.

## The decision

Should the operator run an apply or an upload again after the kubelet
ended its pod, and when?

- **Run again only a pod that the kubelet rejected at admission.** A
  pod that the kubelet refused before any container started, with a
  reason such as `OutOfcpu` or `OutOfmemory`, cannot have written.
  The operator runs such a pod again with the waits of a scan, and at
  the signal it checks that the `Machine` still reports the boot of
  the record, and fails the record if it does not. An eviction after
  the containers started still needs a person. This is the safest
  form, but it needs the boot check at the signal, and a test of the
  shutdown order above.
- **A retry annotation that a person sets.** The operator runs the
  apply or upload again when a person sets an annotation on the
  `Firmware`, and removes it after. A person still acts, but with one
  command in place of a withdrawal and a second approval. The machine
  rehearses again only if the boot changed.
- **Leave it.** The manual's `troubleshoot.md` already says that the
  record is `Failed` and that a person approves again. It costs
  nothing to build, and each rejection costs a person a withdrawal, an
  approval, and a rehearsal.
