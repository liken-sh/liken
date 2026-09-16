# 33, The run reports its display

Built on 2026-09-16, and drilled on `liken-1` the same day with three
kills of the display sidecar.
A `Play` reports the liveness of its display sidecar as
a `DisplayAlive` condition, `kubectl get plays` shows the condition's
reason in a `Display` column, the unit's bus status includes the
condition, and `media_display_restarts_total` counts the restarts. A
display that restarts more than twice in one run ends the run the way
a finished film ends, so the unit returns to its idle screen. This
plan closes the open problem "nothing reports a crashed display".

## The problem

The display is a native sidecar in the playback pod, so when it
crashes the kubelet restarts it alone and the pod stays `Running`. The
`Play`'s phase follows the pod's phase, so it stays `Running` too. The
bus reports nothing either: the command sidecar reports mpv's own
properties and holds nothing about the display's container.

A person sees a film that plays with no on-screen display, while
`kubectl get plays` and every bus topic report a healthy run. Nothing
in the cluster names the fault. The only record is the restart count
on one container of one pod, and the only repair is to delete the pod
by hand.

The operator already watches playback pods, so a container status with
a restart count or a terminated state is one read away from a
condition on the `Play`. The open question was whether a crashed
display is a `Play`-level fault at all, because the film keeps playing
without it. This plan answers yes: a film with no scrubber, no
choosers, and no volume indicator is a fault the viewer sees, so the
run reports it, and after two restarts the run ends.

## The design

**The condition.** `DisplayAlive` on a `Play`:

* `True`, reason `Running`: the display container runs, whatever its
  `restartCount`.
* `False`, reason `Restarting`: the container is not running, after an
  exit the kubelet recorded.
* the message, on either status: the restart count with the last
  termination's reason and its exit code, and no message at a count
  of 0.
* absent: no display container status, or a container that has neither
  started nor ended.
* `lastTransitionTime`: stamped only where the status changes.
* `observedGeneration`: the `Play`'s `metadata.generation`.

The condition reads the container's present state on every pass and
never latches. A display that crashed once and came back reads `True`
again, with the restart count in its message, because the fact a
viewer acts on is whether the display draws right now. The count is
the history, and the message states it. A condition that stayed
`False` after one crash would report a fault the kubelet had already
repaired, and the operator would need a rule of its own for when to
clear it. The self-heal reads the count, not the condition, so a run
can end while the condition reads `True`.

The count comes from `status.initContainerStatuses` because the
display is a native sidecar, an init container with `restartPolicy:
Always`, and the kubelet reports every native sidecar there. The
kubelet is the authority for whether the container runs and for every
exit it restarted the container from, so the operator reads its
`restartCount` and its `lastState` and keeps no watch of its own.

The condition belongs on the `Play` because the display belongs to the
run: it starts with the pod, ends with the pod, and a `Player` outlives
both. A condition on the `Player` would describe the unit's last run
and go stale when that run retired. On the `Play` the condition stands
beside the phase that stays `Running` through the crash, which is the
report the phase alone cannot give.

**The column.** `kubectl get plays` carries `Display`, from
`.status.conditions[?(@.type=="DisplayAlive")].reason`.

**The metric.** `media_display_restarts_total{player}`, from the same
container status, at 0 from the run's first pod, and deleted with the
run.

The series exists at 0 from the pass that first reads the run's pod,
so a scrape tells a healthy run from a unit with no run. A series that
appeared on the first restart would be absent in both cases, and a
dashboard could not tell them apart. The run's end deletes the series,
the way the other per-run series go, because the label names the unit
and a retired run's count would otherwise be reported against the next
run on the same unit.

The operator remembers the count it last read for each run, in a map
keyed by the run and dropped when the run goes, and adds only the
growth to the counter. A Prometheus counter never falls, and the
kubelet's count is a gauge: a recreated pod starts at zero, and a
`Player` edit recreates a running pod. Adding the growth keeps the
total from falling across those resets, and a count that fell adds
nothing.

**The bus.** `players/{namespace}/{name}/status`, in the `play` block:
`displayAlive` with `status`, `reason`, and `message`.

**The self-heal.** More than 2 restarts in one run: phase `Finished`,
message from the condition, then the retire that a finished film takes.

The kubelet's restart of the sidecar alone is the cheapest repair
there is: mpv keeps playing, the position holds, and the display is
back within seconds.
The first two restarts are that repair, and the pod stays through
them: the phase stays `Running`, and the condition reports each
restart in its count. A third in one run is a
display that does not stay running, and a film that plays with no
display is the case a person deletes the pod by hand for. So the
operator ends the run itself: the phase moves to `Finished` with the
condition's message, the retire that plan 15 built deletes the pod and
the claim, the unit reads `Idle`, and the browser or the idle screen
returns. The run ends as `Finished` because a `Failed` pod stands for
its log and holds the unit, and because the film's position stays on
the `Play`'s status for the next `Play` to resume from.

## What was set aside

**The ending label and the fade.** Plan 30's ending label and the
sidecar's 500 ms hold both key on the sidecar's ending report, and a
display crash never produces one. A run that ends on its display
takes the retire path alone, so the compositor removes the film's
surface when the pod goes, with no fade. A fade needs the operator to
ask the sidecar to end the film, a command on the bus this plan does
not add.

**The command sidecar's own topic.** The bus already has a topic per
run, `plays/{namespace}/{name}/status`, and the crash could travel
there. The sidecar owns that topic and reports mpv's properties, and
it has no view of the display's container, so the mark would have to
come from the operator on a topic another process writes. The
`Player`'s status topic is the operator's own, and it is the topic a
delegate's client already reads, so the operator publishes the
condition there, in the `play` block.

**A condition on the `Player`.** Set aside for the reason above: the
display belongs to the run, and a `Player` condition would name a run
that has already retired.

## How it was proved

The unit tests in `display_test.go` cover the derivation: every branch
of the condition, the stamp that moves only on a change, the counter
across a recreated pod, the run that ends on its third restart, and
the bus status with and without the condition.

The drill ran on `liken-1` on 2026-09-16 with a film playing. The
display sidecar's process was killed three times from the host PID
namespace, through `kubectl debug node/<node> --profile=sysadmin`, and
the table below is what the cluster reported after each kill.

The kill comes from the host PID namespace because `kubectl debug
--target=display` cannot deliver it. That debug container shares the
display container's PID namespace, where the display's process is PID
1, and the kernel drops a signal sent to PID 1 of a namespace from
inside that namespace when the process has no handler for it. From
the host's namespace the same process has an ordinary PID, and the
signal reaches it.

| Measurement | Time after the kill |
| --- | --- |
| first restart | 1 s |
| second restart | 11 s |
| third restart | 22 s |
| condition `False`, after the second kill | 3 s |
| `Play` `Finished`, after the third kill | 32 s |
| pod gone, after the third kill | 38 s |

The three restart times are the kubelet's backoff. It restarts a
container at once on its first exit, waits 10 s before the second
restart, and 20 s before the third, and it doubles the wait on each
exit up to 5 minutes. The 1 s, 11 s, and 22 s in the table are those
waits plus the time the kubelet took to record each exit.

The `Display` column, `media_display_restarts_total`, and the
`displayAlive` block on the unit's status topic each reported the
state after every kill, and the ending ran as designed. Playback never
stalled through the three restarts. The browser returned to its page
when the pod went, with no restart of its own.
