---
title: Objects the operator creates
weight: 70
---

The operator creates pods, `Service`s, `ResourceClaim`s, `ConfigMap`s,
and `Job`s in the `observatory` namespace while a reservation runs, and
deletes all but the `Job`s when it ends. Kubernetes deletes each `Job`
an hour after it finishes. Their names, ports, and labels are stable,
so your own `Service` or `NetworkPolicy` can select them. Do not edit
the objects themselves: the operator deletes them at the end of each
reservation, and creates them again for the next one.

## Names

The operator names each pod, `Service`, and `ResourceClaim` it creates
`<resource-name>-<kind>`, with the kind in lowercase. The `Mount`
`east` runs in the pod `east-mount`, the `Camera` `east-main` in
`east-main-camera`, and the INDI server of the `Telescope` `east` in
`east-telescope`. So `kubectl get pods` lists one telescope's pods
together. Give a device the name of its telescope, and add a word only
when the telescope has two devices of one kind, as the example does
for its cameras.

A generated name must be a DNS label of 63 characters or fewer,
because each shim dials its device by the `Service` name. The operator
refuses a resource whose generated name breaks that rule, and the step
that needs the name fails with a message that gives the longest
resource name that fits.

## Ports

| Pod | Name | Port | What listens |
|---|---|---|---|
| an INDI server, such as `east-telescope` or `lab-observatory` | `indi` | 7624 | `indiserver`, for KStars and every other INDI client |
| a device, such as `east-mount` | `driver` | 7625 | the device's driver, for its INDI server only |
| a guider, such as `east-guider` | `events` | 4400 | PHD2's event server |

Each `Service` has the same name as its pod, the same port, and the
type `ClusterIP`. No port has authentication or encryption, because
INDI and PHD2 have none.

## Labels

Every pod and `Service` the operator creates has these labels:

| Label | Value |
|---|---|
| `app.kubernetes.io/managed-by` | `observatory-operator` |
| `app.kubernetes.io/part-of` | `observatory` |
| `app.kubernetes.io/name` | the object's name, such as `east-telescope`. Each `Service` selects its pod by this label. |
| `observatory.liken.sh/role` | `server`, `device`, or `guider` |
| `observatory.liken.sh/server` | the INDI server the pod belongs to, such as `east-telescope` |
| `observatory.liken.sh/kind` | the kind of the resource that caused the object, such as `Telescope` or `Camera` |
| `observatory.liken.sh/resource` | that resource's name, such as `east` |

A `job` action's `Job` has the label `observatory.liken.sh/role: job`.
To select one telescope's INDI server, use
`observatory.liken.sh/kind: Telescope` and
`observatory.liken.sh/resource: <telescope>`. To select its guider,
use `observatory.liken.sh/kind: Guider` and
`observatory.liken.sh/resource: <guider>`.

## Placement

The scheduler places most pods. The guider's pod, and the camera of the
`OpticalTrain` that the telescope's `Guider` names, have a required pod
affinity to the telescope's server, so they run on the server's node
and the guide frames cross no link between nodes. A guide camera with a
`spec.claim` gets no affinity, because the node of its device decides
where it runs. A telescope with no `Guider` has no affinity on any
pod. The guider's pod, `Service`, and `ConfigMap` take the name
`<guider>-guider`, such as `east-guider`. A `job` action's `Job` takes
the kind, the resource, the trigger, and a hash, such as
`observatory-lab-activation-a799431dd4` ([Procedures](/docs/concepts/procedures/)).

## Finalizers

The operator adds the finalizer `observatory.liken.sh/deactivate` to
each resource it runs something for, so a delete waits until the
operator has stopped it.
[Deleting a running resource](/docs/concepts/how-a-reservation-runs/#deleting-a-running-resource)
lists when each kind holds it.
