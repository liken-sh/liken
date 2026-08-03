# The apiservers abort requests that liken's operators send

Open problem. The apiservers log "Timeout or abort while handling" and
"Post-timeout activity" for requests that liken's own operators sent.
Neither operator reports an error, so from liken's side every request
succeeds, and the noise lands in the apiserver's log at ERROR.

Two operators send the requests. The cluster operator's sweep probes
for the flux engine's Deployment. Each machine operator writes its own
Machine status and reads its own Machine and Node. Both build their
client with `kubernetes.InClusterClientAt`.

## What the log pair means

The pair comes from the apiserver's timeout filter. It fires on two
different events: the handler ran past its deadline, or the handler
aborted because the client went away. The line does not say which one
happened, which is why the diagnosis has stayed open.

`ResponseHeaderTimeout` in `kubernetes/apiclient.go` is the client's
only ten-second deadline, so a client-side give-up is the first
suspect on liken's side.

## Where the aborts land

Raising `engineProbeInterval` to 60 seconds
(`cluster-operator/flux.go`) cut the count and left the fault alone.
When a five-machine fleet took that release, its aborts fell from
about 2,400 a day to about 1,050, which is close to the probe's own
share of them.

Measure on a window with no rolling reboot in it. A fleet roll lifts
the count by a third for about a day afterwards. Over 24 quiet hours,
with the probe asking once a minute, that fleet aborted 1,043
requests, which is 2,086 ERROR lines:

* 621 on `PUT /apis/liken.sh/v1alpha1/machines/<name>/status`
* 227 on the engine probe's Deployment GET
* 110 on other paths, most of them `GET /api/v1/nodes/<name>`
* 85 on `GET /apis/liken.sh/v1alpha1/machines/<name>`

The probe's count fell by six, in step with the interval, and its rate
did not fall with it: 227 of the 1,440 probes a day is 16%, against
the 13% measured while it asked every 10 seconds. The interval bought
quiet at the same fault rate.

The two machine paths are 68% of the total. A four-machine lab fleet
showed the same shape from the other direction: no abort landed on the
engine probe's path at all, and every abort landed on a machine
operator's Node and Machine status writes. So the machine operator is
where the next measurement starts.

## What is ruled out

The aborts include requests to `127.0.0.1:6443`. The network between
machines is not the cause, and the loopback case reproduces on one
machine.

Three aborts landed on
`GET .../leases/kube-controller-manager?timeout=5s`, whose client is
k3s's own controller-manager and not liken. That is a small number,
and it is the first sign that the stall is not a property of
`kubernetes/apiclient.go`.

## The client goes away, at least sometimes

A small share of the aborts carry the write that failed. On nuc4, for
a `PUT .../machines/nuc4/status`, five lines land inside two
milliseconds, in this order:

```
writers.go:123  apiserver was unable to write a JSON response:
                write tcp 127.0.0.1:6443->127.0.0.1:46172:
                write: connection reset by peer
status.go:71    apiserver received an error that is not an
                metav1.Status: ... connection reset by peer
wrap.go:53      Timeout or abort while handling
                PUT /apis/liken.sh/v1alpha1/machines/nuc4/status
writers.go:136  apiserver was unable to write a fallback JSON
                response: http: Handler timeout
timeout.go:140  Post-timeout activity timeElapsed=1.375445ms
```

The write to the client fails before the abort line, and the peer is
on loopback. For this request the client reset the connection while
the apiserver was writing the response, which is the abort branch of
the filter and not a slow handler. The apiserver then failed to write
its fallback too, because the request was already past its deadline.

This is a small minority of the traffic. Over the same quiet 24 hours
the fleet logged 13 lines carrying `connection reset by peer` and 75
carrying "unable to write a JSON response", against 1,043 aborts. So
this explains about 1% of them, and what the other 99% do is
unmeasured. The next measurement should establish whether every abort
is a client reset that the apiserver did not happen to log, or whether
two different things share one log line.
