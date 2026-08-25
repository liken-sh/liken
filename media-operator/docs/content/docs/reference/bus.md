---
title: The media bus
weight: 60
toc: true
---

# The media bus

The resources say what should exist. The bus carries what happens
while it runs: reports, commands, button events, and state. It is
one MQTT broker, and every running piece of this operator connects
to it: the operator, each playback pod, each standing remote pod,
and each idle pod. Your program can connect too. A phone app, a
Home Assistant instance, and a library application all join the same
way, with a plain MQTT client and no Kubernetes credentials.

The broker is `deploy/bus.yaml`: one Mosquitto `Deployment` with its
`Service` and `ConfigMap`, all named `bus`, beside the operator's
`Deployment` and never inside it. The two are separate so the
operator restarts without dropping a message: a button press reaches
`mpv` while the operator is down. The broker holds no volume, so a
broker restart loses only the retained set, and the next report from
each running Play refills it within seconds.

## The topic base

Every topic extends one base, `liken/media` by default. The operator
holds the base as one string and passes it to every pod it creates,
so the whole tree moves together when a cluster chooses another
base. The pages in this reference write topics without the base.

## Two rules shape the tree

State is retained and events are not. A retained topic always holds
the current value, so a program that just connected reads the live
state without asking. An event topic carries one moment: a button
press, a command, a request to move focus. When you add a
subscriber, you never poll; when you publish an event, it does not
linger.

The topic names the object and the payload does not. A Play's
namespace and name are segments of its topic path, and its report
body carries only the playback numbers. Parse the path, not the
payload, to learn which object spoke.

## The trees

Every topic on the bus belongs to one resource's tree, and that
resource's page gives the topics, the payloads, and who writes each.

| Tree | What it carries | Page |
|---|---|---|
| `plays/{namespace}/{name}/...` | one run's commands, report, and availability | [Plays](/docs/reference/plays/) |
| `players/{namespace}/{name}/...` | one unit's presentable state, volume, and panel | [Players](/docs/reference/players/) |
| `remotes/{namespace}/{name}/...` | one controller's events, presence, and focus | [Remotes](/docs/reference/remotes/) |
| `keymaps/{name}` | one Keymap's compiled binding table | [Keymaps](/docs/reference/keymaps/) |

The `keymaps` tree has no namespace segment because a `Keymap` is
cluster-scoped.

## Availability

Retained state outlives the pod that wrote it, so a reader needs a
signal that the writer is gone. The playback pod and the standing
remote pod each name an
`availability` topic in their tree as the MQTT Last Will, with
`offline` as the payload, and publish `online` there once connected.
Both messages are retained. When a pod dies without a clean
disconnect, the broker publishes the will, so retained state a dead
pod left behind does not read as live. A reader that folds state
from one of these trees reads the availability topic beside it.
