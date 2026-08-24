# 15, Finished plays clean up

A `Finished` Play lives forever, and so does its `Completed` pod.
Nothing in the operator deletes either one, so the pod list and the
play list accumulate one entry per film until a hand clears them.
The pod holds nothing of value once the film is over: the final
position is on the Play's status, and the ending already traveled
the bus. The Play holds a little more, the record of what played and
where it stopped, but not forever.

This plan gives both a lifetime. The pod goes on the pass that sees
the terminal phase, and the Play goes when its time-to-live after
finishing passes, a field on the spec with a five-minute default.

## The pod goes at once

On the pass that folds a Play to `Finished`, the operator deletes
the playback pod and its claims, with the delete verbs it already
holds. The Play stays, carrying its final status. Only `Finished`
pods are deleted: a `Failed` pod stands, because `Failed` is the
phase a person debugs, its log is the evidence, and a `Failed` Play
resumes rather than ends.

## The Play follows, on its spec's clock

The Play spec gains `ttlSecondsAfterFinished`, the name and the
meaning a `Job` gives it: how long a `Finished` Play stands before
the operator deletes it. The operator defaults it to 300 seconds
when the spec does not set it. Zero deletes the Play on the pass
that sees it finished, and a long value keeps the record around.

The field is spec, not operator configuration, because the window is
the Play's own affair: a library app that creates Plays sets it to
the window its continue-watching feature wants, and two apps on one
cluster choose differently.

Deleting the Play is already the whole teardown: the owner
references collect anything that remains, and the retained-topic
reclaim clears the bus. The status gains `finishedAt`, stamped on
the pass that first sees the terminal phase, so the TTL counts from
when the film ended and not from when the Play was created, and a
restarted operator reads the clock from the status instead of
remembering it.

## What the TTL window means

While a `Finished` Play stands, `kubectl get plays` still answers
what just played and where it stopped, and the final position is
still readable. When the Play goes, that record goes with it. So the
TTL is also the outer bound on any future resume-where-you-stopped
feature: a watch history that outlives the Play must be saved
somewhere else before the deletion, and no such feature exists
today. That is stated here so the next design reads it.

## Considered and set aside

* **An operator-wide TTL instead of a spec field.** One knob would
  serve one household taste, but the window belongs to whoever
  created the Play, and different library apps want different
  windows on one cluster. The operator supplies only the default.
* **Deleting `Failed` pods too.** A `Failed` pod's log is the
  evidence a person debugs from, and the Play resumes. Cleanup that
  eats evidence saves nothing worth the loss.
* **Keeping the pod for its logs until the Play goes.** The
  `Finished` pod's log is a film that played to its end, and the
  ending already reached the status and the bus. Five more minutes
  of a dead pod buys nothing.

## Proof

On `liken-1`, with the sailing demo:

* Let the film end. The playback pod is gone within a pass, and the
  Play still shows `Finished` with its position.
* Five minutes later, the Play is gone, and its retained topics with
  it.
* Create a Play with `ttlSecondsAfterFinished: 0` and let it end.
  The Play and the pod go together.
