# The broker is always in-cluster

Plan 03 stands up one MQTT broker, a `Deployment` and a `Service`
named `bus` in the operator's namespace, and points every pod at it.
For a home that runs no broker of its own, that is the whole answer.
For a home that already runs one, it is a second broker beside the
first, and that is waste.

Many homes already run a broker. Home Assistant's MQTT integration
needs one, and so does zigbee2mqtt, so a house with either has a
Mosquitto or an EMQX up before `liken` arrives. The clean shape for
that house is to point the operator at the broker it has, not to add
another. The two brokers would carry the same kinds of message, and
the media entities the operator publishes belong on the same broker
Home Assistant already reads.

The operator holds the broker's address and credentials as
configuration, so a bring-your-own mode is a matter of where those
values come from. The in-cluster `bus` supplies its own defaults; an
external broker supplies an address, a username, and a password,
which the operator passes to each pod the way it passes the rest of a
pod's environment. The undecided part is how an operator that has
an external broker configured also declines to create the in-cluster
`bus`, and whether that choice is one field or the presence of the
external configuration itself.

The topic and auth choices in plan 03 are made so this mode stays
reachable. The base topic is one configurable string, so it can move
under a broker that carries other tenants. The pod authenticates with
whatever the broker asks for, so an external broker's own auth is the
auth the pod uses. Nothing in plan 03 depends on the broker being the
one the operator created.
