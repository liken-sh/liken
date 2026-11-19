# A failed `DOME_POLICY` write is not retried

Open bug, recorded on 2026-11-18. A unit test reproduced it on
2026-11-19.

`keepDomePolicies` in `domepolicy.go` writes `DOME_POLICY` to each
connected mount of a `Ready` reservation's telescope when the policy
differs from what the observatory's domes require. Before it starts a
write, it stores the server's epoch and the wanted policy in
`mountPolicies.tried`, and it skips a mount whose `tried` entry equals
the current key. The comment on `tried` gives the reason: a driver
that refuses a policy is not written again on each pass.

The key does not record why the write ended. When `lockPolicy` fails
for another reason, such as the 30 second `policyWait` limit on a slow
driver, the pass only logs the error, and the mount keeps the wrong
policy until the server's epoch changes. `indiconn.go` changes the
epoch when a connection opens and on INDI events other than an update
or a message, so a mount whose driver stays connected and defines no
property is not written again.

## Evidence

- `domepolicy.go`, `keepDomePolicies`: `p.tried[id] = key` runs before
  the goroutine that calls `lockPolicy`, and the goroutine does not
  clear the entry when `lockPolicy` returns an error.
- `indiconn.go:118` and `indiconn.go:133`: the only places that set a
  server's epoch.

## The reproduction

A test in a `synctest` bubble with the fake INDI server holds every
change to the mount's `DOME_POLICY`, deletes the observatory's last
`Dome`, so the mount needs `DOME_IGNORED`, and waits past
`policyWait`. Then it releases the hold, changes a label of the
`Telescope` east so that a pass runs while the server's epoch and
the wanted policy stay the same, and reads `DOME_POLICY`. It failed
with:

```
after the write timed out and a later pass ran, DOME_POLICY is "Ok DOME_LOCKS", want Ok DOME_IGNORED because the observatory has no dome
```

With the `tried` check removed from `keepDomePolicies`, the same test
passes. The test is not in the tree, because a failing test cannot land on
`main`.

A change to the domes does not show the defect: deleting the last
dome or adding the first one changes the wanted policy, which is part
of the key in `tried`, so the policy is written again.

## What a fix has to decide

A mount whose driver did not answer a `DOME_POLICY` write in time
keeps the wrong policy until its driver reconnects.

- Which failures count as a refusal that must not be written again,
  and which ones, such as a timeout or a closed connection, let the
  next pass write the policy again?
- Does a retry after a timeout wait first, so that a driver that
  never answers is not written on every pass?
