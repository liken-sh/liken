# 06, Grading policies

Stub. Proposed on 2026-10-12. Not designed.

An exposure can finish and still be useless: a cloud passed, the wind
shook the telescope, guiding lost its star, or the focus drifted. A
target that wants sixty frames should mean sixty good ones. So a
person can declare a policy that rejects frames, and a rejected frame
does not count toward its target. Without a policy, every frame counts.

What the policy measures, and how much each measure matters, is the
person's choice, never the operator's. The operator offers measures it
can produce: PHD2's guiding error and the guide star's signal during
the exposure, which cost nothing to read, and the star count, the
half-flux radius, and the star shape from the worker of [plan
02](02-solving-and-centering.md). A policy can instead name the
person's own container, run as a `Job`, or as an Indexed `Job` to grade
a whole night again after the policy changes.

A grade is a record keyed by the frame's identity. It never changes
the frame.

To design: everything, when the first real nights show what is needed.
