# A cluster without helm retries the Gateway API CRD manifest

Open bug. On a cluster whose `spec.features` turns on neither `helm` nor
a feature that requires it, k3s fails to apply its bundled
`gateway-api-crd.yaml` and retries it, with every other bundled manifest,
about every 15 seconds. The machine prints each attempt on its console,
so a quiet cluster shows a scrolling loop from its first boot.

## What the manifest is

k3s v1.37 ships `/var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml`,
and the deploy controller applies it like the other bundled manifests.
It holds a `HelmChart`, not the CRDs themselves. Its `Addon` records
the kind:

```yaml
apiVersion: k3s.cattle.io/v1
kind: Addon
metadata:
  annotations:
    addon.k3s.cattle.io/gvks: helm.cattle.io/v1, Kind=HelmChart
  name: gateway-api-crd
  namespace: kube-system
spec:
  source: /var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml
```

The chart is `https://%{KUBERNETES_API}%/static/charts/gateway-api-crd-1.6.103.tgz`.

When `helm` is off, `init/k3s.go` writes `disable-helm-controller: true`,
so the server serves no `helm.cattle.io` API. The apply of a
`HelmChart` then fails with "the server could not find the requested
resource". `DisabledComponents` disables the bundled features that are
off, and `gateway-api-crd` is not one of them, so k3s keeps the
manifest and retries it.

## Evidence

A one-machine cluster on release 2026.10.28-003, k3s v1.37.1+k3s1, with
no features. The events in `kube-system` for the `Addon`, from the first
boot:

```
19 ApplyingManifest: Applying manifest at "/var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml" (2026-10-29T17:30:13Z to 2026-10-29T17:34:54Z)
19 ApplyManifestFailed: Applying manifest at "/var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml" failed: the server could not find the requested resource (2026-10-29T17:30:13Z to 2026-10-29T17:34:54Z)
```

The other bundled manifests repeated with it, 13 times each in the
first four minutes: `runtimes`, `rolebindings`, `machines-crd`, and
`machinedisruptionbudgets-crd` among them. The console shows the same
failure from k3s:

```
failed to process /var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml ...
```

Every pod ran with no restart, and the Machine reported `Converged`, so
the loop did no harm beyond the noise and the work of each retry.

Turning on `traefik` ended it. That feature requires `helm`, so the
Helm controller started, and the next apply succeeded:

```
1 AppliedManifest: Applied manifest at "/var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml" (2026-10-29T17:35:10Z)
1 ApplyJob: Applying HelmChart from https://%{KUBERNETES_API}%/static/charts/gateway-api-crd-1.6.103.tgz using Job kube-system/helm-install-gateway-api-crd (2026-10-29T17:35:10Z)
```

The cluster then held 10 Gateway API CRDs, and no failure followed.

## Not decided

- Whether `gateway-api-crd` belongs on the disable list whenever `helm`
  is off, or whether it becomes a bundled feature of its own. Whether
  k3s accepts that name in `disable` was not checked.
- Whether a cluster with Traefik on should get the Gateway API CRDs
  from this chart, from Traefik's chart, or from both, as it does now.
