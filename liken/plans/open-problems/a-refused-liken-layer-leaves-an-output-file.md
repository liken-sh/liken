# A refused `liken layer` leaves an output file

Open bug, recorded on 2026-11-18. The output was observed once by
running the command, and the cause below is a reading of the code.

`liken layer <manifests-dir> <identity-dir> <output.cpio>` packs a
deployment's layer. `Layer` in `liken/image/layer.go` reads and checks
the manifests and the passphrases before it creates the output, and
the comments there say that this order leaves no truncated cpio behind
for a later step to pack into an image.

The identity files are read after the output exists. `Layer` calls
`os.Create(out)` and then, for each path in `identity.Bundle`, reads
the file from the identity directory with `os.ReadFile` and writes it
into the archive. A missing file returns the read's error, and the
output stays on disk. When `liken layer` ran on a directory that was
not an identity directory, it printed
`open <dir>/token: no such file or directory` and left a 0-byte
`out.cpio`. A write that fails in the middle of the archive also
leaves a partial file.

## What would confirm it

A unit test of `Layer` with a valid manifests directory and an empty
identity directory, which checks that the output path does not exist
after the error.

## What a fix has to decide

Either `Layer` reads every identity file before it creates the
output, as it does for the manifests and the passphrases, or it
writes to a temporary file in the output's directory and renames it
over `out` only after `close` succeeds. The rename also covers a write
that fails in the middle.
