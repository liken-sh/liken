# Restrict machine-operator credentials to one node

Open security design question. Review priority: medium. Machine operators
act on their own nodes, but their API credentials authorize writes across
the fleet. A stolen worker operator token can affect other machines
without first compromising a leader's host.

## Current authorization

The [machine-operator manifest](../../machine-operator/manifests/machine-operator.yaml)
uses one `ServiceAccount`, `liken-machine-operator`, for every pod in the
`DaemonSet`. Projected tokens can differ between pods; they nevertheless
authorize the same account and roles.

The cluster-scoped permissions include:

| Resource | Verbs |
| --- | --- |
| `machines` | get, list, watch, create |
| `machines/status` | update |
| `clusters` | get, create |
| `nodes` | get, delete, patch |
| `resourceslices` | get, create, update, delete |
| `resourceclaims` | get |
| `pods` | list |
| `pods/eviction` | create |
| `services`, `helmcharts` | list |

Separate namespaced roles permit get, create, and update on any `Lease`
in `liken-system`, and get on the single `registry-credentials` `Secret`.
The secret rule has `resourceNames`; the node, status, slice, and lease
rules do not. There is no lease-delete permission.

The watch in [main.go](../../machine-operator/main.go) selects the
operator's own `Machine`. The API applies that query filter, but a caller
with the token can send a different request. It is not a restriction on
which objects that identity may access. Omitting list permission also
does not prevent access to an object whose name is already known.

## Consequences and evidence

The RBAC manifest permits another machine's status or heartbeat to be
forged, any node to be cordoned or relabeled, and another node's device
inventory to be rewritten. It also permits pod eviction across the
cluster. Node deletion can trigger the `k3s` etcd-member cleanup used
by [demotion.go](../../machine-operator/demotion.go).

These privileges are not full cluster-admin. The role cannot edit an
existing `Cluster` spec or read arbitrary secrets, though it can create
new `Machine` and `Cluster` objects. Its cluster-wide read permissions
also remain broader than the operator's ordinary node-local queries.

This finding comes from static authorization review. No token was taken
from a live cluster, and no admission or exploitation test was run.
Additional deployment-specific policy could restrict access, but these
manifests do not establish that boundary.

## Immediate safeguards

Document the actual authority and audit each verb against its caller.
Remove unused permissions if any are found. These changes reduce or
clarify exposure; a smaller role attached to the same shared account
cannot distinguish node A from node B.

## Design choices

Node-local authorization needs both a distinguishable node identity and
enforcement of what it may do. Options to evaluate include per-node
client certificates or `ServiceAccount` identities, scoped roles,
validation of writes, and moving fleet-wide operations to a controller.

RBAC `resourceNames` cannot be templated per pod on the existing shared
role. Collection create requests cannot be constrained by that list in
the same way as named reads or updates. An admission webhook can validate
writes, but does not restrict reads; read scope still needs an appropriate
authorization design.

The design must cover issuance, rotation, revocation, and recovery before
the machine operator starts. This overlaps with the credential requirements
of the [static-pod candidate](system-image-versioning.md). Issuing a
per-node certificate alone fixes neither permissions nor scheduling.

## Remedy scope

**Broader identity and authorization design.** This changes the trusted
boundary between a node and the fleet. It must preserve bootstrap,
self-management, demotion, and required fleet-wide observations without
silently granting replacement credentials the same broad authority.

Whether this boundary is a supported security promise should be explicit.
The current source comments describe stronger node-local protection than
the shared roles enforce; narrowing behavior in code alone cannot supply
that protection against a stolen token.

## Tests needed

Use real API authorization checks for node A against its own resources
and node B's resources. Include status, leases, slices, node deletion,
pod eviction, and collection creation. Verify legitimate bootstrap and
demotion, credential rotation, old-credential rejection, and recovery
when the initial credential cannot be obtained. Test any admission or
controller dependency while it is unavailable.
