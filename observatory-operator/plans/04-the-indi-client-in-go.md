# 04, The INDI client in Go

Proposed on 2026-10-05. Not built.

## The problem

The operator, the reconciler, and `astrophotography-operator` all
speak INDI. No Go client is maintained:
[`goastro/indiclient`](https://github.com/goastro/indiclient) last
changed in March 2020, and its fork `jnmorley/indiclient` in October
2023. Both target `indiserver` 1.7.

## The requirement

A Go package speaks the INDI wire protocol directly, with no cgo, the
way `equipment-operator/cec/` speaks the kernel's CEC API. It follows
the repository's rule for events: it sends `getProperties`, takes the
`def*Vector` messages as the baseline, and applies each `set*Vector`
after that. When the connection fails, it connects again and takes the
baseline again.

It keeps each property's definition as the device sent it: the type,
the permission, the switch rule, the limits, and the format. It never
sends `enableBLOB` unless a caller asks for BLOBs, so a client that
reads frame paths receives no frame data.

The package is shared by two components, so where it lives is a
question for this plan. The rule against a shared junk directory
applies: it is an INDI client, and its name says so.

## How we test it

Go tests run against a real `indiserver` with the simulators from plan
02, in a container. They cover the baseline, updates, a server that
restarts, and a driver that restarts behind the server.

## Upstream issues

- The protocol version is still 1.7: `INDIV` in
  `libs/indicore/indiapi.h`. INDI 2.x added shared buffers and Unix
  sockets between the server and its drivers, which change no message
  that a client receives over TCP.
- [indi#1528](https://github.com/indilib/indi/issues/1528): the C++
  client kept the last BLOB in memory after a driver restarted. The Go
  client releases a BLOB once its caller has read it.

## References

- The INDI protocol: <https://docs.indilib.org/protocol/>
- The client side of `libindi`: `libs/indiclient/` in `indilib/indi`
- [Root plan 74](../../plans/74-astrophotography.md), "The INDI client
  in Go"
