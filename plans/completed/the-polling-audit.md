# The polling audit

Completed on 2026-09-27. Measured on a nine-machine home cluster with
five API servers.

This audit read every repository in the organization against the rule
"Keep state current with events" in `AGENTS.md`, measured the API
server's load, and fixed the four causes that made most of it. The
request rate at rest went from about 549,000 requests an hour to about
227,000, a drop of 59%. The causes that the measurement found were not
the causes that reading the code ranked first.

## The rule that started it

The organization rule says that each component opens its watch or
subscription first, reads the whole state once, and opens the watch
and reads again when the watch fails. For Kubernetes, that is a list
followed by a watch from the list's `resourceVersion`. A timer is
correct only as a clock: a time-to-live, a deadline, or the age of a
certificate. A timer that reads state again to find a change is a
defect.

The rule came from a device. An operator scanned a CEC bus once a
minute, and a TV on that bus switched its input every few minutes. The
switching stopped when the operator stopped the scan. A timed read
also wakes a process when nothing changed, and each read adds load to
the API server.

## The ranking from reading the code

The first pass read the code of every repository and estimated the
requests each timed read makes in an hour at rest. These numbers are
estimates, not measurements.

| Rank | Component | Timed read | Estimate |
|---|---|---|---|
| 1 | `liken`'s machine-operator | A 10 s pass: gets of the `Machine`, `Node`, `Cluster`, and a `Secret`, a list of its own pod, two sysfs walks | 2,200/h per machine, about 19,000/h on nine machines |
| 2 | library-operator | A 10 s pass: about 13 cluster-wide lists, `Node` and `PersistentVolume` included, and a get per object | 11,000 to 12,000/h |
| 3 | media-operator | A 10 s pass: lists, cluster-wide `ResourceSlice` and `Receiver` reads, and gets per `Player` | 9,000/h |
| 4 | `liken`'s cluster-operator | A 10 s pass of about 10 reads, and a full pass on each `Machine` status write | 3,600/h or more |
| 5 | equipment-operator | A 10 s HTTPS poll of a streamer, about 10 requests each | Device traffic |
| 6 | display-operator | A 10 s DDC pass, up to 8 VCP reads per lit panel | Device traffic |
| 7 | git-csi-driver | A 30 s resync that ends the watches and lists every `PersistentVolume` again | Not estimated |
| 8 | bluetooth-operator | A list of `PairingRequest` objects every 5 s | Not estimated |
| 9 | `liken`'s inotify and uevent readers | No reopen and no full read after a failure; an `ENOBUFS` from the uevent socket was skipped | Correctness |

The same reading found many smaller timers, and three comments that
did not match their code.

## The measurement

The measurement read `apiserver_request_total` from each of the five
API servers twice, 605 seconds apart, and split the difference by
verb, resource, and flow schema. A client keeps its connection to one
API server, so a count that appears on one server only belongs to one
client. The per-server split found the single clients below.

At rest the cluster made about 549,000 requests an hour. Service
accounts made 87% of them. The kubelets made only about 19,000 an
hour. The operators averaged 1 to 13 millicores of CPU each, so CPU
was not a reason for any fix. The reasons were the load on the API
server and simpler code.

### The four measured causes

1. **A USB sound card's serial, shared by three machines.** Three
   machines had one model of USB sound card, and all three cards
   reported the same serial. audio-operator derived the cluster-scoped
   `Sink` name from the serial, so the three machines wrote one `Sink`
   and took turns setting their own machine in `status.node`, about
   once a second. Every audio-operator pod watched every `Sink` and
   `Source` in the cluster, so each write woke the operator on every
   machine. The result was about 80,000 `Sink` gets and 11,000 `Sink`
   and `Source` updates an hour.
2. **A wake storm in library-operator.** The lists of `Library` and
   of `MetadataProvider` objects each ran 3,892 times an hour, so the
   pass ran about once a second, not every 10 seconds. The progress
   store republishes a playing `Play`'s position every second, and
   each message woke a whole pass. Each pass read every claim and pod
   by name: about 78,000 `PersistentVolumeClaim` gets and 73,000 pod
   gets an hour in the first measurement, all from one client on one
   API server. The pass also
   wrote the `Library` about 8,000 times an hour, because its
   `status.runs` carried fields that the CRD schema drops, so the
   stored object never matched what the pass meant to write.
3. **A 30 s relist of every `PersistentVolume`.** Every node of
   git-csi-driver listed every `PersistentVolume` every 30 seconds and
   read each staged volume again: about 5,000 lists and 19,000 gets an
   hour. Each new watch opened with no `resourceVersion`, so a change
   between the list and the watch waited for the next resync.
4. **A 5 s list of pairing requests.** Every node of
   bluetooth-operator listed every `PairingRequest` every 5 seconds,
   about 6,400 lists an hour. The watch cache answers these lists, so
   they cost the API server little. A new request or an approval
   waited up to 5 seconds for the next list.

### What the measurement overturned

- The machine-operator and the cluster-operator, ranked first and
  fourth, made a small share of the requests.
- The display-operator's DDC pass reads no panel at rest. It reads a
  panel only when a claim sets a control.
- The audio-operator and display-operator watches that opened with no
  `resourceVersion` only wake a pass that reads everything, and a
  backstop covers them. An event that arrives between the read and
  the watch waits for the backstop, so they are not bugs.
- The uevent reader's skipped `ENOBUFS` was a small real bug: a
  receive that lost events must wake a full read.

## How each fix was chosen

A fix was made only when it had a clear gain. Before each change, the
finding stated what it cost now, which event source could replace the
timed read, and whether the code became simpler. A finding with no
clear gain was not changed. One agent worked in each repository, an
adversarial review read each change, and each repository released and
rolled out on its own.

The four causes had these fixes:

1. audio-operator starts each `Sink` and `Source` name with the
   machine. The CRDs declare `status.node` as a selectable field, so
   each machine lists and watches only its own objects, from the
   list's version.
2. library-operator wakes the pass only when a recorded mark appears,
   clears, or changes to ended. A report that repeats the stored mark
   wakes nothing. The pass lists its claims, volumes, and pods once,
   and `status.runs` has its own type that matches the schema.
3. git-csi-driver lists once, watches from the list's version with
   bookmarks, resumes a closed watch from the last version, and lists
   again only for a `410` or an error event.
4. bluetooth-operator lists once and watches `PairingRequest` objects
   from the list's version. A lost uevent now wakes a full pass.

`liken` 2026.09.27-001 makes the uevent reader wake a full read after
`ENOBUFS`. The work also replaced timers and corrected the wrong
comments that the first pass found.

## The three guards of a watch loop

The fixes replaced timed reads with watch loops written by hand, one
or more in each operator. Reviews of the first loops found the same
three faults in four repositories, and each loop needed several
review rounds. The organization rule now names a guard for each
fault. The `operators` skill in `.agents/skills` holds
them in full, with the scenarios that a watch loop must pass in a
test.

1. **Resume, do not list.** A closed watch opens again from the last
   version it delivered. Some loops listed again after every close. A
   watch with no `resourceVersion` first sends every
   object, so each open cost as much as a list.
2. **List again only for a `410`, and only once.** A loop that listed
   again after every error listed in a tight loop on an error that was
   not a `410`. A loop that listed again after every `410` made about
   9,000 lists in 2 seconds when the server answered `410` to a fresh
   list's version. So only the first `410` lists at once, and a second
   one waits out the backoff. An event that does not decode counts as
   an error, and the loop closes the stream at once. Two loops read to
   the end of the stream after such an event, and the server held the
   stream open for minutes, so every event in that time was lost.
3. **Decide the wait by the watch's life.** A server that accepted a
   watch and closed it at once made one loop open a new watch
   thousands of times a second. A watch that lives less than a second
   is now a failure, and the backoff applies. The life starts when the
   `200` arrives. Loops that timed the watch from the request counted
   a slow dial or a slow refusal as a watch that ran, reset the
   backoff, and retried every few seconds while the server was down.

## The result

The final measurement ran after every operator release in the audit
was on the cluster: equipment-operator 2026.09.27-004, audio-operator
2026.09.27-004, bluetooth-operator 2026.09.27-002, media-operator
2026.09.27-005, library-operator 2026.09.27-003, display-operator
2026.09.27-002, git-csi-driver 2026.09.27-002, and per-node-csi-driver
2026.09.27-002.

The total went from about 549,000 requests an hour to about 227,000,
a drop of 59%. The containers in `liken-system` used 285 millicores of
CPU and 1.8 GiB of memory in total.

The per-item numbers come from a comparison taken earlier the same
day, after the first releases of audio-operator, git-csi-driver,
per-node-csi-driver, library-operator, and equipment-operator. The
total at that point was about 266,000 an hour. The comparison's
before column for claim and pod gets is lower than the first
measurement's, and the notes of the audit do not record why.

| Item | Before | After |
|---|---|---|
| `Sink` gets | 80,000/h | 2,200/h |
| `Sink` and `Source` updates | 11,000/h | 0 |
| `PersistentVolumeClaim` gets | 62,000/h | 6,900/h |
| Pod gets | 56,000/h | 5,600/h |
| `Library` updates | 8,000/h | 30/h |
| library-operator passes | 65/min | 15/min |

The git-csi-driver `PersistentVolume` counts and the
bluetooth-operator `PairingRequest` lists after their fixes were not
recorded per item. They are part of the final total.

## What stays open

- **Leases.** `Lease` traffic is about 64,000 requests an hour, the
  largest remaining item. It is the kubelets' heartbeats and the
  leader elections, and the audit made no change to it.
- **A second library wake source.** After the fix, library-operator
  still ran about 15 passes a minute at rest, where a 10 s tick gives
  6. A change for one more wake source was set aside, and the source
  of the other passes is not known.
- **One `ConfigMap` watch.** Some client opens a watch on the
  `ConfigMap` objects of one namespace about twice a second, about
  8,200 requests an hour. The client is not identified.
- **One shared watch loop.** Each operator still writes its own watch
  loop. The open plan in the `operators` skill names two answers, one
  shared Go module with a conformance test, or client-go's reflector
  in each operator. Neither is chosen, and the size and memory cost of
  client-go in one operator is not measured.
