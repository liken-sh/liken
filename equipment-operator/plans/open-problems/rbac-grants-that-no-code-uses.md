# RBAC grants that no code uses

Open bug, recorded on 2026-11-18. Checked against the code on
2026-11-19: `grep -rn "MethodPut\|ReplaceStatus" --include=*.go .`
finds no call outside the tests, and `applyCECBus` is called only at
`cecbus_client.go:99` and `:116`, each with a `/status` path. A unit
test cannot show an unused grant. A run on the `lab` fleet with the
verbs removed can.

`deploy/rbac.yaml` grants two verbs that no request of the component
needs:

- The `equipment-operator` `ClusterRole` grants `update` on
  `receivers/status`. `update` is the verb of a PUT, and the component
  sends no PUT. It writes the status with server-side apply, a PATCH.
  `apiclient.ReplaceStatus` in `kubernetes/apiclient/objects.go` sends
  a PUT, and no file of the component calls it.
- The `equipment-operator-cec` `ClusterRole` grants `patch` on
  `cecbuses`. Its comment says that the current pods never patch a
  `CECBus` and that `patch` serves pods of an earlier release during a
  rollout. The node workload creates a `CECBus` with a POST
  (`CreateDiscoveredCECBus`) and patches only `cecbuses/status`.

## Evidence

A search of the component for `MethodPut` and for `ReplaceStatus`
finds no call. `applyCECBus` in `cecbus_client.go` is called only with
a `/status` path.

## What would confirm it

Remove each verb on the `lab` fleet and run the operator and the node
workload through discovery, a status write, and a `CECBus` create. A
`Forbidden` in the logs shows a request that the search missed.

## What a fix has to decide

- Whether the release that applied a `CECBus` with `patch` can still
  run beside the current pods. If it cannot, `patch` goes.
