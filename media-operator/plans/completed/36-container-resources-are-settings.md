# 36, Container resources are settings

Built on 2026-09-27. Every container the operator builds states a cpu
request, a memory request, and a memory limit, so no pod of this
operator is in the `BestEffort` QoS class. No container the operator
builds or ships states a cpu limit. The values are settings: the
kustomize base generates the `ConfigMap` `media-operator-resources` from
`deploy/container-resources.yaml`, the operator reads the file once at
start, and a cluster owner changes a value with a `configMapGenerator`
merge in an overlay. The defaults fit a 1GB machine that drives a
1920x1080 screen, and their limits hold a film on a 3840x2160 screen.
This plan closes the open problem "no container states resource
requests". No drill has run on `liken-1` yet.

## The problem

No container this operator built stated a cpu or a memory request.
The player, the command sidecar, the display, the idle client, and
the long-running remote pod that reads controller input all had an
empty `resources` block beside their `resources.claims`. Every one of
them was in the `BestEffort` QoS class, so the kubelet evicted them
first under memory pressure, and the scheduler counted them as free.

The playback pod runs three containers, one of them a Vulkan client
with a graphics closure, on machines that `liken` deploys with as
little as 1GB of memory. So the question needed one decision for every
pod in this operator.

The decision had two parts: whether a container states a request at
all, and what number a screen machine can supply. A request that is
too large keeps a `Play` `Pending` on the machine that has its screen,
and the `Play` cannot run on any other machine.

## The design

**Three values per container, and no cpu limit.** Each container the
operator builds states a cpu request, a memory request, and a memory
limit. Under memory pressure the kubelet evicts first the pods whose
working set exceeds their request, and a `BestEffort` pod has a request
of zero. Among those pods it ranks by priority and then by how far the
working set exceeds the request. A cpu limit throttles the decoder or
the pass that the container runs, and the result is dropped frames or
a late status, so no container states one. This covers the three
`Deployments` the base ships as well.

**Requests for the common screen, limits for the large one.** The
requests are near the steady use of a 1920x1080 screen, so a playback
pod and the idle pod together request about 688Mi and schedule on a
1GB machine. The memory limits are above the highest use measured on a
3840x2160 screen, with about half again as headroom, so a film on that
screen is not killed at the defaults. A machine with a large screen
runs above its requests, and under memory pressure the kubelet ranks
its pods ahead of pods that stay within theirs.

| Container | Pod | cpu request | memory request | memory limit |
|---|---|---|---|---|
| `player` | playback | 80m | 432Mi | 1Gi |
| `display` | playback | 10m | 144Mi | 640Mi |
| `command` | playback | 10m | 16Mi | 32Mi |
| `idle` | idle | 5m | 96Mi | 256Mi |
| `reader` | a `Remote`'s pod | 1m | 4Mi | 16Mi |

This plan built the `command` request at 12Mi, and the two pods'
requests at about 684Mi.
[Plan 39](39-every-watch-wakes-the-pass.md) raised the `command`
request to 16Mi, because the pod build grew when the api's watches
moved to client-go in plan 38, and the table and the total above show
the values since then.

The shipped `Deployments` keep the requests and memory limits they
stated: 10m, 32Mi, and 64Mi for the operator, 10m, 32Mi, and 256Mi for
`media-api`, and 10m, 16Mi, and 32Mi for the bus. Each limit is at
least three times the highest use measured, and each manifest states
that measurement beside its limit. `media-api`'s limit is sized for
four ffmpeg muxes at once, which the eight days of samples did not
include.

**The settings file.** `deploy/container-resources.yaml` follows a
container's `resources` block, one entry per container name:

```yaml
player:
  requests: {cpu: 80m, memory: 432Mi}
  limits: {memory: 1Gi}
```

A `configMapGenerator` in `deploy/kustomization.yaml` makes the
`ConfigMap` from the file under the key `resources.yaml`, and the
`Deployment` mounts it at `/etc/media-operator`. The generator adds a
hash of the content to the name, and kustomize rewrites the volume to
that name, so a changed value rolls the operator's pod. The operator
reads the file once at start in `containerresources.go` and needs no
watch and no permission on the `ConfigMap`.

A value that is missing, or does not parse as a Kubernetes quantity,
takes the built-in default, and the operator logs one line that names
the container and the field. A `limits.cpu` is ignored with one line.
A memory request above its limit would make the API server refuse
every pod of that kind, so both values take their defaults, with one
line. A file that is absent or is not YAML gives every default, with
one line. The operator starts in every one of these cases. The shipped
file states the defaults exactly, and a test holds the two equal.

**Applied after the build.** The pod builders build the claims and the
containers, and `resourceSettings.apply` writes the requests and the
limit onto each container by name at the three places that create a
pod. It runs before the template hash is stamped, so a changed setting
recreates the idle pod and each `Remote`'s pod once, the way a changed
image does. A running playback pod keeps its values until its film
ends, because only a `Player` reshape recreates it.

## The measurements

Measured on a home cluster, from its Prometheus, over the eight days
of history it held. The working set is
`container_memory_working_set_bytes`, the number the kubelet ranks
eviction by. The cpu is the rate of `container_cpu_usage_seconds_total`
over five minutes. The percentiles are over one-minute samples. The
films in those eight days played on a 3840x2160 screen and on a
1920x1080 screen.

| Container | Screen | Working set p90 / p99 / max | cpu p90 / p99 |
|---|---|---|---|
| `player` | 3840x2160 | 550 / 615 / 617 Mi | 76 / 94 m |
| `player` | 1920x1080 | 431 / 521 / 522 Mi | 83 / 86 m |
| `display` | 3840x2160 | 400 / 401 / 416 Mi | 5 / 10 m |
| `display` | 1920x1080 | 141 / 187 / 188 Mi | 6 / 16 m |
| `command` | either | 11 / 12 / 13 Mi | 9 / 9 m |
| `reader` | none | 3.4 / 4.1 / 4.8 Mi | 0.1 / 0.2 m |
| `operator` | none | 14 / 15 / 17 Mi | 5 / 9 m |
| `api` | none | 13 / 14 / 16 Mi | 5 / 7 m |
| `bus` | none | 4.6 / 4.9 / 5.5 Mi | 0.6 / 0.8 m |

The command sidecar and the reader run the pod build of the operator's
program, which leaves out the client-go leader election of plan 37.
On a laptop, the pod build's resident memory at start is within 1MB of
the build these measurements come from, about 10MB, where the full
build's is about 21MB. So the measurements hold for them.

The display's working set is mostly shared memory for the buffers of
its surface, which grow with the screen. Its resident set is about
20Mi. The kernel cannot drop the shared buffers, so the working set is
the number to request.

The home cluster gives its idle screens to another controller through
`spec.idle.controller`, so it runs no `idle` container. The idle value
comes from `liken-1` instead: 95Mi of working set and 1m of cpu on a
3840x1600 screen, steady over one minute of samples. The cpu request
is rounded up from that one sample, and the limit of 256Mi allows for
a larger screen.

## What was set aside

**Requests sized for a 3840x2160 screen.** A playback pod at those
numbers requests about 908Mi, and with the idle pod about 1004Mi. A
1GB machine cannot schedule that beside k3s and the hardware
operators, so a `Play` there would stay `Pending` with `Insufficient
memory`, even on a 1920x1080 screen that uses much less.

**No limits.** A memory limit makes the kernel kill a container that
reaches it, and a player killed in the middle of a film is a bad
result. Each limit is above the highest use measured at 3840x2160,
with headroom, so a default limit does not kill a film that behaves
the way the measured films did, and a runaway container is killed
before it takes the machine.

**Constants in the code.** A value in Go needs a build to change. The
correct number depends on the cluster's screens and machines, which
only the cluster owner knows.

**A watch on the `ConfigMap`.** The generated name changes with the
content, so a change reaches the operator as a new pod. A watch would
need a permission and a loop for a value that changes a few times in
a cluster's life.

**Requests from the screen's mode, or from the `Player`'s spec.** A
request that scales with the mode of the `Display` the `Player`
resolved would fit each screen, and a field on the `Player` would let
each unit state its own size. Both add code or API for what one
setting per cluster covers today.

## How it was proved

`containerresources_test.go` parses a full file, a file with one value
missing, a quantity that does not parse, a `limits.cpu`, a memory
request above its limit, an unknown container, a file that is not
YAML, an empty file, and an absent file, and checks each value and
each log line. It holds the shipped file equal to the defaults. It
runs a pass and the idle and `Remote` reconciles with custom settings,
and checks that each container of the pods they create has exactly the
configured requests and memory limit and no cpu limit.
`standing_test.go` checks that a changed setting changes a standing
pod's hash. A local `kubectl kustomize` of an overlay with
`behavior: merge` produced the changed file under a new `ConfigMap`
name, and the `Deployment`'s volume named it.

A drill on `liken-1` is owed: roll the build, confirm
`kubectl get pod -o jsonpath='{.status.qosClass}'` reads `Burstable`
for the playback, idle, and remote pods, patch one value through an
overlay, and confirm the operator restarts and the idle and remote
pods are recreated once with the new value.
