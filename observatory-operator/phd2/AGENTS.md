# Working on the PHD2 client

The package doc comment in `client.go` describes the client and names
the protocol reference. `astrophotography-operator` imports this
package too, so a change to an exported name or a behavior also runs
that component's tests. The package imports only the standard
library.

## How PHD2 behaves

- PHD2 sends `AppState` only to a new connection. So the client works
  out the state from the events that imply one, and asks for the
  state again after a stop.
- `phd2test` replies to the methods that guiding uses. Its events and
  error messages come from `src/event_server.cpp` and `src/phdcontrol.cpp`
  in `OpenPHDGuiding/phd2`, and from the transcript in `testdata/`
  that a real PHD2 sent.

The `testing` skill under `.agents/skills` describes how the tests of
this package and of the operators use `phd2test`.
