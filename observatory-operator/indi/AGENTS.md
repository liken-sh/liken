# Working on the INDI client

The package doc comment in `doc.go` describes the client.
`astrophotography-operator` imports this package too, so a change to
an exported name or a behavior also runs that component's tests. The
package imports only the standard library.

## How the server and the simulators behave

The client depends on each of these behaviors. A test in this package
fails when the client stops handling one of them.

- When `indiserver` gets an `enableBLOB` that names a device before
  any `getProperties`, it treats it as a request for that device
  alone. So the client sends `getProperties` first.
- A `delProperty` with no `name` deletes the device. A `delProperty`
  with an empty `name` deletes nothing. The receiver simulator sends
  one on disconnect.
- A device can define a property twice. It can also update a property
  that it has not defined yet. The client's store replaces the first
  definition in place, and ignores an update to a property that is
  not defined.
- INDI has no request identifier. So `Settle` finds the reply to a
  change among the updates that arrive after it. An update counts as
  the reply when it is not Busy, and either a Busy update came first
  or it contains the values that the client sent. The dome simulator
  sends its position on each poll. Without the second condition, the
  wait would end before the dome turns.

## Finding things

- `inditest/testdata/` contains the bytes that a real `indiserver`
  sent with one simulator behind it, for each simulator that a kind
  runs: `baseline.xml`, `connect.xml`, `disconnect.xml`, and more
  steps for the focuser and the dome. `capture.SimulatorScript` in
  `capture/` records the message that each file replies to.
  `replay_test.go` serves the files to a client. A client that sends
  a wrong message gets no reply.
- `standard/` contains the captures of the standard properties of
  each INDI interface, made from the bare drivers in
  `standard/bare/`.
- The `observatory-schema` skill under `.agents/skills` has the
  commands that record the transcripts and the captures again after a
  bump of the INDI images. The `testing` skill describes the fake
  `indiserver` in `inditest`.
