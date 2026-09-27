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

Open problem. Eight repositories each hold one hand-written watch loop:
about 2,470 lines in all (1,530 of code) and 4,900 lines of tests for
the loop alone. The loops drift apart, and each fix lands in one
repository at a time. `per-node-csi-driver` already uses a client-go
informer, and `git-csi-driver` already links client-go's typed
clientset under its hand-written loop. Two answers are on the table,
and neither is chosen.

**Measured cost of client-go.** On 2026-09-27, the `PairingRequest`
watch in `bluetooth-operator` was ported to client-go in a throwaway
copy. Each build watched 50, then 250, `PairingRequest` objects on a
k3s v1.36.3 API server in Docker, with client-go v0.36.3. The
operator cannot run outside its pod, so each binary ran only its
watch. The binary still linked every package, so every package
initialized.

| | Hand-written | `cache.NewSharedIndexInformer` and `dynamic` | `dynamicinformer` |
|---|---|---|---|
| Stripped binary (the image is `FROM scratch`, so the same) | 14.9 MB | 20.0 MB | 39.1 MB |
| Linked Go packages | 354 | 474 | 807 |
| RSS, idle, 50 objects | 14.8 MB | 22.1 MB | 31.0 MB |
| RSS, idle, 250 objects | 16.6 MB | 24.0 MB | 32.8 MB |
| Cold build | 11.0 s | 14.3 s | 33.0 s |

`dynamicinformer` links the typed clientset and the informers for
every built-in kind. The middle column links only `tools/cache` and
`dynamic`, and it decodes each object into the operator's own struct
with `runtime.DefaultUnstructuredConverter`, so it needs no code
generation. `go mod graph` did not change (729 to 730 lines), because
the CLI already requires client-go. In the port, 270 lines of loop
code and 439 lines of loop tests were removed, and 55 lines were
added. The port added no tests.

- **client-go's reflector and informer.** This is the Kubernetes-native
  answer. Upstream maintains and tests the guards, and it adds
  streaming lists (`WatchListClient`, on by default in v0.36). It
  fixes the drift, because no loop code stays in the repositories.
  It costs about 5 MB of image and 6 to 7 MB of RSS for each process.
  A two-node test cluster ran seven and four operator pods on its
  nodes that do not link client-go now, so the cost is about 25 to
  50 MB on each node, which matters on a 1 GB machine. Maintenance is
  a client-go version bump with each Kubernetes pin, and a handler
  for each watched collection (about 45 lines in the port).
  The reflector does not meet the guards above exactly:
  - After a `410`, it waits a backoff of 0.8 to 1.6 seconds before it
    lists.
  - A watch that closes in under a second with no event makes it list
    again after the backoff. It does not resume.
  - A watch that delivered an event is never short.
  - It measures a watch's life from the request, not from the `200`.
  - Its backoff resets after two minutes with no failure, not after
    one watch that ran a second.

  Each handler must also report an object that does not convert to
  the operator's struct. The port drops such an object with no error.
- **One shared Go module**, `github.com/liken-sh/watch`. It holds one
  loop and a conformance test built from the scenarios above. The
  scripted fake API server from the 2026-09 reviews (524 lines) is the
  start of that test. The module removes about 2,300 lines from the
  eight repositories, and it costs no memory. It fixes the drift and
  keeps the guards exactly as written above. The project then
  maintains the loop, the fake server, and a release that every
  operator must take. Each new API server behavior, such as streaming
  lists, is work for the project.

**Recommendation:** client-go, in the form of the middle column.
Accept the reflector's differences from the guards; none of them
loses an event. An operator that does not link client-go now imports
only `k8s.io/client-go/tools/cache` and `k8s.io/client-go/dynamic`,
never `dynamicinformer`, `informers`, or `kubernetes`. Move one
operator first, and measure its pod on a 1 GB machine before the
others move. Choose the shared module if that memory is too much for
the 1 GB machines. Until the choice, test every hand-written loop
against the scenarios above.
