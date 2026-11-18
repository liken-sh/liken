# Three error messages report the wrong cause or value

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Three messages are built from a value other than the one that caused
the error, so a person who reads them looks for the wrong fault:

- `promote/command.go`, `Main`: when `--cameras` decodes and only
  `--target` is missing, the condition `err != nil || o.Target == ""`
  is true with a nil `err`. The log line then reads
  `promote: --target is required, and --cameras must be JSON: <nil>`,
  and does not say which flag is wrong.
- `sky/sexagesimal.go`, `ParseDec`: the function strips a leading `-`
  or `+` from `text` before it reads the value, and both error
  messages print `text` after that. So `-91:00:00` is reported as
  `the declination "91:00:00" is beyond 90 degrees`.
- `guiding.go`, `phd2Guiding.stop`: when `WaitFor` fails, the error
  says that PHD2 `did not stop within` `stopTimeout` and discards the
  error from `WaitFor`. When `ctx` ends first, for example because
  the reservation ends, the message gives the timeout as the cause.

## Evidence

Each case is in the line named above. No test checks the text of
these three messages for the case described.

## What would confirm it

- Run `promote --cameras '{}'` with no `--target` and read the log
  line.
- Call `ParseDec("-91:00:00")` and read the error.
- Cancel `ctx` while a test's PHD2 fake does not stop, and read the
  error that `stop` returns.

## What a fix has to decide

- Whether `Main` writes one message for each flag.
- Whether `ParseDec` prints the text as the caller gave it.
- Whether `stop` wraps the error from `WaitFor`, or tells a deadline
  apart from an ended `ctx` and a closed connection.
