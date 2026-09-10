---
title: The media bus
weight: 60
toc: true
---

# The media bus

The resources say what should exist. The bus carries what happens
while it runs: reports, commands, button events, and state. It is
one MQTT broker, and it is a contract in the same sense the CRDs
are: this page gives the rules every topic follows and lists every
topic under the media operator's tree, with its writer, its readers,
and its payload. Each resource page links here for the rules and
gives the payload shapes for its own topics.

Three operators meet on the broker. The media operator, its playback
pods, each `Remote`'s pod, and each idle pod connect to it. The
[library operator](https://library.liken.sh/) runs its own tree on
the same broker. The
[equipment operator](https://equipment.liken.sh/) writes into the
media operator's `players` tree. Your program can connect too. A
phone app, a Home Assistant instance, and a library application all
join the same way, with a plain MQTT client and no Kubernetes
credentials.

The broker is `deploy/bus.yaml`: one Mosquitto `Deployment` with its
`Service` and `ConfigMap`, all named `bus`, beside the operator's
`Deployment` and never inside it. The two are separate so the
operator restarts without dropping a message: a button press reaches
`mpv` while the operator is down. The broker holds no volume, so a
broker restart loses only the retained set. Each connected program
republishes the retained state it owns when its session reconnects,
and the next report from each running `Play` refills the rest within
seconds.

## The topic base

Every topic extends one base. The media operator's base is
`liken/media` by default, and the library operator's is
`liken/library`. Each operator holds its base as one string and
passes it to every pod it creates, so a whole tree moves together
when a cluster chooses another base. The pages in this reference
write topics without the base.

## Retained state and events

State is retained and events are not. A retained topic always holds
the current value, so a program that just connected reads the live
state without asking. An event topic carries one moment: a button
press, a command, a request to move focus.

An empty retained payload clears a topic. The writer of a retained
topic clears it that way when the object it described is gone, and a
reader treats an empty payload as a cleared value, not as a live
message. The media operator ignores an empty payload on a status, an
availability, a panel, or a volume topic for that reason. It
publishes those clears itself, and reading one back as a signal
would act on a run or a unit that no longer exists.

An operator clears the topics of an object it owns on a finalizer, so
the object is never gone from the API server while its topics still
stand on the broker. The media operator's finalizer on a `Play` is
`media.liken.sh/bus-topics`. A sweep on every pass is the backstop
behind it: it clears the topics of any run the pass's own list of
`Play`s does not hold, which is what finds a run whose clear the broker
never received.

## The topic names the object

The topic names the object and the payload does not. A `Play`'s
namespace and name are segments of its topic path, and its report
body carries only the playback numbers. Parse the topic path to learn
which object a message belongs to.

## Availability

Retained state outlives the pod that wrote it, so a reader needs a
signal that the writer is gone. The playback pod and the `Remote`'s
pod each name an `availability` topic in their tree as the MQTT Last
Will, with `offline` as the payload, and publish `online` there once
connected. Both messages are retained. When a pod dies without a
clean disconnect, the broker publishes the will, so retained state a
dead pod left behind does not read as live. A reader that folds state
from one of these trees reads the availability topic beside it.

The same pattern carries the owner mark on a unit's level. The
equipment operator names the mark's topic as its Last Will with an
empty retained payload, so a dead session clears its own mark.

## The trees

Two operators publish trees on the broker. Each tree's page lists its
topics, and the resource pages give the payloads.

| Tree | Owner | What it carries | Page |
|---|---|---|---|
| `liken/media` | the media operator | runs, units, and controllers: the table below | this page |
| `liken/library` | the library operator | library reports, catalog availability, and play requests | [The library bus](https://library.liken.sh/docs/reference/bus/) |

The equipment operator publishes no tree of its own. Its receiver
session writes into the media tree's `players` branch, on the volume
topic and the owner mark beside it, and
[its reference](https://equipment.liken.sh/docs/reference/receivers/)
describes that session.

## The media tree

Every topic under `liken/media`. The writer column names every
program that publishes on the topic, the clear included. The
readers column names every program that subscribes. The payload
column links to the section of the resource page that gives the
shape.

| Pattern | Writer | Readers | Retained | Payload |
|---|---|---|---|---|
| `plays/{namespace}/{name}/commands` | any program | the playback pod | no | [one named command](/docs/reference/plays/#commands) |
| `plays/{namespace}/{name}/status` | the playback pod; the operator clears it | the operator | yes | [the run's report](/docs/reference/plays/#status-1) |
| `plays/{namespace}/{name}/availability` | the playback pod and its Last Will; the operator clears it | the operator | yes | [`online` or `offline`](/docs/reference/plays/#availability) |
| `players/{namespace}/{name}/status` | the operator | the idle pod, or a delegate's client | yes | [the unit's name, activity, `Play`, and parts](/docs/reference/players/#status-1) |
| `players/{namespace}/{name}/volume` | the operator, the pod that handles a press, and the equipment operator | the playback pod, the idle pod or a delegate's client, the equipment operator, and the operator | yes | [the level and the muted flag](/docs/reference/players/#volume) |
| `players/{namespace}/{name}/volume/owner` | the equipment operator and its Last Will | the playback pod, a delegate's client, and the operator | yes | [the owner mark, or empty](/docs/reference/players/#volumeowner) |
| `players/{namespace}/{name}/panel` | the idle pod, or a delegate's client; the operator clears it | the operator | yes | [the panel desire](/docs/reference/players/#panel) |
| `players/{namespace}/{name}/commands` | the playback pod | the idle pod, or a delegate's client | no | [the ask for the next work](/docs/reference/players/#commands) |
| `remotes/{namespace}/{name}/events` | the `Remote`'s pod | the playback pod, the idle pod, or a delegate's client | no | [one key event](/docs/reference/remotes/#events) |
| `remotes/{namespace}/{name}/keys` | the operator | the `Remote`'s pod | yes | [the compiled key table](/docs/reference/remotes/#keys) |
| `remotes/{namespace}/{name}/codes` | the `Remote`'s pod | the operator | yes | [the declared code set](/docs/reference/remotes/#codes) |
| `remotes/{namespace}/{name}/availability` | the `Remote`'s pod and its Last Will | the operator | yes | [`online` or `offline`](/docs/reference/remotes/#availability) |
| `remotes/{namespace}/{name}/focus` | the operator | the playback pod, the idle pod or a delegate's client, and the operator | yes | [the name of a `Player`, or empty](/docs/reference/remotes/#focus-and-focuscycle) |
| `remotes/{namespace}/{name}/focus/cycle` | the holder of focus | the operator | no | [empty](/docs/reference/remotes/#focus-and-focuscycle) |

The `players` commands topic has one writer. The playback pod
publishes `play-next` when a person takes the up-next offer on the
scrubber, and the client that wrote the `Play` reads it and creates
the next `Play`.

"The playback pod" in this table is its command sidecar, the one
container that connects to the bus. "The idle pod" is the idle screen
client the media operator ships. A delegate's client, under
[another controller](/docs/guides/handing-the-idle-screen-to-another-controller/),
reads and writes the same `players` and `remotes` topics from
`status.idle.bus`.

## What the operator reads

The operator subscribes to nine filters, one per retained or event
kind it folds into a status or a decision:

| Filter | What the operator does with it |
|---|---|
| `plays/+/+/status` | folds each report into the `Play`'s status |
| `plays/+/+/availability` | gates a retained report on a live sidecar |
| `remotes/+/+/focus` | reads its own marks back after a restart |
| `remotes/+/+/focus/cycle` | advances the mark to the next bound `Player` |
| `remotes/+/+/availability` | gates the declared codes on a live pod |
| `remotes/+/+/codes` | subtracts the key table and reports `status.unbound` |
| `players/+/+/panel` | overrides the screen's `Display` from the desire |
| `players/+/+/volume` | learns which units already hold a level, so the seed writes only where nothing stands |
| `players/+/+/volume/owner` | builds a playback pod with no level of its own while a mark stands |

Presses never pass through the operator. A key event travels from
the `Remote`'s pod to the playback pod or the idle client directly,
gated on the retained focus mark, so a controller keeps working while
the operator is down.
