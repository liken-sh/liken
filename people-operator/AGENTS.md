# Working on people-operator

`people-operator` is a Go Kubernetes operator. `README.md` says what
it is for and describes its kind.

## How it runs

The operator is one unprivileged `Deployment` in `liken-system`, with
leader election. It watches every `Person` and its own baker pods, and
it writes the status of each `Person`.

The operator reads an `https://`, `http://`, or `data:` picture in its
own process. Only the kubelet can mount an NFS export or a claim. So
for an `nfs://` or `claim://` picture, the operator creates a baker
pod. A baker pod runs the operator's image as `people-operator bake`,
with the picture's source mounted read-only. A baker pod for an
`nfs://` picture runs in the operator's namespace. A baker pod for a
`claim://` picture runs in the claim's namespace, because a pod can
mount a claim only from its own namespace. The baker pod prints its
result as one line of JSON to its log, and the operator reads it from
there.

## Design

A picture comes from a source that the operator doesn't control. So a
baker pod mounts no service account token, and the code that decodes
the picture in that pod has no credentials for the API server. A
fetch in the operator's process has a timeout, a limit on the size of
the response body, and a limit on the size of the image. So a slow or
hostile server can't stall or exhaust the operator.

No HTTP URL, NFS file, or file on a claim has a change feed to watch.
So the operator reads each source again on a timer, except a `data:`
URI. Each read is a conditional GET, or a comparison of the file's
modification time and size, so an unchanged picture causes no status
write. An annotation on the `Person` asks for a read at once.

`people-operator` reads no other operator's resources. An operator
that refers to a `Person` depends on this CRD and on nothing else in
this component.

The reasons for these decisions are in `plans/`. Read the relevant
plan before you change how something works.
