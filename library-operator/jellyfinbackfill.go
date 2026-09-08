package main

// jellyfinbackfill.go names the one-time backfill of plan 50: the subcommand
// the Job runs, and the worker label its Job carries. The Job's own program
// and the operator's stand of it live beside this file.

// The argument that selects the backfill, the way jellyfinMode selects the
// role, and the worker name its Job is labeled with.
const (
	jellyfinBackfillMode   = "jellyfin-backfill"
	workerJellyfinBackfill = "jellyfin-backfill"
)
