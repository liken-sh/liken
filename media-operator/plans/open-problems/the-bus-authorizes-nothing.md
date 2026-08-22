# The bus authorizes nothing

Any client that reaches the broker can publish to any topic and
subscribe to any topic. Nothing checks that a remote sidecar publishes
only its own events, that a command on a `Play`'s command topic came
from a bound remote, or that a keymap on a keymap topic came from the
operator. The trust boundary is the whole cluster: a workload that can
open a TCP connection to the broker holds the full media control
plane.

For a single home cluster this is a deliberate simplification, not an
oversight. Every workload on the cluster is one the owner installed,
and the cost of per-topic access control is real: credentials to mint
and rotate, an ACL file the broker reads, and a role for each kind of
client. The design spends none of that until a cluster holds a
workload its owner does not trust.

One tension is worth writing down, because it is already in the
design. The playback pod decodes media from the network, which makes
it the least trusted process in the system, and the design gives it no
Kubernetes API credentials for that reason. That same pod is on the
bus. A compromised playback pod can publish commands to any other
`Play`, publish a false keymap, or forge a remote's events. The bus
gives back a slice of the reach the API restriction took away.

An answer looks like broker ACLs keyed to a credential per role:

* a remote sidecar publishes only its own events topic, and reads its
  keymap and focus topics.
* a command sidecar reads its own `Play`'s command topic and publishes
  only its own status.
* the operator publishes the keymap and focus marks, and reads status.

The shape is known. The work waits until a cluster runs something the
owner does not already trust, or until a second tenant shares one
broker, whichever comes first. It shares that trigger with
[the broker is not configurable](the-broker-is-not-configurable.md).
