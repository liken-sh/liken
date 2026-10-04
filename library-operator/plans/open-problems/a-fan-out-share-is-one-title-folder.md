# A fan-out share is one title folder

A worker `Job` with `parallelism` above 1 is an Indexed `Job`, and
each pod takes the title folders that hash to its index
([plan 76](../completed/76-fan-out-workers.md)). Three questions about
that split are open:

* **Uneven shares.** The hash balances the count of folders, not the
  hours. A long series lands whole in one pod, so that pod can run for
  hours after the others end. A series library may want a split by
  season folder, with the ledger written through the update door. No
  one has measured it.
* **A pod name that is too long.** The pod of an Indexed `Job` takes
  the hostname `<job>-<index>`. A `Library` with a name near the limit
  of a `Job` name can push the hostname past 63 characters. The worker
  `Job` uses the operator's one rule for `Job` names, and no test has
  tried a long name with `parallelism` above 1.
* **A list that changes while the pods start.** Each pod resolves the
  title folder of each video when it starts. A folder created or
  removed between two pods' starts can put one video in two shares or
  in none. A video in no share stays in the gap for the next list. A
  video in two shares is decoded twice, and the update door keeps the
  ledger whole.
