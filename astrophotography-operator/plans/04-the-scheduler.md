# 04, The scheduler

Stub. Proposed on 2026-10-12. Not designed.

Inside a reservation, the operator chooses what to image next. The
choice is a placement problem like kube-scheduler's: targets state
what they need, and the telescope and the sky state what they offer at
each moment. After each exposure the operator filters out the targets
that cannot be imaged now and scores the rest. It plans one exposure at
a time, the method of N.I.N.A.'s Target Scheduler, so a cloud or a lost
guide star an hour in costs that exposure and not the night.

The filters need the sky: each target's altitude and azimuth from the
site's coordinates and the time, twilight, and the moon. They also need
the telescope's own horizon, from `observatory-operator` [plan
18](../../observatory-operator/plans/18-saved-horizons.md), because
trees and roofs block part of the sky. The score favors a target that
sets soon, one that is nearly done, and the one already in the camera,
so the telescope does not switch back and forth.

A pass runs when an exposure ends, when a condition of the observatory
changes, or when PHD2 loses its star. Rise, set, and twilight are clock
times, so a timer for each is correct.

To design: the filters, the score, and the source of the sky's
geometry in Go.
