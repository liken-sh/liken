# 04, The scheduler

Proposed on 2026-10-12, and shaped again on 2026-10-15. The horizon
filter is built (2026-10-13): a runner does not claim a target below
the telescope's `Horizon`, or below 0° when the telescope names none,
releases a target that sets before its next frame, and waits for the
first rise on a timer. The sky's geometry is a port of Meeus in
`sky/`, with the rise found by a search at one-minute steps. The rest
of this plan is not built. It records the decisions of the design
conversation of 2026-10-15, which replaced the first draft's score.

## The problem

Inside a reservation, the operator has to choose what to image next,
or that nothing can be imaged. The right answer changes through the
night: targets rise and set, the moon rises and moves, twilight ends
and begins, and part of the sky is behind trees. A plan made at dusk
breaks the first time a cloud passes or a solve fails.

Today the runner claims the eligible target with the highest priority,
then the oldest, and images it until it has no work left or it sets.
It chooses again only then, so a target that sets at 22:00 can wait
behind a target that is up all night.

A change of target also costs time that is easy to miss: the mount
slews, the operator centers with a plate solve, PHD2 finds its star
and settles, and the filter wheel turns. Autofocus and a rotator will
add more. A scheduler that ignores that cost switches back and forth
and loses the night to the switches.

## What others do

- **N.I.N.A.'s Target Scheduler** ([planning
  engine](https://tcpalmer.github.io/nina-scheduler/concepts/planning-engine.html),
  read on 2026-10-15) plans one step at a time. It filters targets by
  visibility, moon avoidance, and twilight, and scores the rest with
  weighted rules: Target Switch Penalty (67% by default), Meridian
  Window Priority (75%), Percent Complete, Priority, and Setting
  Soonest (50% each). Its switch penalty is a flat bonus for the
  target that ran last, not an estimate of what a switch costs. It is
  a C# plugin of N.I.N.A. (MPL-2.0), with no library or command to
  call.
- **The Ekos Scheduler** is part of KStars, in C++, tied to the Ekos
  interface.
- **astroplan** ([scheduling
  tutorial](https://astroplan.readthedocs.io/en/stable/tutorials/scheduling.html)),
  the Astropy project's planning library in Python (BSD-3, last
  release v0.10 on 2024-04-05), has constraints for altitude, airmass,
  night, and time windows, a `PriorityScheduler`, and a `Transitioner`
  that costs a switch from a slew rate in degrees per second and a
  time for each change of filter.

None of them can be called from the operator without a second runtime
in the image and a process boundary after every frame. So the
operator owns its scheduler, a few hundred lines of Go on `sky/`, and
takes the best ideas of all three. astroplan's values, such as when a
target sets at a site, are the reference that the tests check
`sky/` against.

## Decisions

### When it chooses

- **The runner chooses after every frame.** A cloud or a failed solve
  costs one frame, and a better target can take over in the middle of
  a target's work.
- **The target in the camera stays unless another beats it by more
  than the switch costs.** A target that is 1% better never causes a
  slew that costs four minutes. This is the same rule that keeps a
  thermostat from switching back and forth. The cost depends on the
  pair: the next filter of the same target costs a filter change, and
  a new target costs a slew, a centering, and a guide settle.
- **The cost of a switch is spread over the frames that follow it.**
  A switch to a target that holds the telescope for two hours costs
  its four minutes once, not once for each frame.
- **A choice also runs on events**: an exposure ends, a condition of
  the observatory changes, or PHD2 loses its star. Rise, set, and
  twilight are clock times, so a timer for each is correct.

### What can be imaged now

The filters remove what cannot be imaged now:

- **The horizon** of the telescope, from `observatory-operator`'s
  `Horizon` (built).
- **Darkness**: the sun's altitude, from the twilight that a sequence
  accepts.
- **The moon**: its distance from the target and its phase.

A narrowband filter, such as Hα, OIII, or SII, passes a band a few
nanometers wide, so it images near a bright moon and late into
twilight, and a luminance or RGB filter does not. So the darkness and
moon limits apply to each sequence, not to the whole target: on a
night with a moon, M42 can be eligible for Hα and not for L. A filter
change is cheap and a slew is expensive, so staying on M42 and moving
from L to Hα when the moon rises usually wins.

The sun's position is Meeus's chapter 25 and the moon's is chapter 47,
added to `sky/` and tested against the book's worked examples. Neither
needs a file of ephemerides. The accuracy of the moon's truncated
series was not checked; a moon filter needs about a degree.

### The order

1. **Priority first.** A target of higher priority goes first
   whenever it can be imaged. A person can predict this, and a weighted
   blend of priority and urgency is hard to predict: a user of N.I.N.A.'s
   weights on
   [AstroBin](https://app.astrobin.com/forum/topic/251233/acquisition/managing-multiple-active-projects-with-target-scheduler-for-optimal-conditions)
   reports learning to tune them so that it would not stay on one
   object all night.
2. **Least slack first among equal priorities.** A target's slack is
   the dark hours it has left this season above the telescope's
   horizon, less the work it has left. A target of 30 minutes has slack
   to spare and fits wherever there is room. A target of 20 hours late
   in its season has almost none and goes to the front. The person
   steers this through the work that the target's sequences declare,
   and through its priority.
3. **When no target is urgent, the most work for its season first.**
   Least slack first alone waits on a large target until late in its
   season, and a week of clouds then loses it. So among targets that
   all have slack, the one with the most work left for the hours its
   season has left starts first.
4. **The switching cost decides near ties**, as above.

A target's season is the run of nights ahead in which it stands above
the horizon in the dark. The operator computes it from `sky/` once a
night, not after each frame, and keeps it in the target's status.

The worked night of the design conversation, at 21:00 with the
telescope on M81:

| Target | Window left tonight | Work left | Slack tonight |
|---|---|---|---|
| M81 | 8.5 h, until dawn | 2 h | 6.5 h |
| NGC 253 | 1 h | 30 min | 30 min |
| M42 | 2 h | 3 h | none: it cannot finish tonight |

NGC 253 goes first, from 21:00 to 21:30, because it has a deadline
that it can still meet. M42 follows until it sets at 23:00, and M81
takes the rest of the night.

### The cost of a switch

- **A switch costs the slew's distance over the mount's slew rate,
  plus a time for each step it needs**: a filter change, a centering,
  a guide settle, and later an autofocus and a rotation. This is
  astroplan's `Transitioner`, with the steps that the operator runs.
- **Each part starts from a default that a person can set, and then
  follows what the operator measures.** After each switch, the
  operator moves the estimate of each step toward the time it measured,
  as a running average. The estimate is one number for each step on
  each telescope, kept in status. No history goes into the control
  plane, and nothing reads a metric back. The metrics keep the history
  for graphs. The Vertical Pod Autoscaler keeps its recommendation the
  same way: one current value in its object's status.

### Observing the scheduler

- **A waiting target says why in its `Imaging` condition**, as
  `BelowHorizon` does today, with new reasons such as `NotDark`,
  `MoonTooClose`, `LowerPriority`, and `LessUrgent`. The message names
  the numbers that decided it:
  `The moon is 18° from M42, and filter L needs 40°. Ha can be imaged now, after NGC 253 (less slack).`
- **With several telescopes**, the reason names the closest the target
  came to being chosen, and the message gives each telescope's reason,
  the way kube-scheduler writes a pending pod's `PodScheduled`
  message: `0/3 nodes are available: 1 Insufficient cpu, 2 node(s) had untolerated taint`.
- **Each switch posts one `Event`** on the target that the telescope
  moves to, with the comparison that decided it:
  `Switched from M81 to NGC 253: slack 0.4 h against 6.5 h, priority equal, switch cost 4 min.`
  One `Event` for each decision, never one for each pass.
- **Printer columns `SLACK` and `NEXT`** show each target's slack and
  when it can next be imaged.
- **The order of "no target is urgent" is explained the same way**,
  through the condition's message.

### State

The scheduler holds nothing in memory that it cannot read again. The
target in the camera is the target whose `status.reservation` names
the runner's reservation, and the time of the last switch is the last
transition of its `Imaging` condition. The season, the slack, and the
estimates of the switch's steps are in status. A restart computes the
rest again.

## Accepted for now

- **Each telescope chooses on its own.** Each runner takes its best
  target, and the claim's `409` settles two runners that choose the
  same one. The split can be poor, such as `east` taking the only
  target that `west`'s horizon allows. This is tuned later.
- **The geometry ignores clouds.** A season of cloudy nights has fewer
  hours than its geometry says. A person who worries about a target
  raises its priority.
- **Deep-sky targets only.** Planetary and solar imaging are [plan
  10](10-planetary-and-solar-imaging.md).

## Still open

- **Where the estimates of a switch's steps live.** They are facts
  about one telescope, and `Telescope` belongs to
  `observatory-operator`. One way: `observatory-operator` records its
  device operations, the slew rate and the filter change, because they
  are facts about the gear, and this operator records centering and
  the guide settle. The other: one object of this operator for each
  telescope.
- **Where the darkness and moon limits live.** On each sequence of a
  `Target`, with defaults; or the passband of each filter on
  `observatory-operator`'s `FilterWheel`, as the fact about the glass,
  and the limits derived from it here, with a sequence's override.
- **The forecast of the night**, such as "NGC 253 until 21:30, then
  M42, then M81". It is a preview that the next choice can replace.
  It could be in each target's status, as its expected window tonight,
  or in a command that computes it when asked.
- **The defaults** of each limit and of each step's time.
