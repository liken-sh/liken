# The webhook, reachable

Plan 19. A way in. At the end of this plan Radarr, Sonarr, and Jellyfin
can tell a scanner that a title arrived, and the `Library` says the
address to send it to.

## The problem

The scanner already accepts the webhook. It listens on port 8090, reads
the changed path out of the payload shapes the `*arr` tools and Jellyfin
send, maps that path onto the volume, rescans the title or series folder
it names, and prunes within that folder. A payload it cannot map to the
volume falls back to a full walk, so a hook is never worse than the
timer.

Nothing can reach it. The port is on the pod and no `Service` stands in
front of it, so the address is a pod IP that changes with every roll.
Nothing tells a person the address either. The one path a new file takes
into the catalog today is the five-minute timer, and the fast path is
built and unused.

## The Service and the address

The operator stands one `Service` per `Library`, in the `Library`'s
namespace, named for the scanner pod it fronts and owned by the
`Library`. Deleting the `Library` takes it, the way deleting a `Library`
takes its pod.

It is an ordinary `Service` with a selector. The scanner pod already
carries the two labels that name it: the label that says it is a
scanner, and the label that says which `Library` it belongs to. Those
two select exactly one pod, so the API server keeps the endpoints and
this operator writes no `EndpointSlice`. That is the difference from the
catalog `Service`, which fronts the pods of every `Library` in the
namespace and names no selector.

The port is 8090, TCP, named `webhook`, and the scanner container
declares it, so a person reading the pod sees the port without reading
this operator's source.

The `Library` reports the address. `status.webhook` carries the URL a
person pastes into Radarr, Sonarr, or Jellyfin:

```
http://movies-scanner.media.svc:8090/
```

So the address is one `kubectl get` away, and it is stable across every
roll of the pod.

The handler itself does not change.

## What was considered and set aside

**A trigger for a full re-walk.** An annotation on the `Library` that
the operator turns into a rescan would give a person a way to demand a
walk with no webhook. The timer already walks the whole root, and a
`Library` whose spec changes rolls its pod, whose first act is a full
walk. A person who wants a walk now deletes the scanner pod. This stays
out until something asks for it.

**One shared webhook endpoint for every `Library`.** One address, with
the `Library` named in the payload or the path. The tools send their own
payload shape and do not name a `Library`, and the scanner is the thing
that holds the volume open. One address per `Library` is the address
that means something.

**An Ingress or a node port.** The media servers that send these hooks
run in the same cluster, so a cluster address is the whole requirement.
A hook from outside the cluster is a routing question the cluster's
owner already answers for every other service.

## The proof

The unit tests cover the built `Service`, the address in the status, and
the ownership that takes the `Service` with the `Library`.

The drill runs on `liken-1`. Read `status.webhook`, post a Radarr import
payload to it from a pod in the cluster, and confirm from the scanner's
log that it rescanned that one title folder and not the whole root.
Confirm the `Service` goes when the `Library` goes.
