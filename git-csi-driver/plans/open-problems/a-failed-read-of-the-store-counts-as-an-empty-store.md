# A failed read of the store counts as an empty store

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it.

The sweep in `sweep.go` decides what to delete from what it reads in
`<store>/volumes/`, and it treats a failed read as a read that found
nothing:

- `usesRepository` returns false when `os.ReadDir` of the volumes
  directory fails. `sweepRepository` then runs `os.RemoveAll` on the
  bare repository. A work tree on disk that the driver holds no volume
  for reads its objects from that repository through its alternates
  file, so the tree loses its history.
- `workTree.alternate` returns an empty string when the alternates
  file cannot be read, so `usesRepository` does not count that tree
  either.
- `followedRefs` ignores the error of the same `os.ReadDir`, so
  `sweepRefs` deletes the refs that only volumes on disk follow.

A missing directory means no volumes, as the comment in
`followedRefs` says. Other errors, such as too many open files, mean
the sweep does not know.

`resume` in `records.go` returns without a log line when its
`os.ReadDir` fails, so the node plugin starts without its mounted
volumes and nothing says why. `sweepRepositories` also returns
silently, which is safe because it deletes nothing.

## What would confirm it

A test in `sweep_test.go` with a work tree on disk, no volume in
memory, and a volumes directory that the sweep cannot read, for
example with mode `0000` when the test does not run as root. After
the sweep, the bare repository is gone.

## What a fix has to decide

Which errors count as "no volumes". `os.ErrNotExist` does. For any
other error, the sweep could skip the repository and log a warning,
and `resume` could log one. `alternate` would need to return its
error for the sweep to tell the cases apart.
