# A fetch retry can wait less than `--demand-min-interval`

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

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

## What would confirm it

A case in `demand_retry_test.go` that sets `demandMin` to an hour and
calls `nextRetry`. It returns 5 minutes.

## What a fix has to decide

Which rule wins: the cap, so a remote that comes back is fetched
within 5 minutes, or the interval, so a retry never fetches sooner
than a demand could. The fix can raise the cap to the interval, or
refuse an interval longer than `maxDemandRetry` at the start, or
change the comment to state that the cap wins. The install guide
describes `--demand-min-interval` and changes with the rule.
