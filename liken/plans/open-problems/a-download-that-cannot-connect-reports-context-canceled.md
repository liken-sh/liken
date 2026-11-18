# A download that cannot connect reports `context canceled`

Open bug, recorded on 2026-11-18. The output was reproduced once by
running the command; the cause below is a reading of the code and is
not confirmed by a test.

`liken fetch`, and `liken verify` and `liken sbom` for a release,
download from a release channel through `Get` in
`liken/releases/download.go`, and so does `machine-operator`. When the connection fails, the error names a
cancelled context, and the real cause follows in parentheses. A run
against a port where nothing listens printed:

```
liken: fetching the channel document: context canceled (Get "http://127.0.0.1:1/channel.yaml": ... connection refused)
```

`Get` makes a context with `context.WithCancelCause`, so that the
stall timer can cancel a request with `errStalled`. When `client.Do`
returns an error, `Get` calls `stop` and then `causeOf(ctx, err)`.
`stop` calls `cancel(nil)`, which cancels the context with the cause
`context.Canceled`. `causeOf` then finds a cause that the error does
not wrap, and returns `fmt.Errorf("%w (%v)", cause, err)`. So every
error of `client.Do` becomes `context.Canceled`, with the transport's
error as text only. A caller that checks the error with `errors.Is`
or `errors.As` for the transport's error, such as a refused
connection or a `*url.Error`, does not find it.

The tests in `liken/releases/download_test.go` cover a stall, a
cancel by the caller, and a 404. No test covers a request that fails
before the response.

## What would confirm it

A test of `Get` with a dial that fails, in a `synctest` bubble with
`kubernetes/apiservertest`. The test checks that the error wraps the
dial's error and that `errors.Is(err, context.Canceled)` is false.

## What a fix has to decide

The order only: `causeOf` must read the cause before `stop` cancels
the context, and the same holds for any other path that calls `stop`
before it reports an error.
