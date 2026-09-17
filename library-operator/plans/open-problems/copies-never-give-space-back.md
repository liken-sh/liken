# Copies never give space back

A Corrosion copy only grows on disk. When the agent deletes rows, SQLite
moves the freed pages to the file's freelist and reuses them later, but
the file keeps its size until something runs `VACUUM`. Corrosion never
does, and nothing in this operator does.

The orphan sweep in the liken fork of Corrosion (release 2026.09.17-001)
made the case visible. Before it, every Job copy on the house cluster
carried about a million orphaned rows in `__corro_buffered_changes`,
some 200 to 365 MB per copy. The sweep now deletes them at each agent
start, so the copies stop growing, but the bytes those rows held stay in
the files. On 2026-09-17 a hand pass ran `VACUUM` on every idle copy on
both clusters to take the space back once. A copy that shrinks and grows
in the normal course of scans, deletes, and library removals will
accumulate the same slack again, only slower.

What is owed is an occasional `VACUUM` that the operator schedules, not a
person. The open questions are who runs it and when. A `VACUUM` needs an
exclusive lock and a file's worth of free space while it copies, so it
cannot run under a live agent, and a Job copy is only idle between Jobs.
A Job could run it as a first or last step when the freelist passes a
threshold (`PRAGMA freelist_count` against `page_count`). The standing
catalog pods never go idle, so their copies need `PRAGMA
incremental_vacuum` on `auto_vacuum = INCREMENTAL`, set when the copy is
created, or a pause the operator arranges. Either way the threshold, the
cadence, and the disk headroom a `VACUUM` needs should be measured on
liken-1 first.
