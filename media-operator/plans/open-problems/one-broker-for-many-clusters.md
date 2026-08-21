# One broker for many clusters

Plan 03 roots every topic under one base, `liken/media` by default.
Two `liken` clusters that publish to one broker under the same base
collide: both write a `Play` named `film` in namespace `default` to
`liken/media/plays/default/film/status`, and each operator reads the
other's reports as its own.

For the common case the collision never happens. A home runs one
cluster, and the broker is in that cluster, so there is one publisher
per topic. The collision needs two clusters and a shared broker, which
is the bring-your-own case in
`the-broker-is-always-in-cluster.md` taken one step further: a broker
that several clusters share.

The base topic is one string, so the fix is to put the cluster's name
in it: `liken/<cluster>/media/...` instead of `liken/media/...`. Each
cluster then owns a subtree, and one broker carries them all without
overlap. What is undecided is where the cluster's name comes from. A
`liken` cluster has a name in its `Cluster` resource, and the operator
could read it, or the operator could take the base topic whole as
configuration and leave the naming to whoever sets it.

The reason to defer the choice is that the wrong default is worse than
none. A cluster name baked into every topic today would move every
topic the day the name is added, and Home Assistant's discovery
configs would have to move with them. One configurable string that
defaults to `liken/media` lets the single-cluster home stay simple and
lets the shared-broker home set a base that carries its cluster's
name.
