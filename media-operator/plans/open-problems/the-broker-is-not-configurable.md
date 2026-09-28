# The broker is not configurable

Plan 03 creates one MQTT broker, a `Deployment` and a `Service`
named `bus` in the operator's namespace, and points every pod at it
under one topic base, `liken/media`. Both the address and the base are
fixed. A home that already runs a broker gets a second one beside it,
and two clusters that share a broker collide.

Many homes already run a broker. Home Assistant's MQTT integration
needs one, and so does zigbee2mqtt, so a house with either already runs
Mosquitto or EMQX before `liken` is installed. For that house, the
operator should use the existing broker and not add another. The two
brokers would carry the same kinds of message, and the media entities
the operator publishes belong on the broker Home Assistant already
reads.

The collision comes from the shared broker. Two `liken` clusters
that publish under the same base both write a `Play` named `film` in
namespace `default` to `liken/media/plays/default/film/status`, and
each operator reads the other's reports as its own. One cluster with
its own in-cluster broker has one publisher per topic, so the
collision does not occur there.

The operator stores the broker's address, its credentials, and the
topic base as configuration, so both fixes change only where those
values come from. The in-cluster `bus` supplies its own defaults. An
external broker supplies an address, a username, and a password, which
the operator passes to each pod the same way it passes the rest of a
pod's environment. A shared broker also supplies a base that includes
the cluster's name, `liken/<cluster>/media/...`, so each cluster owns a
subtree.

Three questions are undecided. The first is how an operator that has
an external broker configured also skips the in-cluster `bus`, and
whether that choice is one field or the presence of the external
configuration itself. The second is where the cluster's name comes
from: a `liken` cluster has a name in its `Cluster` resource and the
operator could read it, or the operator could take the whole base as
configuration and leave the naming to whoever sets it. The third is
the order, because the topic work is only useful for a broker the
operator did not create.

The base is deferred because a wrong default is worse than no default.
A cluster name baked into every topic today would move every topic on
the day the name is added, and Home Assistant's discovery configs
would move with them. One configurable string that defaults to
`liken/media` keeps the single-cluster home simple, and lets the
shared-broker home set a base that includes its cluster's name.

Nothing in plan 03 depends on the broker being the one the operator
created. The in-cluster `bus` sets `allow_anonymous true` and no pod
has a credential today, so this work also adds the first one. It
is one credential for the whole operator, and every pod uses the
external broker's own authentication. A credential for each kind of
client is a separate question, in
[the bus has no per-topic access control](the-bus-authorizes-nothing.md).
