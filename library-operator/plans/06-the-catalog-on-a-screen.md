# The catalog on a screen

Plan 06. The Corrosion sidecar in the idle pod, the catalog on the
screen's local disk, and the update stream the media browser reads. At
the end of this plan every screen that runs the media browser has the
catalog, and a title added on the volume reaches the screen's file with
no request from the media browser.

## The problem

The media browser reads the catalog from a local file, and an agent in
the same pod keeps that file. The idle pod is `media-operator`'s, and it
has exactly the containers `media-operator` gives it. So either
`media-operator` gains a notion of Corrosion, which the one-way rule
forbids, or the idle pod accepts containers declared on the `Player` in
a generic way.

## The change below: extra containers on the idle pod

This plan adds to `Player`, under `spec.idle`, a way to declare extra
containers, volumes, and volume mounts for the idle pod, in the plain
Kubernetes shapes, and a termination grace period. `media-operator` adds
them to the pod as given and assigns them no meaning. The field is as
generic as the image field: an idle client that needs a helper process
or a mount gets one.

The sidecar in the pod is plan 03's, unchanged: Corrosion, the schema,
an `emptyDir` for the database, the API on localhost, and the short
name `catalog` as its bootstrap list. That name resolves to the
`Service` in the pod's own namespace, so a screen joins the catalog
cluster of the namespace its `Player` is in, and it sees that
namespace's libraries and no others. The operator adds the idle pod to
that namespace's `EndpointSlice`, so a screen is a bootstrap peer for
the next screen when no scanner is up.

## The catalog on a screen

A screen's sidecar stores every table on an `emptyDir` and rebuilds it
from its peers on every pod start. The proof of concept measured that
rebuild: a fresh node joining a cluster with 105,000 rows was in full
sync after 17 s, and a node that missed 50 writes caught up in under a
second. Two findings shape this pod.

- The first full sync leaves the agent at its ingest peak, up to 380 MB,
  until it restarts, after which it uses 74 MB with the same rows. So
  the sidecar restarts once after its first sync, or the pod finds
  another way to the at-rest number. The builder chooses. The at-rest
  number is the one the design budgets.
- A busy agent can take more than 30 s to stop, so the grace period is
  set long enough, and the media browser tolerates an absent sidecar for
  a moment.

The media browser opens the database read-only and never writes. It
subscribes to the sidecar's update stream for each table it shows, and
on each event it re-reads that row from the file. The stream has no
snapshot, so the media browser reads the file first, then subscribes,
then re-reads anything the stream names. A dropped stream is reopened,
and a reopened stream is followed by a full re-read, because the events
in between are gone.

## The local harness

`local/` gains the media browser against the workstation's three-agent
cluster: the media browser opens in a window, reads the local catalog,
and a row changed through one agent appears on screen. This is the loop
a person iterates the media browser in, with no cluster.

## What was set aside

A catalog mounted from a shared volume instead of a sidecar. That was
the Litestream design, in [`rejected/`](rejected/).

Reads through the sidecar's API. The API is for writes and for the
stream. Reads from the file measured under 4 ms.

## Proof

On `liken-1`: the idle pod on one `Player` runs the sidecar, the sidecar
joins the scanners' cluster and receives their rows, and the media
browser reads a title from the file. A title added on the volume and
indexed by a scanner appears in the screen's file within a few seconds,
and the media browser's log shows the update event. The sidecar's
resident memory after its restart is recorded on the box.
