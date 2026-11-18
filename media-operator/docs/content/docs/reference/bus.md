---
title: The media bus
weight: 60
toc: true
---

# The media bus

The resources declare what should exist. The programs that play media
send each other status reports, commands, button presses, and state
through one MQTT broker. The media operator, its playback pods, each
`Remote`'s pod, and each idle pod connect to it, and the
[library operator](https://liken.sh/library/) publishes its own topics
on the same broker. Your own programs can connect too: a phone app or a
Home Assistant instance connects with a plain MQTT client and needs no
Kubernetes credentials, because the broker accepts anonymous clients.

This page gives the rules that every topic follows and lists every
topic of the media operator, with its writers, its readers, and its
payload. Each resource page gives the payloads of its own topics.

The broker is `deploy/bus.yaml`: one Mosquitto `Deployment` with its
`Service` and `ConfigMap`, all named `bus`. It is a separate
`Deployment` from the operator, so a button press still reaches `mpv`
while the operator restarts.

The broker has no storage volume, so a broker restart loses the
retained messages. When each program reconnects, it publishes the
retained state that it owns again, and the next status report of each
running `Play` restores the rest. When the operator's write queue is
full, its client drops the publish. On its next pass, the operator
publishes again each `Player` status, key table, and clear that it
dropped.

## The topic base

Every topic starts with a base. The media operator's base is
`liken/media` by default, and the library operator's is `liken/library`.
Each operator passes its base to every pod that it creates, so when you
set another base, all the topics move under it. The pages in this
reference write topics without the base.

## Retained state and events

State topics are retained and event topics are not. The broker keeps
the current value of a state topic and sends it to each new subscriber.
An event topic carries button presses, commands, and requests to move
the focus, and the broker keeps none of them.

An empty retained payload clears a topic. The writer of a retained topic
publishes one when the object that the topic describes is gone. A reader
treats an empty payload as "no value", not as a message. The media
operator ignores an empty payload on a status, availability, panel, or
volume topic, because it publishes those clears itself and the run or
`Player` is already gone.

The operator clears the topics of a `Play` through the finalizer
`media.liken.sh/bus-topics`, so the `Play` stays in the API server until
the operator's client has queued the clears of its `status` and
`availability` topics. Each pass also clears the topics of every run
that has no `Play`, in case a clear never reached the broker.

## The topic names the object

The payload does not say which object it belongs to. Read the
namespace and name from the topic. For example, a `Play`'s status report
contains only playback values, and its namespace and name are segments
of its topic.

## Availability

Retained state stays on the broker after the pod that wrote it is gone,
so a reader must also learn whether the writer is still running. The
playback pod and the `Remote`'s pod each register their `availability`
topic as their MQTT Last Will, with the payload `offline`, and publish
`online` to it after they connect. Both messages are retained. When a
pod dies without disconnecting cleanly, the broker publishes the Last
Will, so a reader does not take the dead pod's state for live state. A
reader of a `Play`'s or a `Remote`'s state also reads its
`availability` topic.

The playback pod adds a space and its pod UID after the word, and puts
the same UID in each status report. The new pod of a run has the same
name as the old pod, and the broker can publish the old pod's Last Will
after the new pod is online. A reader uses the UID to tell the two pods
apart.

## The trees

Each operator publishes its topics under its own base.

| Tree | Owner | What it carries | Page |
|---|---|---|---|
| `liken/media` | the media operator | runs, `Player`s, and controllers, listed in the table below | this page |
| `liken/library` | the library operator | library reports, catalog availability, and play requests | [The library bus](https://liken.sh/library/docs/reference/bus/) |

No device operator connects to the bus. The media operator writes a
`Player`'s requested volume, power state, and input into the
[`Receiver`](https://liken.sh/equipment/docs/reference/receivers/) or
the [`Sink`](https://liken.sh/audio/docs/reference/sinks/) that serves
the `Player`, and reads back what the device reports there.

## The media tree

These are all the topics under `liken/media`. The Writer column
includes the program that clears the topic. The Payload column links to
the resource page that gives the payload.

| Pattern | Writer | Readers | Retained | Payload |
|---|---|---|---|---|
| `plays/{namespace}/{name}/commands` | any program | the playback pod | no | [one named command](/docs/reference/plays/#commands) |
| `plays/{namespace}/{name}/status` | the playback pod, and the operator to clear it | the operator | yes | [the run's status report](/docs/reference/plays/#status-1) |
| `plays/{namespace}/{name}/availability` | the playback pod and its Last Will, and the operator to clear it | the operator | yes | [`online` or `offline`, and the pod's UID](/docs/reference/plays/#availability) |
| `players/{namespace}/{name}/status` | the operator | the idle pod, or a delegate's client | yes | [the `Player`'s name, activity, `Play`, and parts](/docs/reference/players/#status-1) |
| `players/{namespace}/{name}/volume` | the operator | the playback pod, the idle pod or a delegate's client, and the operator | yes | [the level, the muted flag, and which program draws the indicator](/docs/reference/players/#volume) |
| `players/{namespace}/{name}/volume/commands` | any program that is not a remote | the operator | no | [a request for a volume step or a mute](/docs/reference/players/#volumecommands) |
| `players/{namespace}/{name}/panel` | the idle pod, or a delegate's client, and the operator to clear it | the operator | yes | [whether the panel should be on or off](/docs/reference/players/#panel) |
| `players/{namespace}/{name}/power` | the operator | the idle pod, or a delegate's client | no | [a command to wake or sleep the screen](/docs/reference/players/#power) |
| `players/{namespace}/{name}/commands` | the playback pod, and any program that requests the home page | the idle pod or a delegate's client, and the operator | no | [a request for the next `Play`, or for the home page](/docs/reference/players/#commands) |
| `remotes/{namespace}/{name}/events` | the `Remote`'s pod | the playback pod, the idle pod or a delegate's client, and the operator | no | [one key event](/docs/reference/remotes/#events) |
| `remotes/{namespace}/{name}/keys` | the operator | the `Remote`'s pod | yes | [the compiled key table](/docs/reference/remotes/#keys) |
| `remotes/{namespace}/{name}/codes` | the `Remote`'s pod | the operator | yes | [the set of codes the controller declares](/docs/reference/remotes/#codes) |
| `remotes/{namespace}/{name}/availability` | the `Remote`'s pod and its Last Will | the operator | yes | [`online` or `offline`](/docs/reference/remotes/#availability) |
| `remotes/{namespace}/{name}/focus` | the operator | the playback pod, the idle pod or a delegate's client, and the operator | yes | [the name of a `Player`, or empty](/docs/reference/remotes/#focus-and-focuscycle) |
| `remotes/{namespace}/{name}/focus/cycle` | the program that has the `Remote`'s focus | the operator | no | [empty](/docs/reference/remotes/#focus-and-focuscycle) |

The playback pod publishes two requests on the `players` commands
topic. It publishes `play-next` when a person accepts the up-next offer
on the scrubber, and the client that created the `Play` creates the next
`Play`. It publishes `home` when a person presses home during a film,
just before the `Play` ends, and the program that draws the idle
screen treats it as a press of the home key. Any other program can publish `home` there too. The
operator then asks the `Player`'s `Receiver` to show the `Player`, the same as
it does for a remote's home key.

In this table, "the playback pod" means its command sidecar, which is
the only container of the pod that connects to the bus. "The idle pod"
is the idle screen client that the media operator ships. A delegate's
client, under
[another controller](/docs/guides/handing-the-idle-screen-to-another-controller/),
reads and writes the same `players` and `remotes` topics from
`status.idle.bus`.

## What the operator reads

The operator subscribes to these topic filters.

| Filter | What the operator does with it |
|---|---|
| `plays/+/+/status` | copies each report from the run's current pod into the `Play`'s status |
| `plays/+/+/availability` | accepts a retained report only while the run's current pod is online |
| `remotes/+/+/focus` | after a restart, reads back the focus marks it published, and publishes only a mark that a restarted broker lost |
| `remotes/+/+/focus/cycle` | moves the mark to the next bound `Player` |
| `remotes/+/+/events` | sets the `Player`'s `spec.power` for a power key, and for any key on a `Player` that is off. Changes the volume for a volume key. For any other key, asks the focused `Player`'s `Receiver` to select the `Player`'s input when it is not selected, and to show the `Player` for a home key |
| `remotes/+/+/availability` | accepts the declared codes only while the `Remote`'s pod is online |
| `remotes/+/+/codes` | subtracts the declared codes from the key table and reports the result in `status.unbound` |
| `players/+/+/panel` | overrides the screen's `Display` to match the requested panel state |
| `players/+/+/volume` | reads back the levels it published, so after a restart it does not publish a level that the broker already has |
| `players/+/+/volume/commands` | changes the volume, the same as a volume key |
| `players/+/+/commands` | asks the `Player`'s `Receiver` to show the `Player` when a `home` request names no `Remote` |

Most key presses never pass through the operator. A key event goes from
the `Remote`'s pod directly to the playback pod or the idle client, and
only the program that has the `Remote`'s focus acts on it. So a
controller keeps working while the operator is down. The volume keys,
the power keys, and the input requests are the exceptions, because the
operator acts on them through the `Player`, the `Receiver`, or the
`Sink`. They do nothing while no operator replica is the leader.
