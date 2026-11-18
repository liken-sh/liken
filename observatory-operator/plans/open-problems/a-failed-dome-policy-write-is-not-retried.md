# A failed `DOME_POLICY` write is not retried

Open bug, recorded on 2026-11-18. It is suspected and not reproduced.

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
- `indiconn.go` lines 118 and 133: the only places that set a
  server's epoch.

## What would confirm it

A test in a `synctest` bubble with the fake INDI server, where the
mount's driver does not reply to a `DOME_POLICY` write until
`policyWait` ends. After the timeout, delete the observatory's last
dome or add one, and check whether a later pass writes the policy
again while the server's epoch stays the same.

## What a fix has to decide

- Which failures count as a refusal that must not be written again,
  and which failures, such as a timeout or a closed connection, clear
  the `tried` entry so that the next pass writes the policy again.
- Whether a retry after a timeout needs a delay, so that a driver that
  never replies is not written on every pass.
