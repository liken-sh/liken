# 99. A leader serves etcd's metrics on the node network

Milestone 99. Proposed and built on 2026-11-21. The drill below has
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

`image/etc/rancher/k3s/config.yaml`, the static configuration of a
leader, sets `etcd-expose-metrics: true`. Every leader then serves
etcd's metrics at `http://<node IP>:2381/metrics`.

The key is in the static file and not in `boot.yaml`, because it is
the same on every leader and comes from no field of the `Cluster` or
the `Machine`. `init/k3s.go` renders only what a boot derives.

The key reaches a machine with the release that has it, on the reboot
that the upgrade already needs. No k3s restart and no step by a person
is needed.

- **A cluster with one leader** uses sqlite, so nothing listens on
  port 2381 and the machine behaves as before.
- **A cluster that grows from one leader to several** converts the
  founder's datastore to etcd on the boot that adds the second leader.
  The key is already set, so the founder serves the metrics from that
  boot, and each new leader serves them from its first boot.
- **A follower** reads `agent.yaml`, which does not have the key,
  because a k3s agent refuses a server key. A leader that is demoted
  boots as a follower, starts no etcd, and serves nothing on the port.
  k3s removes the `etcd` role label from its `Node`, so a scrape job
  that selects on the label drops the target.

## Choices

**Always on, with no field.** A field of the `Cluster` that defaults
to on would let a person close the port. It was set aside because the
metrics ports of the operators have no such field either, and because
a person who has turned the port off cannot diagnose the datastore of
a cluster that is failing now: the change needs a reboot of each
leader. If a cluster needs the port closed, a `NetworkPolicy` cannot
do it, because the port is on the host. That cluster needs a field,
which a later milestone can add without a change to this one.

**Plain HTTP with no authentication.** k3s offers no TLS and no
authentication for this listener. The port is on the node network
only, and not on the other addresses of a machine. That is narrower
than `machine-operator`, which serves its metrics on port 9200 of
every address. The metrics show the member IDs, the size of the
datastore, the counts and durations of requests, and the Go runtime's
figures. They show no key and no value of the datastore. The client
port 2379 and the peer port 2380 still require a certificate from the
cluster's etcd CA.

**A scrape job in the manual, and no object in the `monitoring`
component.** A `ScrapeConfig` of the prometheus-operator would fit
beside the `PodMonitor`s of `deploy/monitoring`. It was set aside for
now because `ScrapeConfig` is a `v1alpha1` kind that an older
prometheus-operator does not serve, and a fleet repository that
applies the component to such a cluster would fail on the unknown
kind. The manual gives the object for a person to add.

## The manual

`docs/content/docs/reference/metrics.md` gets a section on etcd: the
address, what the port serves, which machines serve it, and a scrape
job in the plain Prometheus form and as a `ScrapeConfig`.

## Tests

`init/k3s_static_test.go` reads the static files from the repository.
One test fails when `config.yaml` does not set the key, and one fails
when `agent.yaml` sets it.

## Drills

1. On a `lab` fleet with three leaders and a follower, from a pod on
   the cluster network, `GET http://<leader's InternalIP>:2381/metrics`
   returns `etcd_disk_wal_fsync_duration_seconds`. The same request to
   the follower's address is refused.
2. On a `lab` fleet with one leader, the request to the leader's
   address is refused.
