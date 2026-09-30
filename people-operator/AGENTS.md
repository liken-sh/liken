# Working on people-operator

This directory defines the `Person` resource for a
[`liken`](https://liken.sh/) cluster, one cluster-scoped CRD, and the
operator that writes each `Person`'s picture into its status. The
manifests, the source, and the tests are the documentation.

The operator watches every `Person` and every baker pod through
client-go's reflector, in the shared `kubernetes` module. Load the
`operators` skill under `.agents/skills` at the top of the repository
before you change a watch, the pass, or a timer.

`plans/00-design.md` is the design, and the `plans/` directory holds the
plans that build it. Code exists only where a plan calls for it.

`make test` runs every check CI runs.
