# 07: Running in the cluster

`transect` runs on a workstation today, with an admin kubeconfig,
port-forwards, and its resources from `--resources`.  This plan runs it
as a `Deployment` in the cluster, behind sign-in, with its resources as
CRDs and their conditions written to status.  The maintainer's direction
is to keep iterating from outside the cluster for now, so this plan is
not scheduled, and nothing in it sets the order of the other plans.  Two
of its stages need work from plan 08: stage 4 needs the full
`MetricsSource`, with its `service` fields, and stage 8 needs the kind
knowledge schema that plan 08 finishes changing.  The workstation loop
stays as it is and stays the main way to develop.

`transect` is a component of the `liken` repository, so stage 7's CI is
the CI that `ci/` generates for every component.  Its group is
`transect.liken.sh`.

## Closed on 2026-11-19

The plan is closed.  `transect` runs in a cluster, and the maintainer
decided on 2026-11-19 to drop the parts below that are not built and to
design them again when one is needed.  The sections after this one keep
the plan as it was written, so "What is built" still says that nothing
is deployed.

A home cluster has run the `Deployment` since 2026-11-11.  Its
repository follows the deploy artifact `transect-deploy` at a release
tag, and patches the in-cluster addresses of Prometheus, Alertmanager,
and the log store into the `Deployment` as `TRANSECT_*` variables.  The
ingress controller's forward auth admits one group of the identity
provider and deletes a client's copy of the user headers.  The
cluster's default-deny policy admits the ingress controller and
Prometheus, and egress stays open, as in the cluster's other
namespaces.  `transect`'s resources and overlays are files in a
read-only `git-csi-driver` volume of the cluster's repository, as
recommendation 5 gives, and `transect` loads them again on a push
without a restart.  On 2026-11-19, 6.5 hours after its pod started, the
pod used 110Mi of memory against the request of 256Mi.  That reading is
the second measurement that stage 2 asks for.

The `ConfigMap` watch is metadata-only: the store in
`crates/transect-cluster/src/cluster/mod.rs` holds
`PartialObjectMeta<ConfigMap>`.

These parts are not built, and no plan holds them:

- `transect rbac`.  The cluster's repository keeps the aggregated
  `ClusterRole` of the custom groups by hand.
- `transect crds`, the CRDs of stage 6, and the status that `transect`
  would write to them.  The conditions of a resource show on
  `/resources` only.
- Stage 8, the kind knowledge that each operator would ship.  It needs
  the CRDs.
- The access lens does not read the `expected-access` annotation.
- The "sign in again" banner.  A page whose session expired stops
  updating and shows no message.
- The line that shows who is signed in.  No code reads a user header.
- The egress policies of stage 4, which a cluster writes for itself.

## What is built

Parts of stages 1, 2, 3, and 7 are built; nothing is deployed.

- **Stage 1, in part.**  `/livez` and `/readyz` answer once the port is
  bound, which happens after the first sync, so before it every probe is
  refused.  Every flag reads the environment variable `TRANSECT_` and
  its name.  Without `--kubeconfig`, kube's inference falls back to the
  `ServiceAccount`.  On SIGTERM the server stops accepting and gives
  open responses five seconds, since an event stream never ends and
  axum's graceful shutdown would wait on it forever.  `transect crds`
  and `transect rbac` are not built.
- **Stage 2.**  The `Dockerfile` builds with `cargo auditable build
  --release --locked` on the `rust` image of `toolchains/images.toml`,
  first against a skeleton of every crate's manifest and an empty
  source for each crate, so the dependencies cache in their own layer.
  The binary links glibc, so `transect-closure.sh` collects it and the
  libraries that the loader resolves for it from a Debian snapshot, and
  the image is that tree on `scratch`, with the `trust` CA bundle and
  the license notices, as user 65534.  `rust-toolchain.toml` pins the
  rustc of a workstation and CI, and `ci pins` holds it equal to the
  Rust of the `rust` image.  The look ran once, on an earlier build of
  the image that linked musl statically on `rust:1.99.1-alpine`: the
  image against a real cluster, with a read-only root filesystem and
  every capability dropped, served the map, `/resources`, `/access`,
  and the brand's fonts, and used 139 MiB just after the first sync.
- **Stage 3, in part.**  `deploy/` is the kustomize base:
  `ServiceAccount`, `transect-reader` (aggregating),
  `transect-reader-builtin`, `transect-pod-logs` and their bindings, the
  `Deployment` with its probes, requests, and security context, a
  `Service` on port 80, and a `NetworkPolicy` that refuses every caller.
  It names no namespace; an overlay's `namespace:` moves the bindings'
  subjects.  The role is written by hand, and a test in
  `crates/transect-cluster/src/cluster/grants.rs` limits it to get,
  list, and watch, no Secrets, no wildcard, and no proxy.  It is not
  applied to a cluster yet.
- **Stage 7, in part.**  `ci/` generates `transect`'s workflow,
  `.github/workflows/component-transect.yaml`, from
  `transect/package.toml`, as it does for every component of `liken`.
  CI publishes the image `ghcr.io/liken-sh/transect`, signed keyless
  with cosign, with an SBOM and provenance attached.  After the image,
  it publishes `deploy/`, the kustomize base, as the OCI artifact
  `ghcr.io/liken-sh/transect-deploy` under the same version, with the
  image's tag in the base set to that version.  A cluster follows the
  artifact, so it moves the manifests and the image in one step.  The
  cluster's repository does not reference them yet.

What remains, in the stages' order: `transect crds` and `transect rbac`;
ConfigMaps watched as metadata only (the watch still lists them
whole); the steady-state memory that stage 2 asks for; the
`expected-access` annotation, which the base's `ServiceAccount` has but
the access lens does not read yet; stage 4's egress policies and
in-cluster addresses, stage 5's sign-in and "sign in again" banner,
stage 6's CRDs and status, the cluster's side of stage 7, and stage 8.
A cluster's repository can deploy the base today with its own overlay.

## Decisions

- **The image is the binary and the glibc libraries it loads, on
  `scratch`.**  It is built with `cargo auditable build --release
  --locked` on the `rust` image, keeps jemalloc, and includes a CA
  bundle at `/etc/ssl/certs`, since the registry and Prometheus clients
  read the system's roots, and the license notices.  It runs as user
  65534 with a read-only root filesystem and every capability dropped.
  Templates and assets are already compiled in.

- **Every rule is `get`, `list`, and `watch`, and none covers
  Secrets.**  The base `ClusterRole` names every built-in kind the
  stores read, and a test checks the list against the `reflect` calls in
  `crates/transect-cluster/src/cluster/`: Nodes, Namespaces, Pods,
  ReplicaSets, ControllerRevisions, Deployments, StatefulSets,
  DaemonSets, Jobs, CronJobs, Services, EndpointSlices,
  Ingresses, IngressClasses, NetworkPolicies,
  PodDisruptionBudgets, PersistentVolumes, PersistentVolumeClaims,
  StorageClasses, CSIDrivers, ConfigMaps, DeviceClasses,
  ResourceClaims, ResourceSlices, CRDs, every RBAC kind, and
  ServiceAccounts.  The only write is `patch` on `transect.liken.sh`
  `*/status`.  The registry mirror mode `api-server` reads with the
  account's token and needs neither `services/proxy` nor `nodes/proxy`,
  and neither is granted.

- **ConfigMaps are watched as metadata only.**  `list` on ConfigMaps
  returns their data to any client that asks for whole objects, and that
  data sometimes contains credentials.  The grant cannot be narrowed to
  metadata, so `transect` keeps its `ConfigMap` watch metadata-only, and
  no page asks for a `ConfigMap` whole.

- **Custom kinds are granted by aggregation and by `transect rbac`.**
  The base defines `transect-reader` with an `aggregationRule` on the
  label `transect.liken.sh/aggregate-to-reader: "true"`, so an operator
  can ship a `ClusterRole` for its own group.  Most operators ship none,
  and `transect` watches every CRD, so `transect rbac` prints one
  aggregated `ClusterRole` with `get`, `list`, and `watch` for every
  group the cluster's CRDs serve, for the cluster's repository to review
  and commit.  No CRD serves the core group, so Secrets are never in
  it.  A wildcard rule would include Secrets, so there is none.

  That role grants more than `transect` keeps.  `transect` trims an
  unknown kind to its metadata, conditions, and printer columns before
  the store, but the account's token can read every object of every
  group whole, and custom resources contain credentials inline often
  enough, such as a Helm release's `values`.  This is the same exposure
  as `list` on ConfigMaps, below, and the cluster's reviewer should
  read the role with it in mind.  `transect rbac --exclude-group
  <group>` leaves a group out, and a group left out is one line on
  `/resources` naming it, not a refusal per kind.

- **One replica, no leader election.**  A restart costs one full list.
  Status is written by server-side apply under the field manager
  `transect`, so two pods during a rollout write the same thing.  One
  replica has a cost: the loss view exists for the moment a machine is
  lost, and a single replica on that machine is down until it is
  rescheduled, which takes a few minutes when its pod is evicted on the
  default timeouts.  A second replica would cost little more than its
  own watches, since both write the same status.  The plan starts with
  one replica, and the workstation loop gives access while it is down.

- **Probes.**  `/livez` answers when the process does.  `/readyz` passes
  once the first sync of the required stores is done, and stays passing
  after that: an API server outage that left `transect` unready would
  remove it from its `Service` when an operator most needs it, so
  staleness shows on the page instead.  It does not wait on optional
  kinds, the custom kinds, Flux's, and RBAC, which retry forever in
  `crates/transect-cluster/src/cluster/discovery.rs`.  A startup probe
  on `/readyz` allows 60 seconds, against the 30-second sync timeout
  that exits the process.

- **Requests and limits.**  The `Deployment` requests `cpu: 50m` and
  `memory: 256Mi`, with a `512Mi` memory limit and no CPU limit.  The
  trimmed watches change the memory figure, so stage 2 measures again
  before these are final.

- **Every flag reads an environment variable** as `TRANSECT_*`, and
  `--listen` is `0.0.0.0:8080` in the manifest and in the image.

- **The CRDs come from the code.**  `transect crds` prints them, each
  with `subresources: {status: {}}` so the status patch has a
  subresource to write to, and the group stays in its one constant.  A
  test checks the committed `deploy/crds.yaml` against the output.

- **Files and objects both load.**  `transect` watches its CRDs beside
  `--resources`.  When a file and an object share a kind and a name,
  both get a `Conflict` condition and the file takes precedence, since
  it is what a developer is editing.  Status goes only to objects.

- **Credentials are mounted files.**  A `kubernetes.io/dockerconfigjson`
  `Secret` in `transect`'s namespace is mounted for `--registry-auth`,
  and a log store's credential is a `Secret` mounted as a file that the
  `LogSource` names by path.  The kubelet reads both; `transect` gets no
  RBAC on Secrets.  This replaces the design's line that `transect`
  would read the one `Secret` a `LogSource` names.

- **The base ships no `Ingress`.**  How a cluster exposes `transect`,
  and through which sign-in, belongs in the cluster's repository.  The
  docs give a sketch with forward auth, and two ways in when the
  identity provider is down: `kubectl port-forward` and the workstation
  loop.  The sketch strips any user header the client sends before the
  auth service sets its own, or the "signed in as" line could be forged.

- **An expired session shows on the page.**  When a session expires,
  the server-sent events stream reconnects and gets a redirect to
  sign-in, which htmx drops with no message.  The page treats a non-200
  answer on the stream as signed out and shows a "sign in again"
  banner.

- **The base's `NetworkPolicy` refuses every caller and leaves egress
  open.**  The callers to admit, the ingress controller and on some CNIs
  the kubelet's probes, and the addresses `transect` calls are the
  cluster's own, so the overlay adds them.  The next decision lists the
  full set the cluster should have.

- **NetworkPolicies cover both directions.**  `Ingress` admits the
  ingress controller only, by labels the cluster patches in, and the
  kubelet's probes, by the machines' addresses, since some CNIs drop
  probes under a default-deny ingress policy.  Egress admits DNS; the
  API server, which on a distribution that runs it on the host network,
  such as k3s, is an `ipBlock` of the servers' addresses, not a pod
  selector; Prometheus, Alertmanager, and the log store; and registries
  on port 443.  The cluster also admits `transect` in the policies of
  its Prometheus, Alertmanager, and log store.

- **Kind knowledge ships with each operator** as an opt-in kustomize
  component, `deploy/transect/`, next to its CRDs.  It is opt-in
  because an object of a CRD that is not installed fails the whole
  apply.  Shipping it in each operator's repository makes the kind
  knowledge schema a contract across repositories, so stage 8 waits
  until plan 08 has finished changing that schema.

- **`transect`'s own account declares why it is powerful.**  It reads
  every kind the cluster serves, so plan 09's access lens would list it
  as powerful.  The base's `ServiceAccount` has the annotation
  `transect.liken.sh/expected-access`, which states that it reads the
  cluster to draw the map.

## Open, with a recommendation

1. **How the page signs people in.**  An identity provider in front,
   through forward auth, with one group allowed in; `transect`'s account
   then bounds what any viewer sees.  The other way, each viewer's own
   Kubernetes credentials, does not fit one shared cache: it needs a
   `SubjectAccessReview` per object per render, or `impersonate`, which
   is stronger than any read.  Recommendation: an identity provider in
   front, and the user header read only to show who is signed in.
2. **Pod logs.**  `get` on `pods/log` in every namespace is the most
   sensitive grant, because log lines can contain credentials.
   Recommendation: its own `ClusterRole` and binding, so a cluster can
   leave it out and fall back to a `LogSource` or to no logs.
3. **The namespace.**  Recommendation: `transect`.
4. **The CRD schemas.**  Recommendation: start loose, with
   `x-kubernetes-preserve-unknown-fields` on `spec`, since the parser
   already validates and the shapes still change; derive the schemas
   from the Rust types once they settle.
5. **Overlays in the cluster.**  An overlay is a plain object of a
   watched kind, so it cannot be a CRD.  Recommendation: mount a
   `--resources` directory from a read-only git volume, and keep
   `Intent` for what is stored in the cluster.
6. **`--secret-annotations` in the cluster.**  Recommendation: off; the
   base grants nothing on Secrets, and plan 09's `Secret` pages work
   without it.

## Stages

### 1. Probes, flags, and the environment

This stage covers `/livez` and `/readyz` as the decision describes,
clap's `env` feature, `transect crds` with the status subresource, and
`transect rbac`
with `--exclude-group`.  Look: `cargo test` passes; `curl` reads both
probes against the workstation loop, with `/readyz` failing until the
stores sync and still passing after the workstation's network drops;
`transect rbac` prints a role with no core group in it, and with a
group excluded, `/resources` names it in one line.

### 2. The image

This stage covers the `scratch` image, built locally.  Look: `docker
run` it with a mounted kubeconfig, and its pages and its memory match
`cargo run` against the same cluster.

### 3. RBAC and the Deployment, with no Ingress

This stage covers the base: namespace, `ServiceAccount`, ClusterRoles,
bindings, `Deployment`, and `Service`, applied by hand, with the role
`transect rbac` printed.  Look: reach it through `kubectl port-forward`;
`/resources` shows no refusals, or one folded line for the groups left
out; `kubectl auth can-i --as=system:serviceaccount:...` agrees with
each rule and refuses Secrets; and the `api-server` registry mirror
answers to the account's token, or the page states that it does not.

### 4. In-cluster addresses and NetworkPolicies

This stage covers Prometheus, Alertmanager, and the log store by
`Service` name, through `MetricsSource` and `LogSource`, the log store's
credential as a mounted file, and the ingress and egress policies.
Look: usage bars, alerts, and logs all show; probes pass; and no policy
drop alert fires.

### 5. Sign-in

This stage covers an `Ingress` in the cluster's repository, with forward
auth and the user header stripped, and the stream's "sign in again"
banner.  Look: a signed-out browser gets the sign-in page, a signed-in
one gets the map; a request that sends its own user header shows the
signed-in user, not the header; an expired session shows the banner; and
the port-forward still works with the identity provider stopped, which
stays the documented way in when it is down.

### 6. CRDs and status

Apply the CRDs and one `Layers` object.  Look: `kubectl get -o yaml`
shows its conditions in status; a file and an object with the same
name both show `Conflict`, and the file's version is the one drawn.

### 7. Publish

CI builds, signs, and publishes the image, then publishes the deploy
artifact `transect-deploy`, and the cluster's repository follows the
artifact.  The CI is the `liken`
repository's.  Look: a tag produces an image that `cosign
verify` accepts, and the cluster runs it.

### 8. Kind knowledge from the operators

This stage follows plan 08.  It covers each `liken` operator's
`deploy/transect/` component, which contains the kind knowledge now in
`local/resources/`.  Look: delete the local copies, and every page
shows the same text from the cluster's objects.

## Not in this plan

- The flow agent, a separate image and base with `CAP_NET_ADMIN` on the
  host network: slice 2.
- More than one replica, as the decision states.
- An arm64 image.
