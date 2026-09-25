# Job copies never give space back

When a Corrosion agent deletes rows, SQLite moves the freed pages to the
file's freelist, and the file keeps its size until something releases
them. Corrosion opens every database with `auto_vacuum = INCREMENTAL`
and runs a maintenance loop that releases free pages whenever the
freelist passes 10000 pages, 40 MB at the 4 KiB page size. That loop
starts 60 s after the agent starts and then runs every five minutes.

A catalog pod runs long enough for that loop to start, so its copy
shrinks without help within minutes of a large delete. A Job's agent
runs for seconds to a few minutes, so its copy almost never shrinks.
The orphan sweep in the
liken fork of Corrosion (release 2026.09.17-001) showed the size of the problem.
Every Job copy on the house cluster had about a million orphaned rows
in `__corro_buffered_changes`, about 200 to 365 MB per copy. The sweep
now deletes them at each agent start. The pages those rows used stay on
each Job copy's freelist. On 2026-09-17, a manual pass ran
`VACUUM` on every idle copy on both clusters to release the space once.
The full `VACUUM` also repacked pages that no delete had freed. On
copies with no orphans, that repacking was a large part of the space
recovered.

A Job copy needs a release of free pages that runs without a person.
The simple option is a fork change beside the sweep: run the
freelist check once when the startup sweep finishes, so a copy whose
sweep freed pages gives them back in the same agent life. An operator
option is a first or last step of each Job that runs `PRAGMA
incremental_vacuum` when `freelist_count` passes a threshold, before
the agent opens the file or after it closes it. A full `VACUUM` needs
an exclusive lock and free space equal to the file's size while it
copies, so it stays a manual tool. The threshold and the time a release costs at Job start should be
measured on liken-1 first.
