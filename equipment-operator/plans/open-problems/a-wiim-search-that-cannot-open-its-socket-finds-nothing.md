# A WiiM search that cannot open its socket finds nothing

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

`browseMDNS` and `searchSSDP` in `wiim/discovery.go` return with no
log and no error when the socket does not open or the query write
fails. `parseMDNS` also returns silently on a packet that does not
unpack. `wiim.Discover` returns only a list of devices, so its caller
cannot tell a failed search from a search that found no amp.

`discovery.store` in `discovery.go` counts each call as a full
search. Every amp that the search did not return counts one miss, and
after `discoveryMisses` searches in a row `pruneReason` returns a
reason, and discovery deletes the `Receiver` that it created for the
amp. So a pod whose socket fails for that many searches, about
`discoveryMisses` times `discoveryInterval`, deletes every discovered
`Receiver`, and the log names only the misses.

## Evidence

- The `return` after each `err != nil` in `browseMDNS` and
  `searchSSDP`.
- `discovery.store` increments `searches` for every result, empty or
  not.

## The reproduction

A test in `wiim/` runs `Discover` once with sockets that answer
nothing, and once with a `listenPacket` that fails to open the
socket. It wants the two results to differ, so a caller can tell a
failed search from an empty network. It failed with:

```
a search whose socket did not open returned [], and a search that nothing answered returned [], want results that differ
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When a network search for WiiM amps cannot open its socket, it
returns the same empty list as a search that found no amp. After
`discoveryMisses` such searches in a row, discovery deletes every
`Receiver` that it created.

- Does a failed search count as a search in which the amp was
  missing, or does it not count?
- How is the failure reported: a log line, an `Event`, or a
  condition?
