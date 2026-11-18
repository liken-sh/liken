# `wpaEvent.level` is read only by tests

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`parseWPAEvent` parses the priority of each message that
`wpa_supplicant` sends and stores it in `wpaEvent.level`, but no code
outside the tests reads the field.

## The evidence

`wpaEvent.level` is declared at `init/wpactrl.go:42` and set in
`parseWPAEvent` (`init/wpactrl.go:68` to `81`). A search for `.level`
in the files of `init/` that are not tests finds no read. The reads are
in `init/wpactrl_test.go`, lines 24 and 81.

## What would confirm it

Remove the field and build the package without its tests.

## What a fix has to decide

Whether a caller needs the priority, for example to drop debug
messages from the console. If none does, the field and its tests go,
and `parseWPAEvent` only strips the priority.
