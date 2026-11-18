# A WiiM search that cannot open its socket finds nothing

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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

## What would confirm it

A test that replaces `discover` with a function that fails to open
its sockets, runs `discoveryMisses` searches, and checks whether
discovery deletes a discovered `Receiver`.

## What a fix has to decide

- Whether `Discover` returns an error, and whether a failed search
  counts as a miss.
- Where the failure is reported: a log line, an `Event`, or a
  condition.
