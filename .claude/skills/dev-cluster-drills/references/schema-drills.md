# CRD schema drills

Do not reason about apiextensions behavior. Drill it against the dev
cluster. The behavior that costs the most time is the order of
operations: **the API server prunes unknown fields and drops nulls for
non-nullable values before CEL and schema validation run.** A rule that
looks airtight in the schema never sees the value it was written to
refuse.

Two more behaviors follow from the same order:

* Only `kubectl apply` and `kubectl create` use strict decoding, so
  only they refuse an unknown field outright.
* `kubectl patch` warns and prunes. That can flip a zero-config map
  value on by itself: a patch of `{replicas: 2}` against a property
  that does not accept `replicas` lands as `{}`.

liken's own schema for `spec.features` came out of these experiments,
not out of a design: a map, plus CEL, plus `nullable`, plus the
`maxProperties` and `x-kubernetes-preserve-unknown-fields` pair. The
first design named each feature as a property and expected the API
server to refuse an unknown slug and a null. It did neither.

## The drill

Before you commit a schema or CEL change to `machine/manifests/` or
`cluster/manifests/`, prove it on a throwaway CRD in a scratch group.
Use a group that can never collide with a real one, for example
`drills.example.com`.

1. Write the CRD with only the field under test, in the same shape the
   real schema will use.
2. `dev-cluster/kubectl apply -f /tmp/drill-crd.yaml`
3. Probe admission without storing anything:
   `dev-cluster/kubectl create --dry-run=server -f /tmp/case.yaml`
4. Probe the paths that `apply` does not cover. Create the resource for
   real, then send merge patches against it, and read back what the API
   server stored. A patch is where pruning shows itself.
5. `dev-cluster/kubectl delete crd <name>.drills.example.com`

Use this instead of a release build. A release build costs minutes and
proves less, because it can only tell you that a whole boot worked or
did not.
