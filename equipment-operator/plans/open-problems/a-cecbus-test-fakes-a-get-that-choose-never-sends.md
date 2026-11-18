# A CECBus test fakes a GET that choose never sends

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

`TestABusCreatedAfterTheReadIsLeftAlone` in `cecnode_test.go` says it
covers a person who creates a `CECBus` of the machine's name just
after the pass read that none exists, "so a GET of that name also
returns 404". It sets `createdUnseen` on the fake API server in
`cecapi_test.go`, which makes a GET of that one `CECBus` return 404.
`cecNode.choose` in `cecnode_bus.go` sends no GET. It reads the list
it is given and, when the list lacks the machine's name, sends the
create, which the fake answers with a conflict. So the setting has no
effect, and the test runs the same requests as
`TestABusOfTheMachinesNameThatAListMissedIsLeftAlone` just above it.
`createdUnseen` has no other user.

## Evidence

- `choose` calls `CreateDiscoveredCECBus`, a POST, and no GET.
- A search for `createdUnseen` finds only the field, its use in the
  fake's GET handler, and this test.

## What would confirm it

Delete the `createdUnseen` line from the test and run it. If it still
passes, and the fake's request counts match those of the other test,
the setting does nothing.

## What a fix has to decide

- Whether a request that `choose` sends can meet the race that the
  test names. If none can, the test and `createdUnseen` go.
