# Working on people-operator

This repository defines the `Person` resource for a
[`liken`](https://liken.sh/) cluster: one cluster-scoped CRD and no
controller. Like the rest of the `liken` project, it is written to be
read: the manifests and the tests are the documentation.

`plans/00-design.md` is the design, and `plans/README.md` indexes the
plans that build it. Code exists only where a plan calls for it.

`make test` runs every check CI runs.
