# 39, Every watch wakes the pass

Built on 2026-09-27. The watches that plan 38 added only to fill the
view now wake the pass when a change reaches a field the pass reads:
the claims, the `ResourceSlice`s, the `Display`s, the `Receiver`s, the
idle and reader pods, and a playback pod whose status changes. The
10-second tick is a clock and nothing else. The same change bounds every request of the operator's own API
client at 30 seconds, and raises the command sidecar's memory request
from 12Mi to 16Mi. No drill has run on `liken-1` yet.

## The problem

Plan 38 moved the pass's reads into memory and kept the wake rules the
hand-written loop had. Four watches and two kinds of pod woke nothing,
and a playback pod woke the pass only when it went away or failed, so
many changes reached the pass only on the next 10-second tick: a claim
the scheduler allocated, a monitor that went dark, a receiver that
changed input, a standing pod that went away, and a playback pod that
started running, succeeded, or restarted its display. The
organization's rule counts a timer that reads state to find a change as
a defect, and the watches already delivered each change.

The API client set no deadline on a whole request, so a body that
stopped part way could hold a pass. Before plan 38 the watches used the
same client and a deadline would have cut their streams. client-go runs
the watches now.

Plan 38 measured the pod build about 0.5 MB larger in anonymous memory
at start, and the command sidecar measured 11Mi at the 90th percentile
against a 12Mi request.

## The design

### The wake rules

`changewake.go` holds one rule for each of the four kinds. A rule names
the fields of an object that the pass reads and another writer
changes, as a mark. The informer hands an update both the copy it held
and the new copy, so the handler compares their marks and keeps no
copy of its own. Each mark holds the UID and the deletion mark, so an
object deleted and created again during a gap in the watch, and a
deletion request, both wake the pass.

| Kind | Wakes the pass | Wakes nothing |
|---|---|---|
| `ResourceClaim` | a claim a `Play`, a `Player`, or a `Remote` owns that is allocated, reallocated, released, deleted, or read already allocated | this operator's create of an unallocated claim; a change to `reservedFor`; every claim another program owns |
| `ResourceSlice` | a slice published, changed (a new `metadata.generation`), or withdrawn | a change to the slice's labels or annotations |
| `Display` | a new status from the display-operator; a `Display` published or withdrawn | this operator's `spec.override` apply |
| `Receiver` | new inputs or a new commands topic in the spec; new power, input, or conditions in the status; a `Receiver` published or withdrawn | this operator's session apply in the status and its release in the spec |

The pods watch wakes the pass when any of this operator's pods goes
away, and when a playback pod's status changes. The pass derives a
`Play`'s phase from its pod's status: `Pending` with the scheduler's
message, `Running`, `Succeeded`, `Failed`, and a display container that
keeps restarting. The operator's `Pod` struct holds only the status
fields the pass reads, so a probe time that moves wakes nothing, and
the mark leaves out the labels, which carry this operator's own ending
label. A new playback pod wakes the pass only when it has already
started, which is how the informer reports a pod that ran, finished,
or failed during a gap in the watch. The API server gives every pod it
creates the `Pending` phase, so this operator's own create wakes
nothing. An idle pod and a reader pod restart their containers
in place, and the pass reads nothing of their status, so only their
removal leaves the pass something to do. A standing pod the pass deletes
because its template changed now has its replacement created on the
pass that its removal wakes, not on the next tick.

`finishReplacement` read a deleting claim again after a one-second
timer. The claim's removal wakes the pass now, so the timer is gone.

### The tick

The tick is a clock. It ends three waits that no event ends: a position
that only advanced reaches the `Play`'s status
(`positionWriteInterval`), a Finished `Play` goes when its window
passes, and the first level or focus mark of a new broker session goes
out when `catchUpGrace` ends. The constant is `tickInterval` now, and
its comment says the tick covers no change the pass would miss.

Each of the three waits has a deadline the pass already computes, so
each could schedule its own wake with `requeueAfter`, as the recreate
backoff does: at the last position write plus `positionWriteInterval`
for a run whose advance the throttle held, at `finishedAt` plus the
window for a Finished `Play`, and at `catchUpEnds`. The tick would then
have no work, and a cluster with nothing playing would run no pass at
rest. This plan does not change the clocks. The tick costs no request
on a settled cluster, and the position throttle is tuned against the
tick's period.

### The request deadline

`apiRequestTimeout`, 30 seconds, bounds each request of the operator's
own client from the dial to the last byte of the body, as in
`equipment-operator`. Every request that client sends is one read or
one write. A pass that waits on a request waits at most this long, and
the next wake or tick tries the request again.

### The command sidecar's request

The command sidecar measured 11Mi of working set at the 90th percentile
and 13Mi at most, on a home cluster with the pod build before plan 38.
That build starts about 4 MB larger in `VmRSS` and 0.5 MB larger in
anonymous memory. The request is 16Mi: the 90th percentile with that
growth and some room. The limit stays at 32Mi, twice the request and
more than twice the highest use measured. A playback pod and the idle
pod together request about 688Mi now, against 684Mi before, and still
schedule on a 1GB machine. Plan 36's table and guides show the new
value.

## Tests

| What | Test |
|---|---|
| Each claim change wakes or not, by the table above | `TestAClaimWakesThePassWhenItsAllocationChanges`, `TestAClaimRemovalWakesThePassOnlyForTheOperatorsClaims` |
| Each slice change wakes or not | `TestASliceWakesThePassWhenItsSpecChanges` |
| Each `Display` change wakes or not | `TestADisplayWakesThePassWhenItsStatusChanges` |
| Through the reflector: the override does not wake the pass, and a panel that leaves does | `TestADisplayWakesThePassThroughTheReflector` |
| Each `Receiver` change wakes or not | `TestAReceiverWakesThePassWhenTheEquipmentOrItsWiringChanges` |
| A playback pod's status change wakes the pass, and the ending label and an idle pod's status do not | `TestAPodWakesThePassWhenItEndsOrAPlaybackPodMoves` |
| Every pod removal wakes the pass | `TestAPodRemovalWakesThePass` |
| A request whose body stops part way ends at the deadline | `TestTheInClusterClientBoundsAWholeRequest` |
| A replacement waits for the old claim, and the next pass creates the pod | `TestAReplacementWaitsForTheOldClaim` |

No test runs a replacement through the claim's removal event end to
end: the claim rule's tests prove that the removal wakes the pass, and
the replacement test proves what the pass that follows does.

## Considered and set aside

* **Waking on every change to the four kinds.** A claim's
  `reservedFor`, a `Display`'s override, and a `Receiver`'s session
  change on this operator's own writes, and the pass reads none of
  them to decide anything, so each would run a pass for nothing.
* **Watching only the slices of the display driver.** The pass reads
  the slices that hold its idle claims' draw devices, and the claim
  names the driver. Drivers write a slice only when its devices
  change, so the wakes from other drivers are few.

## The drill still owed

On `liken-1`, with the operator on a build of this change:

1. Unplug a unit's monitor, and time how long its `Player`'s `Screen`
   condition takes to read the change. It must be well under the
   10-second tick.
2. Delete a Remote's reader pod, and check that the replacement is
   created within about a second, with one line.
3. Start a `Play`, and time how long its phase takes to read
   `Running` after the pod runs. It must be well under the tick.
4. Read the command sidecar's working set on a playback pod, and
   compare it with its 16Mi request.
