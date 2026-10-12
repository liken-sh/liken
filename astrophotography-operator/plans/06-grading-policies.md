# 06, Grading policies

Proposed on 2026-10-12. Not built. This plan records the problem and
the decisions of the design conversation. How a policy is written is
deliberately left open until real nights show what is needed.

## The problem

An exposure can finish and still be useless: a cloud passed, the wind
shook the telescope, guiding lost its star, or the focus drifted. A
target that wants sixty frames should mean sixty good ones, or a cloudy
hour counts as progress.

## Decisions

- **Grading is optional.** Without a policy, every frame counts.
- **A person's policy can reject a frame, and a rejected frame does not
  count toward its target.**
- **What matters, and how much, is the person's choice, never hard-wired
  into the operator.** The operator offers measures it can produce, and
  the policy chooses among them.
- **Some measures cost nothing.** PHD2 reports the guiding error and
  the guide star's signal during each exposure, which catches lost
  guiding, wind, and most clouds without reading the frame. Measures of
  the frame itself, such as the star count and the stars' size and
  shape, come from the worker of [plan 02](02-solving-and-centering.md),
  and only a policy that asks for them pays to read the frame.
- **A policy can name the person's own container** in place of the
  operator's measures, run as a `Job`, or as an Indexed `Job` to grade a
  whole night again after the policy changes.
- **A grade is a record keyed by the frame's identity.** It never
  changes the frame.

## Still open

The form of a policy: rules that each reject, or a weighted score with
a cutoff, and whether a measure compares against fixed values or
against the night's other frames.
