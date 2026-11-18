# The recorder cuts reason and message to limits that do not apply

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

The `events` recorder cuts each `Event`'s reason to `maxReason`, 128
bytes, and its message to `maxMessage`, 1024 bytes, in
`kubernetes/events/recorder.go`. The API server applies those limits
only to an `Event` that has an `eventTime`, and the recorder never
sets one. So the cut protects against no rejection, and a message
longer than 1024 bytes loses its end for no stated reason. A person
who runs `kubectl describe` then reads a shorter message than the
component wrote.

## Evidence

- `recorder.go` calls `cut(reason, maxReason)` and
  `cut(message, maxMessage)` when it builds each `Event`.
- The doc of `Event` in `events/event.go` says that the recorder sets
  no `eventTime`, and that the API server limits the length of reason
  and message only on an `Event` that has one. The comment on
  `maxReason` and `maxMessage` says the same, and gives no other
  reason for the cut.
- A comparison of the comments with the validation in
  `pkg/apis/core/validation/events.go` of Kubernetes found the limits
  only on the path for an `Event` with an `eventTime`.

## What would confirm it

Post a core/v1 `Event` with no `eventTime` and a 2000-byte message
to the API server of the `lab` fleet, and read it back.

## What a fix has to decide

- Whether to remove the cut, or to keep a limit for another reason,
  such as the size of the object in etcd, and state that reason at
  the constants.
- The recorder test of reason and message length changes with that
  decision.
