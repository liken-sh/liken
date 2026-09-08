---
title: The receiver on the bus
weight: 20
toc: true
---

# The receiver on the bus

The operator writes two topics on the media bus, and both belong to
the `media-operator`'s `players` tree. This page states what the
operator writes there and when. The
[media players page](https://media.liken.sh/docs/reference/players/)
gives the whole tree, and the
[media bus page](https://media.liken.sh/docs/reference/bus/) gives
the rules every topic follows.

The `media-operator` writes the volume topic into
`spec.session.volumeTopic` in full, base included, so this page
writes both topics in full.

| Topic | Payload | Retained | This operator |
|---|---|---|---|
| `liken/media/players/{namespace}/{name}/volume` | `{"level": 40, "muted": false}` | yes | reads every message, and writes the receiver's true level |
| `liken/media/players/{namespace}/{name}/volume/owner` | `{"owner": "receiver/{receiver}"}`, or empty | yes | writes the mark while a session stands |

## The owner mark

The mark says that equipment applies the level and the pods do not.
Its topic is the volume topic plus `/owner`, and its payload names
the `Receiver`:

    {"owner": "receiver/living-room"}

An empty payload clears the mark. An empty retained publish removes
the retained message from the broker, so a subscriber that connects
after the clear receives nothing on the topic.

The `media-operator` and each playback pod's command sidecar read
the mark. While it stands, every playback pod for the unit holds
`mpv` at unity and applies no level, and the `media-operator` starts
a playback pod with no level of its own. When the mark clears, the
pods apply the level the volume topic holds. The players page states
what each reader does with the mark.

The operator sets the mark when a session starts, on the session's
own broker connection. It publishes the mark again on every fresh
connection to the broker, because a broker that restarted holds no
retained state.

The operator clears the mark on two paths:

* A stop. The `media-operator` lifts `spec.session`, or writes a
  session that names another `Player`, input, or volume topic. The
  same stop runs when the `Receiver` is deleted, when
  `spec.denon.address` changes, and when the operator shuts down.
  The operator publishes the empty payload, waits 200 ms for it to
  reach the broker, and closes the connection.
* The MQTT Last Will. Each session's connection names the owner
  topic as its will, retained, with an empty payload. When the
  operator dies without a clean disconnect, the broker publishes
  the will, and the pods take the level back.

## The level

The volume topic is the `Player`'s. The `media-operator` and the
pods write it, and the payload is the `media-operator`'s: `level`
runs 0 to 100, and `muted` is a boolean. The players page gives the
payload's rules.

While a session stands, the operator subscribes to the volume topic
and reads each message as a direction. A level above the one it
holds moves the receiver up one `spec.volume.step`, a level below
moves it down one step, and `muted` is applied as the receiver's
own mute. The receiver moves from where it stands, in its own
scale, and never to the level the message carries.

The operator also writes the volume topic, retained. A pod writes
the topic only for a press it handled. While the mark stands the
receiver is the level's owner, so the operator writes the topic on
three occasions:

* When a session starts, the operator publishes the receiver's
  current position, mapped onto 0 to 100 against
  `spec.volume.max`. Until that message comes back from the broker,
  every message on the topic is history from an earlier session and
  moves nothing. The operator waits for the receiver to
  report its volume and for `spec.volume.max` to be set before it
  publishes this first message.
* When the receiver reports a volume or a mute, the operator
  publishes the new position. This covers the answer to a press and
  a hand on the receiver's own knob, so the retained level stays
  true and the next press steps from where the knob left it.
* When a press moves nothing, such as a press up at the ceiling,
  the operator publishes the receiver's position, so the topic does
  not drift from the equipment.

A position the operator already published is not published again.

So while a session stands, the topic holds the receiver's true
level in the bus scale. The same level in the receiver's own scale
is in `status.volume`.
