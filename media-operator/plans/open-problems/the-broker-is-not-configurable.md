# The broker is not configurable

Plan 03 stands up one MQTT broker, a `Deployment` and a `Service`
named `bus` in the operator's namespace, and points every pod at it
under one topic base, `liken/media`. Both the address and the base are
fixed. A home that already runs a broker gets a second one beside it,
and two clusters that share a broker collide.

Many homes already run a broker. Home Assistant's MQTT integration
needs one, and so does zigbee2mqtt, so a house with either has a
Mosquitto or an EMQX up before `liken` arrives. The clean shape for
that house is to point the operator at the broker it has, not to add
another. The two brokers would carry the same kinds of message, and
the media entities the operator publishes belong on the same broker
Home Assistant already reads.

The collision follows from the shared broker. Two `liken` clusters
that publish under the same base both write a `Play` named `film` in
namespace `default` to `liken/media/plays/default/film/status`, and
each operator reads the other's reports as its own. One cluster with
its own in-cluster broker has one publisher per topic and never sees
it.

The operator holds the broker's address, its credentials, and the
topic base as configuration, so both fixes are a matter of where those
values come from. The in-cluster `bus` supplies its own defaults. An
external broker supplies an address, a username, and a password, which
the operator passes to each pod the way it passes the rest of a pod's
environment. A shared broker also supplies a base that carries the
cluster's name, `liken/<cluster>/media/...`, so each cluster owns a
subtree.

Three questions are undecided. The first is how an operator that has
an external broker configured also declines to create the in-cluster
`bus`, and whether that choice is one field or the presence of the
external configuration itself. The second is where the cluster's name
comes from: a `liken` cluster has a name in its `Cluster` resource and
the operator could read it, or the operator could take the base whole
as configuration and leave the naming to whoever sets it. The third is
the order, because the topic work is only worth doing for a broker the
operator did not create.

The reason to defer the base is that the wrong default is worse than
none. A cluster name baked into every topic today would move every
topic the day the name is added, and Home Assistant's discovery
configs would move with them. One configurable string that defaults to
`liken/media` lets the single-cluster home stay simple and lets the
shared-broker home set a base that carries its cluster's name.

Nothing in plan 03 depends on the broker being the one the operator
created. The in-cluster `bus` sets `allow_anonymous true` and no pod
carries a credential today, so this work also adds the first one. It
is one credential for the whole operator, and an external broker's own
auth is the auth every pod uses. A credential for each kind of client
is a separate question, in
[the bus authorizes nothing](the-bus-authorizes-nothing.md).
