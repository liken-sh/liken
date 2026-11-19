# The recorder cuts reason and message to limits that do not apply

Open bug, recorded on 2026-11-18. A unit test reproduced the cut on
2026-11-19, and the upstream validation was read the same day.

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
- `pkg/apis/core/validation/events.go` on the `master` branch of
  kubernetes/kubernetes, read on 2026-11-19, defines
  `ReasonLengthLimit = 128` and `NoteLengthLimit = 1024`.
  `legacyValidateEvent` checks `len(event.Reason)` and
  `len(event.Message)` against them only in the branch for an `Event`
  whose `EventTime` is set. For an `Event` with no `EventTime`, it
  checks only that the namespace matches the involved object.
  `ValidateEventCreate` adds no other check for a request to core/v1.

## The reproduction

A test in `kubernetes/events` sends a `Normal` `Event` with a message
of 2000 bytes through the recorder to the fake API server, and asserts
that the server holds the whole message. It failed with:

```
the server holds 1 Events, the first with a message of 1024 bytes, want 1 Event with the whole message of 2000 bytes
```

The test is not in the tree, because a failing test cannot land on
`main`. The fake API server applies no validation, so the test shows
the cut and not what the API server accepts. A post of a core/v1
`Event` with no `eventTime` and a 2000-byte message to the API server
of the `lab` fleet, read back, would show that too.

## What a fix has to decide

A person who runs `kubectl describe` reads a shorter message than the
component wrote, and the API server would accept the whole message.

- Whether to remove the cut, or to keep a limit for another reason,
  such as the size of the object in etcd, and state that reason at
  the constants.
- The recorder test of reason and message length changes with that
  decision.
