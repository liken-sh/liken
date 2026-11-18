# A crash in the link fallback leaves a .part file in the archive

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

A promotion `Job` copies a frame to `.<name>.part` beside its final
name in the archive, and then renames the copy with `renameNoReplace`
in `promote/noreplace.go`. When the archive's file system refuses
`renameat2` with `RENAME_NOREPLACE`, as many NFS and SMB shares do,
`renameNoReplace` calls `linkThenUnlink`: it links the `.part` file to
the final name and then removes the `.part` name. A crash between the
link and the removal leaves both names in the archive.

The next run finds a file at the final name, so `promote` in
`promote/promote.go` goes to `finishPlaced`. `finishPlaced` compares
the hashes, appends the catalog line if it is missing, and deletes the
frame from the outbox. It does not remove the `.part` file. Only
`copyFile` removes a `.part` file, and it runs only when the frame is
copied again, which does not happen after `finishPlaced`. So the
hidden `.part` name stays in the archive, as a second link to the
archived frame.

## Evidence

- `promote/noreplace.go`, `linkThenUnlink`: `unix.Link(from, to)`
  and then `os.Remove(from)`, where `from` is the `.part` path that
  `frameRun.copy` passes to `place`.
- `promote/promote.go`, `finishPlaced`: no call that removes the
  `.part` path.
- `plans/completed/03-promotion-to-the-archive.md`, the table of what
  a `Job` finds after a crash, says a `.part` file is deleted and the
  frame copied again. That row does not cover a `.part` file beside
  an archived frame with the same hash.
- No test in `promote/` stops a run between the link and the unlink.

## What would confirm it

A test in `promote/` that creates the archived frame and its `.part`
name as two links to one file, with the frame still in the outbox,
then runs `Run` and checks the archive directory for the `.part`
name.

## What a fix has to decide

- Whether `finishPlaced` removes the `.part` name of the frame it
  finishes, or whether a run sweeps every `.part` file of the target's
  archive directory.
- Whether the same gap exists on the same-claim path, where `rename`
  calls `place` on the outbox file itself and a crash leaves the frame
  under both names.
