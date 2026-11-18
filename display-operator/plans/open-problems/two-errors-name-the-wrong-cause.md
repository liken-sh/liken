# Two errors name the wrong cause

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

The component's `AGENTS.md` asks that an error include its source's
own text, so a person reads the cause from the log or the response.
Two error paths give a cause other than the one that happened.

`loadCaptureLeaf` in `serving.go` reads `tls.crt` and `tls.key`. When
the sidecar already serves the API's leaf and a read fails, it returns
"reloading the API's leaf, which stays in use: %v" with `certErr`
only. When the certificate reads and the key does not, the message
ends in `<nil>`, and the key's error is lost.

`dialFault` in `forward.go` turns the error of a request to a capture
sidecar into the caller's problem document. Every `net.OpError` whose
`Op` is `dial` gets "refused the connection", which includes a dial
that timed out. Every error that no case matches gets a 502 that says
the sidecar "responded with something that is not HTTP", which
includes a connection reset and a request that the caller cancelled.
The log line beside each fault has the real error, so only the
caller's message is wrong.

## Evidence

- `serving.go`, `loadCaptureLeaf`: `fmt.Errorf("reloading the API's
  leaf, which stays in use: %v", certErr)` runs when
  `certErr != nil || keyErr != nil`.
- `forward.go`, `dialFault`: the `operation.Op == "dial"` case returns
  `refused`, and the fall-through builds the "not HTTP" fault.

## What would confirm it

A test of `loadCaptureLeaf` with a `tls.crt` and no `tls.key` after a
good load. A test of `dialFault` with a dial timeout and with
`syscall.ECONNRESET`.

## What a fix has to decide

`loadCaptureLeaf` can join both read errors. For `dialFault`, the fix
decides which causes a caller can tell apart: a timeout, a reset, and
a reply that is not HTTP each need their own detail, and the status
of each, 503 or 502, follows from whether a retry can succeed.
