# The bus has no per-topic access control

Any client that connects to the broker can publish to any topic and
subscribe to any topic. Nothing checks that a remote sidecar publishes
only its own events, that a command on a `Play`'s command topic came
from a bound remote, or that a keymap on a keymap topic came from the
operator. The trust boundary is the whole cluster: a workload that can
open a TCP connection to the broker has access to the full media
control plane.

For a single home cluster this is a deliberate simplification. Every
workload on the cluster is one the owner installed, and per-topic
access control has a real cost: credentials to mint and rotate, an ACL
file the broker reads, and a role for each kind of client. The design
adds none of that until a cluster runs a workload its owner does not
trust.

The design already has one conflict here. The playback pod decodes
media from the network, which makes it the least trusted process in the
system, and for that reason the design gives it no Kubernetes API
credentials. That same pod is on the bus. A compromised playback pod
can publish commands to any other `Play`, publish a false keymap, or
forge a remote's events. So the bus gives the playback pod back part of
the control that the API restriction removed.

A fix would use broker ACLs, with one credential for each role:

* a remote sidecar publishes only its own events topic, and reads its
  keymap and focus topics.
* a command sidecar reads its own `Play`'s command topic and publishes
  only its own status.
* the operator publishes the keymaps and the focus marks, and reads
  status.

The design is known. The work waits until a cluster runs something the
owner does not already trust, or until a second tenant shares one
broker, whichever comes first. The same condition starts the work in
[the broker is not configurable](the-broker-is-not-configurable.md).
