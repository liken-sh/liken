# Litestream as the catalog transport

The design before Corrosion replicated each library's catalog as a
SQLite file that one scanner wrote and Litestream shipped as LTX
files to a second shared volume. Every browser opened the catalog
through Litestream's VFS extension, which reads pages on demand from
the replica, caches them, and polls for new segments. It was built and
measured on 2026-08-29 against Litestream commit 3b468c9, and it
worked. It lost to Corrosion on the points below. Litestream stays the
fallback that needs no agent at all.

## What worked

- A Rust client with `rusqlite` loaded the extension, opened a
  5000-row catalog from a `file://` replica in 0.12 s, and answered a
  count in 0.75 ms and a filter-and-sort in 0.23 ms at the median.
- A row written by the Go writer reached the reader 1.4 s later at the
  median: up to a second for the writer's sync and up to a second for
  the reader's poll.
- Several catalogs in one process worked through a fork of the
  extension's 130-line entry point that registers one VFS name per
  replica URL. The stock extension serves one per process. Its C shim
  also declares the auto-extension hook with the wrong return type, so
  any connection whose main database is not on the VFS fails to open.
  Both fixes were three lines.
- Linking only the `file` replica backend cut the extension from 85 MB
  to 36 MB and its load cost from 80 MB to 64 MB of resident memory.

## Why it lost

- Reads are polled. The VFS lists the replica directory on a timer, so
  freshness costs a listing per catalog per screen per second. A
  fetch triggered from the bus needed a method the library does not
  export.
- Readers broke under compaction. With level-1 compaction every 10 s,
  readers opened early wedged in three of four runs with `poll L1:
  non-contiguous ltx file`. The VFS requires the next level-1 file to
  start one past its cursor, and compaction can rewrite the startup
  file so that never holds again. Readers opened later died in three
  of four runs with `database disk image is malformed`. At the default
  30 s interval two runs were clean, and three more at 10 s on a
  quieter machine were clean, so it is a race. A client can detect
  both, one by a lag that grows without bound and one by the
  connection's error, and reopen.
- The extension is a Go runtime inside the client: 64 MB of resident
  memory before a page is read, beside the 87 MB the Iced idle screen
  uses.
- It needs a second shared volume, mounted by every screen.

Corrosion delivered a change in 17 ms by push, needs no shared volume
and no extension, and its sidecar uses 74 MB at rest with a catalog
twenty times larger.
