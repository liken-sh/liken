# Job copies never give space back

When a Corrosion agent deletes rows, SQLite moves the freed pages to the
file's freelist, and the file keeps its size until something releases
them. Corrosion opens every database with `auto_vacuum = INCREMENTAL`
and runs a maintenance loop that releases free pages whenever the
freelist passes 10000 pages, 40 MB at the 4 KiB page size. That loop
starts 60 s after the agent starts and then runs every five minutes.

A standing catalog pod reaches that loop, so its copy shrinks on its own
within minutes of a large delete. A Job's agent lives for seconds to a
few minutes, so its copy almost never does. The orphan sweep in the
liken fork of Corrosion (release 2026.09.17-001) made the case visible:
every Job copy on the house cluster carried about a million orphaned
rows in `__corro_buffered_changes`, some 200 to 365 MB per copy, and
the sweep now deletes them at each agent start. The bytes those rows
held stay on each Job copy's freelist. On 2026-09-17 a hand pass ran
`VACUUM` on every idle copy on both clusters to take the space back
once, and the full `VACUUM` also repacked pages that no delete had
freed, which was a good part of the win on copies with no orphans.

What is owed is a release of free pages that a Job copy reaches without
a person. The plain option is a fork change beside the sweep: run the
freelist check once when the startup sweep finishes, so a copy whose
sweep freed pages gives them back in the same agent life. An operator
option is a first or last step of each Job that runs `PRAGMA
incremental_vacuum` when `freelist_count` passes a threshold, before or
after the agent holds the file. A full `VACUUM` needs an exclusive lock
and a file's worth of free space while it copies, so it stays a hand
tool. The threshold and the time a release costs at Job start should be
measured on liken-1 first.
