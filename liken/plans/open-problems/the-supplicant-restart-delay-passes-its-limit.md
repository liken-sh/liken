# The supplicant restart delay passes its limit

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When a `wpa_supplicant` dies again and again, init waits 32 seconds
before one restart, although `maxBackoff` is 30 seconds. The first
delay is also 2 seconds, not the 1 second that `backoff` names. The
same doubling sets the delay between attach attempts in `watch`.

## The evidence

`supplicantProcess.next` (`init/wireless.go:334`) returns
`p.maxBackoff` only when the delay it receives is already at or over
the limit. Below the limit, it returns the delay doubled, with no cap.
With `backoff` at 1 second and `maxBackoff` at 30 seconds
(`init/wireless.go:272`), the delays are 2, 4, 8, 16, 32, and then 30
seconds. Both `restart` (`init/wireless.go:420`) and `watch`
(`init/wireless.go:407`) call `next` before the first wait, so no wait
is ever 1 second. The comments at `init/wireless.go:231` and
`init/wireless.go:333` say "up to maxBackoff" and that `backoff` is the
first delay.

`superviseK3s` (`init/supervisor.go:300`) has the same shape, and its
comment says "up to 32 seconds", so its 32 is stated.

## What would confirm it

A unit test that calls `next` from 1 second until it repeats, and
compares each value with `maxBackoff`.

## What a fix has to decide

Whether `next` caps the doubled value at `maxBackoff`, and whether the
first wait is `backoff` or twice `backoff`. The test fixtures in
`init/supplicant_test.go:124` and `init/wirelessstop_test.go:130` set
their own limits and can depend on the current sequence.
