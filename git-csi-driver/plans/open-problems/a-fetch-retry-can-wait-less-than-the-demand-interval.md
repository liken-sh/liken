# A fetch retry can wait less than `--demand-min-interval`

Open bug, recorded on 2026-11-18. A review of the comments against the
code found it, and a unit test reproduced it on 2026-11-19.

`nextRetry` in `follow.go` computes the wait before the fetch loop
retries a fetch that failed for a demand. Its comment says that a
retry does not go through the delay timer that limits demands, "so
the wait itself has to stay at or above `--demand-min-interval`". The
code does not keep that rule when `--demand-min-interval` is longer
than `maxDemandRetry`, which is 5 minutes:

```go
f.backoff = min(max(2*f.backoff, f.node.demandMin), maxDemandRetry)
return jittered(f.backoff, f.node.demandMin)
```

With `--demand-min-interval=1h`, the backoff is capped at 5 minutes.
`jittered(5m, 1h)` computes `lowest = max(1h, 2m30s) = 1h`, which is
not below the backoff, so it returns 5 minutes. A remote that keeps
failing is then fetched every 5 minutes, which is twelve times as
often as the interval allows.

`cli.go` accepts any value for `--demand-min-interval`, and
`republish_rotation_test.go` sets `demandMin` to an hour.

## The reproduction

A test set `demandMin` of a fetch loop to an hour and called
`nextRetry` once. It wanted a wait of at least an hour, and failed
with:

```
the first retry waits 5m0s with --demand-min-interval=1h0m0s, want at least 1h0m0s because a retry does not pass the timer that limits demands
```

The test is not in the tree, because a failing test cannot land on
`main`.

## What a fix has to decide

When `--demand-min-interval` is longer than 5 minutes, should a
remote that keeps failing be tried again every 5 minutes, or never
more often than the interval?

Which rule wins: the cap, so a remote that comes back is fetched
within 5 minutes, or the interval, so a retry never fetches sooner
than a demand could. The fix can raise the cap to the interval, or
refuse an interval longer than `maxDemandRetry` at the start, or
change the comment to state that the cap wins. The install guide
describes `--demand-min-interval` and changes with the rule.
