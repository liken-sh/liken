# Tests that check less than their names say

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Two tests check no outcome of the behavior that their names state.

- `TestBusDropsAPublishWhileDisconnected` in `bus_test.go` calls
  `Publish` on a bus that never connected and ignores its result. Its
  comment says only that the call does not panic. `Publish` returns
  `false` for a dropped publish, and callers depend on it:
  `publishedStatuses.publishDerived` records a payload only when
  `Publish` returns `true`, so that a dropped status is sent again.
- `a_client_with_no_bus_ignores_a_back_press` in `idle/src/client.rs`
  sends a back press to a client with no bus and asserts only that
  `client.bus` is still `None`. The press cannot change that field, so
  the test checks only that the call does not panic.

## Evidence

The two test bodies, in `bus_test.go` and `idle/src/client.rs`.

## What would confirm it

Change `Publish` to return `true` on a nil queue, and change the
client to act on a back press without a bus. Each test still passes.

## What a fix has to decide

What each test asserts: for the bus, that `Publish` returns `false`;
for the client, the state that a back press would change if the
client acted on it, such as the screen or the next frame.
