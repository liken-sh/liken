# transect

`transect` is a live, read-only map of a Kubernetes cluster for the
people who run it.  It draws the cluster as nested layers, from the
whole cluster down to one container, and the relationships between
them: what runs on which machine, where each workload keeps its data,
which pod holds which device, which source each object reconciles from,
and what depends on what.  The map is gray where things are healthy,
so only a problem has a color.

`transect` changes nothing in the cluster.  It reads the Kubernetes API
and Prometheus, and a directory of its own resources for the facts that
neither of those gives.  The design is in
[plans/00-design.md](plans/00-design.md), and the plans in
[plans/completed/](plans/completed/) record how each part was built.

## What it shows

- **The cluster map** at `/` has one band per layer, with each
  namespace drawn as a parcel sized by its weight, the machines with
  their usage from Prometheus, and a ground band for storage and other
  things outside the cluster.  The sidebar lists what to know now.
- **A page per namespace, workload, and pod**, under
  `/namespaces/<namespace>`, each with a card: what the thing is, how
  it is doing, what it depends on and what depends on it, and links to
  the directory a GitOps tool such as Flux applies it from and the
  commit its image was built from.  A namespace of DaemonSets draws as
  a matrix of DaemonSets by machine, and a GitOps object or a custom
  resource of a known kind gets a page of its own.
- **Edges** of eight kinds, turned on and off from a row above each
  picture that draws them:
  runs on, depends on, authenticates through, stores on, holds,
  reconciles from, controlled by, and allowed to call, which
  NetworkPolicies give.  Loops among them are a cold-start risk, and
  the map lists them.
- **Health** from readiness, restarts, error lines in pod logs, a custom
  resource's own conditions, and the alerts that Alertmanager reports as
  firing, leaving out what its silences and inhibitions hide.  An alert
  colors the namespace, object, or machine its labels name, and "Now"
  lists every one, the worst first.
- **A lost machine** at `/?lose=<machine>`: what goes down with it,
  what moves elsewhere, what stays down until it returns, and what
  their dependents lose.
- **A workload's section** at
  `/namespaces/<namespace>/<resource>/<name>/section`: one
  workload drawn from its entry to its storage, with lines.
- **A pod's last log lines**, from the Kubernetes API or from a log
  store, tagged by the log patterns that match them.
- **Stored text** that the cluster's people and operators write: why
  a thing exists, what goes wrong with it, and what it provides, from
  annotations, overlays, and `transect`'s own resources.  Each such line
  is marked with where it was written.
- **Access** at `/access`: every port that answers from outside the
  cluster, with the TLS, sign-in, and `NetworkPolicy` on the way to it,
  and what each account may do and where, read from RBAC alone.  A
  namespace and a workload have an access tab, and a `ServiceAccount`'s
  page draws what it may do and why.
- **`/resources`**: every resource and overlay `transect` loaded, with
  the conditions that state what is wrong with one.

`transect` does not yet observe traffic between workloads (the `calls`
edge).

## Running it

`transect` runs on a workstation with a kubeconfig and has the access
of that kubeconfig's user.  Reach Prometheus through any URL, such as a
port-forward:

```bash
kubectl -n <monitoring-namespace> port-forward svc/prometheus 9090
cargo run --release -- --kubeconfig ~/.kube/config \
  --prometheus http://localhost:9090 \
  --resources ./resources/
```

Then open <http://127.0.0.1:8080>.  `--resources` is optional.  It
names a directory of YAML files that contain `transect`'s own
resources, such as the `Layers` resource that assigns namespaces to
bands, and overlays that add annotations to live objects without
editing the cluster.  `transect`'s facts about particular projects come
from those resources too: the Prometheus queries and alert conventions,
the GitOps tool, the ingress controller, and the forges.  Without them,
those parts are off: the usage, the alerts, and the traffic each name
the resource that would show them, and a repository on a forge that no
resource declares is shown as text.  [examples/](examples/) contains
these resources for public projects, such as Flux, cert-manager,
Traefik, and kube-prometheus, in one directory each.  Copy them into
your directory and edit them.  The design describes each resource's
shape.  `cargo run -- --help` lists every flag, including
`--alertmanager` for the alerts, registry credentials and mirrors for
reading image labels, and the address of a log store.

## Running in a cluster

CI publishes the image `ghcr.io/liken-sh/transect`, and the kustomize
base in [deploy/](deploy/) as the OCI artifact
`ghcr.io/liken-sh/transect-deploy`, under the same version.  A release
takes the version of its tag, such as `2026.11.17-001`, and moves
`:latest` on the image and on the artifact.  A push to `main` that
changes `transect` publishes a development build, such as
`2026.11.17-001-dev-017-abcdef01`, which is 17 commits past
`2026.11.17-001`, at commit `abcdef01`.  A development build never
moves `:latest`.  CI sets the tag of the image in the published base to
the base's own version, so a cluster that follows the artifact moves
the manifests and the image in one step.  A kustomization that takes
`deploy/` from git names the version itself, because the base in git
names `latest`:

```yaml
images:
  - name: ghcr.io/liken-sh/transect
    newTag: <a version of ghcr.io/liken-sh/transect>
```

CI signs each image keyless with cosign, and attaches an SBOM and the
build's provenance to it.  Before you deploy an image, `liken verify
image` checks its signature and reads its SBOM and its provenance.

The image holds the binary and the glibc libraries that it loads, on
`scratch`, with a CA bundle and the license notices.  The base runs it
as user 65534 on a read-only root filesystem.

The base contains the `ServiceAccount`, read-only ClusterRoles and
their bindings, one `Deployment`, a `Service` on port 80, and a
`NetworkPolicy` that refuses every caller.  It names no namespace and
ships no `Ingress`.  An overlay sets `namespace:`, adds an `Ingress`
behind the cluster's sign-in and a rule admitting its ingress
controller, and sets the addresses of Prometheus, Alertmanager, and the
log store as env:

```yaml
apiVersion: kustomize.config.k8s.io/v1beta1
kind: Kustomization
namespace: transect
resources:
  - <the transect-deploy artifact, or deploy/ from git>
patches:
  - target: {kind: Deployment, name: transect}
    patch: |
      apiVersion: apps/v1
      kind: Deployment
      metadata: {name: transect}
      spec:
        template:
          spec:
            containers:
              - name: transect
                env:
                  - name: TRANSECT_PROMETHEUS
                    value: http://prometheus.monitoring.svc:9090
```

Every flag reads an environment variable, `TRANSECT_` and the flag's
name in capitals, so `--alertmanager` is `TRANSECT_ALERTMANAGER`.
Without `--kubeconfig`, `transect` uses its `ServiceAccount`.  The
account may get, list, and watch the built-in kinds the map draws, and
nothing on Secrets.  For custom kinds, add a `ClusterRole` labeled
`transect.liken.sh/aggregate-to-reader: "true"` that lists the groups
your CRDs serve; `/resources` names each kind it may not list.
`transect-pod-logs` grants pod logs on its own, so an overlay can delete
it and read logs from a `LogSource` instead.

`/livez` and `/readyz` answer once the first list of the cluster is
done, and stay passing through an API server outage.  When the sign-in
is down, reach `transect` with `kubectl port-forward svc/transect
8080:80` or with the workstation loop above.
