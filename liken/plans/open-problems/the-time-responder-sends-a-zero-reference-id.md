# The time responder sends a zero reference ID

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

A leader that serves SNTP to the other machines sends a reference ID
of zero when its own time source is configured as a hostname or as an
IPv6 address. RFC 5905, section 7.3, sets the reference ID above
stratum 1 to the upstream server's IPv4 address, or to the first four
bytes of the MD5 hash of its IPv6 address. A client uses the field to
detect a timing loop, so a zero value hides a loop between
servers.

## The evidence

`respondTo` (`init/responder.go`, lines 106 to 119) copies the source
into bytes 12 to 16 only when `net.ParseIP(lastSync.source)` returns
an IPv4 address. For any other source the bytes stay zero. The comment
above the switch states the zero for a hostname. `timeSync`
(`init/time.go:80`) records the source as configured, not the address
that answered.

## What would confirm it

A unit test of `respondTo` with a synced clock whose source is a
hostname, and one whose source is an IPv6 address, that reads bytes
12 to 16 of the reply.

## What a fix has to decide

Whether `timeSync` records the address that answered, so the responder
can send it, and whether the responder follows RFC 5905 for an IPv6
source.
