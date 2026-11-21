# 99. A leader serves etcd's metrics on the node network

Milestone 99. Proposed and built on 2026-11-21. The drills below have
not run, because no machine has booted a release with this change.

On a cluster with more than one leader, each leader runs an etcd
member inside the k3s server process. When a leader's etcd stalls for
several seconds, the API server on that leader fails its readiness
check, and requests through it fail. To find the cause, a person needs
etcd's own series for each member, such as
`etcd_disk_wal_fsync_duration_seconds`,
`etcd_disk_backend_commit_duration_seconds`,
`etcd_network_peer_round_trip_time_seconds`,
`etcd_server_leader_changes_seen_total`, and
`etcd_server_slow_apply_total`. They show whether the member waited
for its disk, for a peer, or for its own apply queue.

A `liken` machine before this milestone serves those series only on
the loopback address. A Prometheus in a pod cannot scrape them, and a
`liken` machine has no shell to read them from.

## What k3s does

These facts were read in the source of k3s v1.37.2+k3s1, the version
that `k3s/pin.toml` names, and of the etcd that it builds in,
`k3s-io/etcd` v3.7.1-k3s3.

- `listenMetricsURLs` in `pkg/etcd/etcd.go` binds etcd's metrics
  listener to `http://127.0.0.1:2381`. When the server option
  `etcd-expose-metrics` is set, it adds `http://<address>:2381`, where
  the address is the one that the member advertises to its peers, the
  node IP.
- No other code reads the option. A server that keeps its datastore
  in sqlite starts no etcd, so the option does nothing there.
- `serveMetrics` in etcd's `server/embed/etcd.go` serves `/metrics`
  and the health paths `/health`, `/livez`, and `/readyz` on that
  listener, and nothing else.
- k3s labels the `Node` of each etcd member
  `node-role.kubernetes.io/etcd=true`, and removes the label from a
  node that stops being a member.

The request that led to this milestone also reported a check on a
running cluster with five leaders: the supervisor's `/metrics` on port
6443 had the API server's `etcd_request_*` series and no `etcd_disk_*`
series. That check was not repeated for this milestone.

## The design

The `Cluster` gets `spec.runtime.etcd.exposeMetrics`, a boolean that
is `true` when it is unset. While it is `true`, `init` renders
`etcd-expose-metrics: true` into `boot.yaml` on every leader, and each
leader serves etcd's metrics at `http://<node IP>:2381/metrics`. When
the cluster sets it to `false`, `init` renders no key, and etcd serves
its metrics on the loopback address only, as before this milestone.

`status.runtime.etcd.exposeMetrics` of a `Machine` is `true` when
`init` wrote the key on that machine, like the other values of
`status.runtime`.

- **A running fleet** gets the key with the release that has it, on
  the reboot that the upgrade already needs. No step by a person is
  needed.
- **An edit of the field** is a change to `spec.runtime`, so it
  converges like every other field there: k3s restarts in place on
  each machine, one leader at a time, and no machine reboots.
- **A cluster with one leader** uses sqlite, so nothing listens on
  port 2381. `init` still renders the key, because it cannot tell from
  the `Cluster` whether a leader runs etcd: a cluster that went from
  several leaders to one keeps its etcd datastore.
- **A cluster that grows from one leader to several** converts the
  founder's datastore to etcd on the boot that adds the second leader.
  The key is already set, so the founder serves the metrics from that
  boot, and each new leader serves them from its first boot.
- **A follower** never renders the key, because a k3s agent refuses a
  server key. A leader that is demoted boots as a follower, starts no
  etcd, and serves nothing on the port. k3s removes the `etcd` role
  label from its `Node`, so a scrape job that selects on the label
  drops the target.

## Choices

**On unless the cluster turns it off.** Every other field of
`spec.runtime` keeps the program's own default when it is unset, and
the default of k3s is off. This field is the exception, because a
person needs the metrics when the datastore is already failing, and a
field that had to be turned on first would need a k3s restart on each
leader before anyone could see why.

**A field, and not always on.** The first build of this milestone set
the key in the static `config.yaml` with no field. The port is plain
HTTP with no authentication, because k3s offers neither for this
listener. Every pod and every device on the node network can reach it.
The metrics show the member IDs, the size of the datastore, the counts
and durations of requests, and the Go runtime's figures, and no key
and no value of the datastore. But each request to `/health` or
`/readyz` makes etcd read through its quorum, so anything that can
reach the port can add load to the datastore without a credential. A
home cluster's node network is often the house's LAN, and a cluster
can run pods that its owner does not trust. A `NetworkPolicy` selects
pods, so it cannot close the port to another machine on the node
network, and a `liken` machine has no firewall and no shell. The field
is the only way to close the port.

**A boolean named for the option of k3s.** `exposeMetrics` is the name
that a person finds in the documentation of k3s, as `debug` is. An
enumeration of the addresses to bind was set aside, because k3s offers
two states and no choice of address.

**A scrape job in the manual, and no object in the `monitoring`
component.** A `ScrapeConfig` of the prometheus-operator would fit
beside the `PodMonitor`s of `deploy/monitoring`. It was set aside for
now because `ScrapeConfig` is a `v1alpha1` kind that an older
prometheus-operator does not serve, and a fleet repository that
applies the component to such a cluster would fail on the unknown
kind. The manual gives the object for a person to add.

## The manual

`docs/content/docs/reference/metrics.md` gets a section on etcd: the
address, what the port serves, which machines serve it, how to close
it, and a scrape job in the plain Prometheus form and as a
`ScrapeConfig`. The descriptions of the fields in the CRDs generate
their reference pages.

## Tests

- `cluster/runtime_test.go`: the field resolves to `true` when it is
  unset and on a machine with no cluster document, and a document that
  sets `false` parses.
- `cluster/changes_test.go`: an edit of the field is a restart-class
  change.
- `init/k3s_test.go`: a leader renders the key by default, a leader of
  a cluster that sets `false` renders none, and a follower never
  renders it.
- `init/facts_test.go` and `machine/factstree_test.go`: the status
  value for each role, and its path through the facts tree.

## Drills

1. On a `lab` fleet with three leaders and a follower, from a pod on
   the cluster network, `GET http://<leader's InternalIP>:2381/metrics`
   returns `etcd_disk_wal_fsync_duration_seconds`. The same request to
   the follower's address is refused.
2. On that fleet, set `spec.runtime.etcd.exposeMetrics` to `false`.
   Each machine restarts k3s in its turn, the request to each leader's
   address is then refused, and
   `status.runtime.etcd.exposeMetrics` of each leader is absent.
3. On a `lab` fleet with one leader, the request to the leader's
   address is refused.
