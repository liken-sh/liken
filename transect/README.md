# transect

Agents build and tune things in your cluster all day, and it changes
faster than you can keep up with.  `transect` is living documentation
for that cluster.  It reads what is registered and running, so what it
shows you is true now.

When you come back to a system, `transect` shows how it works now.  It
names the patterns in your workloads, such as a work queue or a
pipeline, so you recognize a model that you already know and do not
have to read every part.  It shows what is out of place: what nothing
uses, what nothing explains, and what differs from its peers.  It shows
where a system does not work.

The cluster does not record everything, such as why a thing exists.
The people and agents who build things add that knowledge, and
`transect` checks each of their claims against the running cluster as
the cluster changes.  A claim that stops being true shows as wrong.

## The proof of concept

The code in this directory is a proof of concept of that idea, and the
design of the rest is in progress.  It is a live, read-only map of a
Kubernetes cluster.  It draws the cluster as nested layers, from the
whole cluster down to one container, and the relationships between
them: what runs on which machine, where each workload keeps its data,
which pod has claimed which device, which source each object reconciles from,
and what depends on what.  The map is gray where things are healthy,
so only a problem is colored.  It shows the descriptions that people
write, each marked with its source.  It names no patterns, and it does
not report what is out of place.

`transect` changes nothing in the cluster.  It reads the Kubernetes API
and Prometheus, and a directory of its own resources for facts that
neither of those provides.  The design of the proof of concept is in
[plans/completed/00-proof-of-concept-design.md](plans/completed/00-proof-of-concept-design.md),
and the product design that new work follows is in
[plans/00-design.md](plans/00-design.md).

## What it shows

- **The cluster map** at `/` has one band per layer, with each
  namespace drawn as a tile as wide as its workload count, the machines with
  their usage from Prometheus, and an outside-systems band for storage and other
  things outside the cluster.  The "Now" sidebar lists the problems to look at.
- **A page per namespace, workload, and pod**, under
  `/namespaces/<namespace>`, each with a card that shows what the object is, its health, what it
  depends on and what depends on it, and links to the directory that a
  GitOps tool such as Flux applies it from and the commit that its
  image was built from.  A namespace of DaemonSets draws as
  a matrix of DaemonSets by machine, and a GitOps object or a custom
  resource of a known kind gets a page of its own.
- **Edges**, which you turn on and off from a row above each picture:
  runs on, depends on, authenticates through, stores on, holds,
  reconciles from, controlled by, and allowed to call (from
  `NetworkPolicy` rules).  The map also lists loops, where objects in
  different namespaces each need another to start.  After a full
  shutdown, such as a power cut, no start order brings a loop up.
- **Health** from readiness, restarts, error lines in pod logs, a custom
  resource's own conditions, and the alerts that Alertmanager reports as
  firing, except the ones that its silences and inhibitions hide.  An
  alert colors the namespace, object, or machine that its labels name,
  and "Now" lists every alert, the most severe first.
- **A lost machine** at `/?lose=<machine>`: what goes down with it,
  what moves to other machines, what stays down until the machine
  returns, and what the dependents of those workloads lose.
- **A workload's section** at
  `/namespaces/<namespace>/<resource>/<name>/section`: one
  workload drawn from its entry point to its storage, with lines
  between the objects.
- **A pod's last log lines**, from the Kubernetes API or from a log
  store, tagged by the log patterns that match them.
- **Descriptions** that the cluster's people and operators write: why
  an object exists, what goes wrong with it, and what it provides.
  They come from annotations, overlays, and `transect`'s own
  resources, and each one is marked with its source.
- **Access** at `/access`: every port that accepts connections from outside the
  cluster, with the TLS, sign-in, and `NetworkPolicy` on the way to it,
  and what each account may do and where, read from RBAC alone.  A
  namespace and a workload have an access tab, and a `ServiceAccount`'s
  page draws what it may do and why.
- **`/resources`**: every resource and overlay `transect` loaded, with
  the conditions that say what is wrong with a resource.

`transect` does not observe traffic between workloads, so the map has
no edge for the calls that actually happen.

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
those parts are off: the usage, the alerts, and the traffic each show
a message that names the resource that turns them on, and a repository
on a forge that no resource declares is plain text.  [examples/](examples/) contains
these resources for public projects, such as Flux, cert-manager,
Traefik, and kube-prometheus, in one directory each.  Copy them into
your directory and edit them.  The design of the proof of concept
describes each resource's shape.  `cargo run -- --help` lists every flag, including
`--alertmanager` for the alerts, registry credentials and mirrors for
reading image labels, and the address of a log store.

## Running in a cluster

CI publishes the image `ghcr.io/liken-sh/transect`, and the kustomize
base in [deploy/](deploy/) as the OCI artifact
`ghcr.io/liken-sh/transect-deploy`, under the same version.  A release
takes the version of its tag, such as `2026.11.17-001`, and moves
`:latest` on the image and on the artifact.  A push to `main` that
changes `transect` publishes a development build, such as
`2026.11.17-001-dev-017-abcdef01`, which is the build of commit `abcdef01`,
17 commits after `2026.11.17-001`.  A development build never
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

The image is built `FROM scratch`.  It contains the binary, the glibc
libraries that the binary loads, a CA bundle, and the license notices.
The base runs it as user 65534 on a read-only root filesystem.

The base contains a `ServiceAccount`, read-only `ClusterRole`s and
their bindings, one `Deployment`, a `Service` on port 80, and a
`NetworkPolicy` that refuses every caller.  It sets no namespace and
includes no `Ingress`.  An overlay sets `namespace:`, adds an `Ingress`
behind the cluster's sign-in and a `NetworkPolicy` rule that admits
its ingress controller, and sets the addresses of Prometheus,
Alertmanager, and the log store in environment variables:

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
cannot read `Secret`s.  For custom kinds, add a `ClusterRole` with the
label `transect.liken.sh/aggregate-to-reader: "true"` that grants access
to the API groups that your CRDs serve.  `/resources` lists each kind
that `transect` may not list.  The `transect-pod-logs` `ClusterRole`
grants reading pod logs separately, so an overlay can delete it and
read logs from a `LogSource` instead.

`transect` listens only after its first list of the cluster is done.
From then on `/livez` and `/readyz` always return `ok`, including
during an API server outage.  When the sign-in
is down, connect to `transect` with `kubectl port-forward svc/transect
8080:80` or with the workstation loop above.
