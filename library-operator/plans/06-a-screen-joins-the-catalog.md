# A screen joins the catalog

Plan 06. The Corrosion sidecar in the idle pod, the catalog on the
screen's local disk, and the change stream the browser reads. At the
end of this plan every screen that runs the browser holds the
catalog, and a title added on the share reaches the screen's file
without the browser asking.

## The problem

The browser reads the catalog from a local file, and that file is
kept by an agent in the same pod. The idle pod is `media-operator`'s,
and today it holds exactly the containers `media-operator` gives it.
So either `media-operator` gains a notion of Corrosion, which the
one-way rule forbids, or the idle pod carries containers it has no
notion of, declared on the `Player` in a generic way.

## The change below: companions on the idle pod

This plan adds to `Player`, under `spec.idle`, a way to declare extra
containers and volumes for the idle pod, in the plain Kubernetes
shapes, and a termination grace period. `media-operator` adds them to
the pod as given and owns none of their meaning. The field is as
generic as the image field: an idle client that needs a helper
process gets one, whatever it is.

The sidecar in the pod is plan 03's, unchanged: Corrosion, the
schema, an `emptyDir` for the database, the API on localhost, and the
cluster's headless `Service` as its bootstrap list. Which namespace
that `Service` lives in, and how a `Player` in a room's namespace
reaches it, is settled here: gossip crosses namespaces by address,
and the operator publishes the address it expects screens to use.

## The catalog on a screen

A screen's sidecar holds every table, on an `emptyDir`, and rebuilds
it from its peers on every pod start. The proof of concept measured
that rebuild: a fresh node joining a cluster with 105,000 rows was in
full sync after 17 s, and a node that missed 50 writes caught up in
under a second. Two of its findings shape this pod:

- The first full sync leaves the agent at its ingest peak, up to
  380 MB, until it restarts, after which it holds 74 MB with the same
  rows. On a one-gigabyte box that gap matters, so the sidecar
  restarts once after its first sync, or the pod finds another way to
  the at-rest number. The builder chooses; the plan records that the
  at-rest number is the one to budget.
- A busy agent can take more than 30 s to stop, so the grace period
  above is set long enough, and the browser tolerates its sidecar
  being absent for a moment.

The browser opens the database read-only, and it must never write. It
subscribes to the sidecar's update stream for each table it shows,
and on each event it re-reads that row from the file. The stream has
no snapshot, so the browser reads the file first, then subscribes,
then re-reads anything the stream names. A dropped stream is
reopened; a reopened stream is followed by a full re-read, because
the events in between are gone.

## The local harness

`local/` gains the browser against the workstation's three-agent
cluster: the browser opens in a window, reads the local catalog, and
a row changed through one agent shows on screen. This is the loop a
person iterates the browser in, and it needs no cluster.

## What was set aside

Mounting the catalog from a shared export instead of a sidecar. That
was the Litestream design, and it is in [`rejected/`](rejected/).

A sidecar the browser talks to for reads. The API is for writes and
for the stream; reads from the file are faster and simpler and were
measured at under 4 ms.

## Proof

On `liken-1`: the idle pod on one `Player` carries the sidecar, the
sidecar joins the scanners' cluster and holds their rows, and the
browser reads a title from the file. A title added on the share and
picked up by a scanner appears in the screen's file, and the
browser's log shows the update event, within a few seconds. The
sidecar's resident memory after its restart is recorded on the box.
