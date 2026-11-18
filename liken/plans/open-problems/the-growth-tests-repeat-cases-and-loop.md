# The growth tests repeat cases and loop

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

Some tests in `init/grow_test.go` repeat the case of another test,
and some give their cases as a loop, not as a table with `t.Run`.

## The evidence

- `TestPlanAllGrowthRefusesToGrowSystemSlots` (line 245) and
  `TestPlanAllGrowthRefusesToGrowASystemSlot` (line 272) both plan a
  `systemA` of 512 MiB that the spec declares as 1 GiB, and both expect
  an error that names `systemA`.
- `TestPlanAllGrowthLeavesRightSizedSystemSlotsAlone` (line 261) and
  `TestPlanAllGrowthAcceptsASlotAtItsClaimedSize` (line 296) both plan
  a `systemA` of 512 MiB that the spec declares as 512 MiB, and both
  expect no plan and no error.
- `TestPlanAllGrowthRefusesToGrowTheBootRoles` (line 283) loops over
  `biosBoot` and `bootHome`, and
  `TestPlanAllGrowthRefusesToGrowSystemSlots` loops over the words it
  wants in the error.

The `testing` skill under `.agents/skills` asks for several cases as a
table with `t.Run`, not as a loop.

## What would confirm it

A read of the four tests side by side.

## What a fix has to decide

Which test of each pair stays. One table of fixed-size roles, with
`systemA`, `biosBoot`, and `bootHome`, can replace both refusal tests.
