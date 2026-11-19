# Drill 05 can read the stats of another drill's volume

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.
A review of the comments against the code found it. On 2026-11-19 the
claim was checked against the code: `lab/drills/05.sh:44` matches
`pvcRef.name` alone. No unit test can show it, because the drill is a
shell script that reads a kubelet's stats summary. A `make drill` run
on the lab, as the next section describes, would show it.

`used_bytes` in `per-node-csi-driver/lab/drills/05.sh` reads the
kubelet's stats summary for the node and prints the `usedBytes` of the
first volume whose `pvcRef.name` is `store`. It does not compare
`pvcRef.namespace` with `drill-05`. Drills 01, 02, 03, and 04 also
name their claim `store`, in the namespaces `drill-01` to `drill-04`.

`make drill` in `lab/Makefile` runs the drills in order, and each
drill's `cleanup` deletes its namespace with `--wait=false`. So the
pods of an earlier drill can still be on the node while drill 05 runs.
Drill 01 writes a file into its copy, so its volume can report more
than 0 bytes. If the summary still lists that volume, `wait_for_stats`
passes on drill 01's bytes, and drill 05 passes without proof that
`NodeGetVolumeStats` reports its own volume.

## What would confirm it

Run `make drill` on the lab, and save the stats summary at the moment
drill 05 first reads it. The summary lists a `store` volume of a
namespace other than `drill-05`.

## What a fix has to decide

Nothing beyond the match: compare both `pvcRef.name` and
`pvcRef.namespace`, using `$NAMESPACE`.
