# A panic in a radio join leaves the radio pending

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

When a radio's bring-up panics during the background pass, the machine
plane runs the component again, and the second run skips that radio.
No result for it ever arrives, so `status.network` shows the radio in
its pending state until the next boot, and the radio watch never stops
waiting for it.

## The evidence

`joinRadios` (`init/radios.go:162`) sets the radio's `claimed` flag
before it calls `joinOne`, and sends the result on `pass.settled` only
after `joinOne` returns. A panic in `joinOne` skips the send.
`runComponent` (`init/components.go:133`) turns the panic into an
error, and the plane runs the component again. The second run finds
the flag set and goes on to the next radio. The comment on
`joinRadios` says the flags exist so a second run starts no second
supplicant and sends no second result.

`watchRadios` counts `pass.pending` down by one for each result
(`init/radiowatch.go:126` to `132`), and keeps its loop open while
`pending` is above zero. It started the radio's entry as
`pendingRadio` (`init/radios.go:135`).

A second run also starts with `stuck` empty, so after a panic it can
raise a radio that the first run would have marked as not attempted.

## What would confirm it

A unit test whose `radio` function panics for one interface, run
through the machine plane, that reads the published connections.

## What a fix has to decide

What the radio's status is after a panic. One way is a deferred send
of a `failedRadio` connection when `joinOne` does not return. Then the
radio is not raised again, and its status names the panic.
