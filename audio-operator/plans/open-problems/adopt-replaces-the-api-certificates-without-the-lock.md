# `adopt` replaces the API's certificates without the lock

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

`certificates` in `audio-operator/apicertificates.go` holds the CA and
the leaf that audio-api serves. Its field `mu` guards `authority` and
`public`, because `keepCertificates` replaces them in its own goroutine
while each forwarded request reads the CA. `setKeys` takes `mu` before
it writes both fields, and `keys` takes `mu.RLock` before it reads
them. `anchor`, which the forwarder calls for the CA that it trusts,
and `nearestExpiry` read through `keys`.

`adopt` writes `c.public` and `c.authority` with no lock. `ensure`
calls `adopt` when the `audio-api-tls` `Secret` exists, and
`mintAuthorityAndLeaf` calls it when another API pod created the
`Secret` first. `ensure` holds `writes`, which serializes the writers,
but not `mu`, so a forwarded request that calls `anchor` during
`adopt` reads the fields while they change. The two writes are also
separate, so a reader can get the new leaf with the old CA, which the
`mu` comment says must not happen. When the `Secret` has no `ca.key`,
or when `readAuthority` fails, `adopt` has already replaced `c.public`
before it returns.

## What would confirm it

A test that calls `ensure` on an existing `Secret` while another
goroutine calls `anchor`, run with `go test -race`. The race detector
reports the race when the two goroutines overlap.

## What a fix has to decide

The simplest fix builds the CA and the leaf in local variables and
calls `setKeys` once at the end, so a reader sees the old pair or the
new pair. A fix also has to decide whether a `Secret` that `adopt`
refuses leaves the old pair in place, which is what a reader of the
error would expect.
