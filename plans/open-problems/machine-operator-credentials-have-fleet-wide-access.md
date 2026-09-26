# Restrict machine-operator credentials to one node

Open security design question, medium priority. Each machine operator
acts on its own node, but its API credentials authorize writes across
the fleet. A stolen worker operator token can affect other machines
without first compromising a leader's host.

## Current authorization

The [machine-operator manifest](../../machine-operator/manifests/machine-operator.yaml)
uses one `ServiceAccount`, `liken-machine-operator`, for every pod in the
`DaemonSet`. Projected tokens can differ between pods, but they all
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
with the token can send a different request. The filter does not limit
which objects that identity may access. Without list permission, a
caller can still access an object whose name it already knows.

## Consequences and evidence

The RBAC manifest lets the token forge another machine's status or
heartbeat, cordon or relabel any node, and rewrite another node's device
inventory. It also permits pod eviction across the cluster. Node
deletion can trigger the `k3s` etcd-member cleanup used by
[demotion.go](../../machine-operator/demotion.go).

These privileges are less than full cluster-admin. The role cannot edit
an existing `Cluster` spec or read arbitrary secrets, though it can
create new `Machine` and `Cluster` objects. Its cluster-wide read
permissions are also broader than the operator's ordinary node-local
queries.

This finding comes from a static review of the authorization rules. No
token was taken from a live cluster, and no admission or exploitation
test was run. A deployment could add its own policy to restrict access,
but these manifests do not.

## Immediate safeguards

Document the actual authority and audit each verb against its caller.
Remove unused permissions if any are found. These changes reduce or
clarify exposure. A smaller role on the same shared account still cannot
distinguish node A from node B.

## Design choices

Node-local authorization needs a separate identity for each node, and
enforcement of what that identity may do. Options to evaluate include
per-node client certificates or `ServiceAccount` identities, scoped
roles, validation of writes, and moving fleet-wide operations to a
controller.

RBAC `resourceNames` cannot be templated per pod on the existing shared
role. That list also cannot constrain collection create requests in the
same way as named reads or updates. An admission webhook can validate
writes, but it does not restrict reads. Read scope still needs its own
authorization design.

The design must cover issuance, rotation, revocation, and recovery before
the machine operator starts. This overlaps with the credential requirements
of the [static-pod candidate](system-image-versioning.md). A per-node
certificate with no other change fixes neither permissions nor
scheduling.

## Remedy scope

The fix is a broader design for identity and authorization, because it
changes what a node's credential may do to the rest of the fleet. It
must keep bootstrap, self-management, demotion, and the required
fleet-wide observations working. Replacement credentials must not
silently get the same broad authority.

The project should decide, and document, whether node-local access is a
supported security guarantee. The current source comments describe
stronger node-local protection than the shared roles enforce. Narrower
behavior in the code does not protect against a stolen token, because
the token keeps the same roles.

## Tests needed

Use real API authorization checks for node A against its own resources
and node B's resources. Include status, leases, slices, node deletion,
pod eviction, and collection creation. Verify legitimate bootstrap and
demotion, credential rotation, old-credential rejection, and recovery
when the initial credential cannot be obtained. Test any admission or
controller dependency while it is unavailable.
