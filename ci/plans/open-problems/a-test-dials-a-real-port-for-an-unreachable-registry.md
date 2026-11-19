# A test dials a real port for an unreachable registry

Open bug, recorded on 2026-11-18. Checked against the code on
2026-11-19: the case dials a real port at `ci/bases_test.go:164`.
Whether the dial makes a run slow depends on the machine, and that
part is not checked.
A review of the comments against the code found it.

The root `AGENTS.md` says that a test reaches no host outside the
process. The case `"unreachable"` of
`TestTheBaseBuildsFromTheTreeWhenTheRegistryCannotBeRead` in
`ci/bases_test.go` sets the registry to
`Registry{Base: "http://127.0.0.1:1", Owner: "liken-sh"}`. The other
cases use `fakeRegistry`, which serves in the test process. This case
gives `Registry` no `Client`, so `http.DefaultClient` opens a real TCP
connection to port 1 of the machine that runs the test.

The case passes on any failure to read the registry, because every
failure gives "builds from the tree", so a refusal and another error
give the same result. The run time depends on the machine: a sandbox
or firewall that drops the packet in place of a refusal makes the
case wait up to 30 seconds, the dial timeout of
`http.DefaultTransport`.

## What would confirm it

Run the case with a listener on `127.0.0.1:1`, or in a network
namespace that drops packets to the port, and check whether the
result or the run time changes.

## What a fix has to decide

How the test makes a registry that cannot be reached without a real
port. One option is a `Registry` whose `Client` has a transport that
returns a connection error for every request. The `testing` skill
names `net.Pipe` and the in-memory transport of
`kubernetes/apiservertest` for a fake peer, but `ci/` is its own Go
module and does not import `kubernetes/`.
