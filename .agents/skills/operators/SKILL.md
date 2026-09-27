---
name: operators
description: How the liken-sh operators keep their view of the cluster and their devices current. Covers the three guards every Kubernetes watch loop needs, the scenarios a watch loop must pass in a test, status writes, timers, device traffic, and the open plan for one shared watch loop. Use when writing or reviewing an operator's watch, reconcile pass, backstop, timer, or device polling.
---

# Writing and reviewing operators

The organization's rule is in `AGENTS.md`, "Keep state current with
events": open the watch or subscription first, read the whole state
once, and open it again and read again when it fails. A timer is
correct only as a clock. This skill carries the detail that the rule
needs in an operator.

Every operator in the organization writes its own Kubernetes watch
loop. In 2026-09, reviews of seven of these loops found the same
faults again and again, and each loop needed several rounds to meet
the guards below. Read this skill before you write or change a watch
loop, and test the loop against every scenario in it.

## The three guards of a watch loop

1. **Resume, do not list.** List first, then watch from the list's
   `resourceVersion`, with `allowWatchBookmarks=true` so that the
   version moves while nothing changes. When a watch closes, open the
   next one from the last `resourceVersion` it delivered. Do not list
   again. A watch with no `resourceVersion` replays every object first,
   so each such open costs as much as a list.
2. **List again only for a 410, and only once.** A `410 Gone`, as the
   HTTP response or as an `ERROR` event, lists again at once. If the
   watch that opens from that fresh list also gets a 410, wait out the
   backoff. A 410 that ends a watch which ran a second or longer
   counts as a first 410. After any other `ERROR` event, and after an
   event whose object or line does not decode, wait out the backoff,
   then list. On any error, close the stream at once: the server holds
   a watch open for minutes, and a loop that reads the stream to its
   end loses every event in that time.
3. **Decide the wait by the watch's life.** A watch that closes less
   than a second after the server accepted it is a failure, whatever
   it delivered, so the backoff applies and grows to its cap. A watch
   that ran a second or longer resets the backoff, even when it ended
   with an error. Measure the life from the moment the `200` arrived,
   not from the request: a slow dial, a slow refusal, and a slow `410`
   answer never ran.

## Scenarios every watch loop must pass

Test a loop against a scripted fake API server that can hold a stream
open, answer slowly, and cut a connection. Each scenario names the
correct result.

| Scenario | Correct result |
|---|---|
| A watch runs for minutes and closes cleanly | Reopen at once from the last version, with no list |
| A watch closes in under a second | Wait out the backoff, then reopen from the last version |
| A `410` response, then a watch that works | One list at once |
| A `410` on every version | Lists bounded by the backoff |
| A `410` after a watch that ran a second | One list at once |
| A non-410 `ERROR` event, stream held open | Close at once, wait, list |
| An object or a line that does not decode, stream held open | Close at once, wait, list |
| A connection reset after a watch that ran | Resume from the last version, with no list |
| A `403`, then the permission is granted | Back off while refused, then watch normally |
| A dead server (the dial fails) | The backoff grows to its cap |
| A slow `200`, then a normal stream for minutes | Every event delivered |
| A slow refusal or a slow `410` | The backoff still grows |

## Status, echoes, and the pass

- **Write status only on a change.** A status field that changes on
  every pass, such as a timestamp or a field the CRD schema drops,
  makes every pass write, and the write wakes every watcher. Compare
  against what the API server stores, not against what the code
  meant to write.
- **Scope each watch to what the process owns.** A DaemonSet pod that
  watches every object of a kind wakes on every other node's writes.
  A CRD can declare `selectableFields`, so each pod lists and watches
  by field selector, for example `status.node=<machine>`.
- **A field-selected list needs the `list` verb.** RBAC authorizes a
  list or a watch against `resourceNames` only when the request
  selects that one name, and the Role must grant `list` as well as
  `watch`.
- **A CRD change reinitializes its storage.** During a rollout that
  changes a CRD, the API server answers `429 storage is
  (re)initializing` for a short time. A loop must back off and retry.
- **Read once per pass, not once per object.** A pass that GETs each
  claim or pod it stands costs one request per object on every pass.
  One list per pass answers the same questions.

## Timers and devices

- **Name what a kept timer covers.** A backstop tick needs a comment
  at the timer that names each failure it covers and why no event
  covers it. A timer that is a clock (a deadline, a TTL, a backoff, a
  heartbeat) needs only a correct comment.
- **Traffic to a device is a cost even when it is cheap.** A TV
  switched its input while an operator scanned its CEC bus each
  minute. Ask a device only when a person's action or an announcement
  needs the answer, bound each question to one per event, and share
  one read in flight between every caller that needs the same fact.
- **A lost event wakes a full read.** A uevent socket that answers
  `ENOBUFS` dropped events. Wake the pass at once instead of waiting
  for the next event or the backstop.
- **Signal a child through its process handle.** A signal to a bare
  pid can reach a later process that reused it. `os.Process` holds a
  pidfd, and a signal after the process was reaped returns
  `ErrProcessDone`.

## Open plan: one watch loop for every operator

Open problem. The seven hand-written loops drift apart, and each fix
lands in one repository at a time. Two answers are on the table, and
neither is chosen:

- **One shared Go module**, such as `github.com/liken-sh/watch`, that
  holds one loop and a conformance test built from the scenarios
  above. Every operator imports it, and a fix lands once.
- **client-go's reflector and informers** in each operator. They are
  the Kubernetes-native answer and already hold these guards. Most
  operators avoid client-go to keep their binaries small, and that
  cost is not measured. `git-csi-driver` and `per-node-csi-driver`
  already use client-go.

Measure the binary size and memory cost of client-go in one operator
before you choose. Until then, test every hand-written loop against
the scenarios above.
