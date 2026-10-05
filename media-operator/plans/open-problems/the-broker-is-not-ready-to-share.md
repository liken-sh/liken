# The broker is not ready to share

Open problem. Plan 03 creates one MQTT broker, a `Deployment` and a
`Service` named `bus` in the operator's namespace. The operator reads
the broker's address from `MEDIA_BUS_ADDRESS` and the topic base from
`MEDIA_TOPIC_BASE`, which defaults to `liken/media`, and passes both to
every pod it creates (`media-operator/wire.go`, `operate.go`).
library-operator's screens read the same `MEDIA_BUS_ADDRESS`, and its
progress role reads the base from `LIBRARY_MEDIA_TOPIC_BASE`. So a
cluster can already point every client at another broker and another
base. What it cannot do yet is use a broker that other systems share,
because no client presents a credential and the broker checks nothing.

## Who connects

Root
[plan 77](../../../plans/completed/77-the-media-bus-stops-at-media-operator.md)
made the bus part of the media domain. `media-operator`, the pods it
starts (the remote readers, the playback sidecars, and the idle
screen clients), and library-operator's screens connect to it. No
device operator does: equipment-operator lost its MQTT client, and the
volume, power, input, and screen asks reach the `Receiver` and the
`Television` as status fields that `media-operator` writes. So the
set of clients that a credential or an ACL must cover is smaller than
it was, and every one of them is a media component.

## Why a home wants a shared broker

Many homes already run a broker. Home Assistant's MQTT integration
needs one, and so does zigbee2mqtt, so a house with either already runs
Mosquitto or EMQX before `liken` is installed. For that house, the
operator should use the existing broker and not add another. The media
entities the operator could publish belong on the broker Home
Assistant already reads, which is the prerequisite of
[the player is not a Home Assistant
entity](the-player-is-not-a-home-assistant-entity.md).

## The work, in order

1. **Credentials.** The in-cluster `bus` sets `allow_anonymous true`
   (`media-operator/deploy/bus.yaml`), and the MQTT clients of
   `media-operator` and library-operator leave the username and
   password unset (`mqtt.go` in each). An external broker needs one
   credential for the whole operator, which the operator passes to each
   pod the same way it passes the address. It is the first credential
   on the bus.
2. **An external broker in place of the in-cluster `bus`.** An
   operator that has an external broker configured should not run the
   `bus` `Deployment` beside it. Undecided: whether that choice is one
   field, or the presence of the external configuration itself.
3. **A topic base with the cluster's name.** Two `liken` clusters that
   publish under the same base both write a `Play` named `film` in
   namespace `default` to `liken/media/plays/default/film/status`, and
   each operator reads the other's reports as its own. A shared broker
   needs a base such as `liken/<cluster>/media`, so each cluster owns a
   subtree. `MEDIA_TOPIC_BASE` already accepts that base. Undecided:
   whether the operator reads the name from the `Cluster` resource or
   leaves the whole base to whoever sets it. The default stays
   `liken/media`, because a cluster name baked into every topic would
   move every topic, and every Home Assistant discovery config, on the
   day the name is added.
4. **Per-topic access control.** Any client that connects can publish
   to any topic and subscribe to any topic. Nothing checks that a
   remote reader publishes only its own events, that a command on a
   `Play`'s commands topic came from a bound remote, or that the key
   table on a `Remote`'s `keys` topic came from the operator. The
   playback pod decodes media from the network, which makes it the
   least trusted process in the system, and for that reason it holds
   no Kubernetes API credential. It is still on the bus, so a
   compromised playback pod can command any other `Play`, publish a
   false key table, or forge a remote's events. A fix uses broker ACLs
   with one credential for each role:
   * a remote reader publishes only its own events topic, and reads
     only its own `keys` topic;
   * a command sidecar reads its own `Play`'s commands topic and
     publishes only its own status;
   * the operator publishes the key tables and the focus marks, and
     reads status.

For a single home cluster on its own in-cluster broker, none of this
is needed: every workload is one the owner installed, and one cluster
has one publisher per topic. The work starts when a cluster shares a
broker with another system or another cluster, or runs a workload its
owner does not trust, whichever comes first.
