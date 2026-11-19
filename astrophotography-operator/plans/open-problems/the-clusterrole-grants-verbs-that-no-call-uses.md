# The ClusterRole grants verbs that no call uses

Open bug, recorded on 2026-11-18. Checked against the code on
2026-11-19: outside the tests, the operator writes no pod, creates a
`Service` with a `POST` (`workload.go:87`) and deletes one
(`workload.go:128`), and changes a claim only with the merge patch in
`outboxfinalizer.go:61`. A unit test cannot show an unused grant. A run on the `lab` fleet with the verbs removed can.

The `ClusterRole` in `deploy/rbac.yaml` grants four verbs that no
request of the operator uses:

- `create` and `delete` on `pods`. The operator only reads pods: the
  watch in `watch.go` and the `GET` of its own pod in `ownimage.go`.
  A promotion `Job` is deleted with `propagationPolicy: Background`
  in `promotion.go`, so the garbage collector deletes its pods.
- `update` on `services`. `ensure` in `workload.go` creates a missing
  `Service` and never replaces one. The only `PUT` is on
  `Deployments`.
- `update` on `persistentvolumeclaims`. `outboxfinalizer.go` changes
  a claim only with a merge patch, which needs `patch`.

`TestTheClusterRoleGrantsTheVerbsTheOperatorUses` in `deploy_test.go`
requires each of these verbs, so the test keeps the extra grants in
place. The test's name says that it checks the verbs that the
operator uses.

## Evidence

A search of the component's Go files, outside the tests, for
`http.MethodPost`, `MethodPut`, `MethodPatch`, `MethodDelete`, and
paths under `/pods` finds the calls named above and no other write to
pods, `Services`, or `PersistentVolumeClaims`.

## What would confirm it

Remove the four verbs from `deploy/rbac.yaml` on a test cluster, run
a reservation with a target through imaging and promotion, and read
the operator's log and the API server's audit log for a `403`.

## What a fix has to decide

- Whether each extra verb is removed, or kept for a call that a plan
  intends to add.
- Whether `TestTheClusterRoleGrantsTheVerbsTheOperatorUses` also fails
  on a verb that the list does not need, so the grant and the code
  cannot drift apart again.
