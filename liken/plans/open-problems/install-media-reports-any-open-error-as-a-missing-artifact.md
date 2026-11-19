# Install media reports any open error as a missing artifact

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`Media` in `image/media.go` opens the microcode early cpio and
`boot.cpio` from the release directory before it writes the install
image. When `os.Open` returns an error, `Media` returns "release %s
has no %s, so it cannot make install media. Use a newer release", and
it drops the error. So a file that exists but that the person cannot
read, or a read error of the disk, tells the person to download a
newer release.

## The evidence

The loop over `microcodeArtifact` and `"boot.cpio"` in `Media` does not
check the kind of the error, and the message does not wrap `err`. The
rule "Errors include their source's text" in `liken/AGENTS.md` says
that a wrapped error includes the source's own error string.
`verifiedRelease`, in the same file, wraps the error of the same kind
of open with `%w`.

## What would confirm it

A test that makes `boot.cpio` in a release directory unreadable, for
example a directory in place of the file, and reads the error that
`Media` returns.

## What a fix has to decide

Whether `Media` gives the "use a newer release" advice only for
`fs.ErrNotExist` and wraps every other error with `%w`, or whether it
always wraps the error and adds the advice.
