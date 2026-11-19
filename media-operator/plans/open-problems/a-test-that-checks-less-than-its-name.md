# A test that checks less than its name says

Open bug, recorded on 2026-11-18. A mutation of the code reproduced it
on 2026-11-19.

`a_client_with_no_bus_ignores_a_back_press` in `idle/src/client.rs`
sends a back press to a client with no bus and asserts only that
`client.bus` is still `None`. The press cannot change that field, so
the test checks only that the call does not panic.

## Evidence

The test body in `idle/src/client.rs`. A change to `Client::receive`
that records an arrival in the unit on a back press when the client
has no bus leaves the test passing: `cargo test` reports
`a_client_with_no_bus_ignores_a_back_press ... ok`. The change is not
in the tree.

## What a fix has to decide

What the test asserts, so that it fails when a client with no bus
acts on back: the state that a back press would change if the client
acted on it, such as the screen or the next frame.
