# CRD schema drills

Test how apiextensions treats a schema against the dev cluster before
you rely on it. The order of operations is the behavior that costs the most
time. **The API server prunes unknown fields, and drops nulls for
values that are not nullable, before CEL and schema validation run.**
So a rule that looks complete in the schema never sees the value that
it was written to reject.

The same order causes these behaviors:

* Only `kubectl apply` and `kubectl create` use strict decoding, so
  only they reject an unknown field outright.
* `kubectl patch` warns and prunes. That can turn on a map value
  that needs no configuration, though the patch did not ask for an
  empty value. A patch of
  `{replicas: 2}` against a property that does not accept `replicas`
  is stored as `{}`.

`liken`'s own schema for `spec.features` came from these experiments,
not from a design: a map, plus CEL, plus `nullable`, plus the pair
`maxProperties` and `x-kubernetes-preserve-unknown-fields`. The first
design made each feature a property, and expected the API server to
reject an unknown slug and a null. It rejected neither.

## The drill

Before you commit a schema or CEL change to a CRD, such as one in
`machine/manifests/`, `cluster/manifests/`, or a component's
`deploy/`, prove it on a throwaway CRD in a scratch group.
Use a group that can never collide with a real one, for example
`drills.example.com`.

1. Write the CRD with only the field under test, in the same shape
   that the real schema will use.
2. `dev-cluster/kubectl apply -f /tmp/drill-crd.yaml`
3. Test admission without storing anything:
   `dev-cluster/kubectl create --dry-run=server -f /tmp/case.yaml`
4. Test the paths that `apply` does not cover. Create the resource,
   then send merge patches against it, and read back what the API
   server stored. A patch is where pruning shows.
5. `dev-cluster/kubectl delete crd <name>.drills.example.com`

Use this drill in place of a release build. A release build takes
minutes and proves less, because it tells you only whether a whole
boot worked.
