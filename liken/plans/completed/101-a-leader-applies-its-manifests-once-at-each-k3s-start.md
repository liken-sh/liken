# 101. A leader applies its manifests once at each k3s start

Milestone 101. Proposed and built on 2026-11-21. The four drills below
ran on 2026-11-21 and passed; "Drills" gives the results.
This milestone replaces the open problem "A cluster without helm
retries the Gateway API CRD manifest", which recorded the loop on
2026-10-29, and the evidence of that record is below.

Since k3s v1.37.1, which `liken` took in 8e4be5a08 and first released
as 2026.10.25-001, every leader of a cluster without the `helm`
feature applies all of its manifests again every few seconds for as
long as k3s runs. That includes `liken`'s operators and CRDs. During a
rolling upgrade, two leaders on different releases write their own
copies over each other's, and the rollout stops.

## What k3s does

These facts were read in the source of k3s v1.37.2+k3s1, the version
in `k3s/VERSION`.

- `watcher.start` in `pkg/deploy/controller.go` sets `force := true`,
  and sets it to `false` only after a call of `listFiles` returns no
  error. While `force` is true, `listFilesIn` applies every file in
  the auto-deploy directory on each pass, and skips none for an
  unchanged modification time or checksum. A pass runs every 15
  seconds.
- k3s writes its packaged manifests into that directory at each start
  (`Stage` in `pkg/deploy/stage.go`). One of them,
  `gateway-api-crd.yaml`, is a `HelmChart` that installs the Gateway
  API CRDs. `traefik.yaml` is the only other packaged manifest that
  uses a kind that a k3s controller serves, and `liken` disables it
  unless the cluster turns on `traefik`, which turns on `helm`.
- `init/k3s.go` writes `disable-helm-controller: true` unless the
  cluster turns on `helm`. Then the API server has no `HelmChart`
  kind, and each apply of `gateway-api-crd.yaml` fails with "the
  server could not find the requested resource". So `force` never
  becomes false.
- Each server runs its own deploy controller over its own disk.
  `stageFiles` runs from `runControllers` in `pkg/server/server.go`,
  outside leader election.
- The `--disable` flag accepts `gateway-api-crd`. It is in
  `DisableItems` in `pkg/cli/cmds/stage.go`, and
  `pkg/cli/server/server.go` puts each name in both `Skips` and
  `Disables`. The deploy controller deletes a file whose name is in
  `Disables`, with the objects that its `Addon` applied and the `Addon`
  itself (`watcher.delete`). In each pass it checks `Disables` before
  it applies a file (`listFilesIn`), so it never applies a disabled
  file.
- `Stage` still writes a disabled file at the top of the directory.
  `AssetNames` in `pkg/util/bindata/embed.go` trims only `embed` from
  each path, so the names it returns begin with a slash, as in
  `/gateway-api-crd.yaml`. `Stage` looks up that name, and the name
  without its extension, in `Skips`, and neither matches. Its check of
  each parent directory still matches a disabled directory, such as
  `metrics-server`. On every boot that the drills below watched, k3s
  wrote `gateway-api-crd.yaml`, `traefik.yaml`, and `ccm.yaml`, and the
  deploy controller deleted each one in its first pass:

  ```
  02:14:05Z msg="Writing manifest: /var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml"
  02:14:08Z ... message="Deleting manifest at \".../gateway-api-crd.yaml\"" object=kube-system/gateway-api-crd
  ```

## Evidence

A one-machine cluster on 2026.10.28-003, with k3s v1.37.1+k3s1 and no
features, posted these `Event`s for the `gateway-api-crd` `Addon` in
its first five minutes:

```
19 ApplyingManifest: Applying manifest at "/var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml" (2026-10-29T17:30:13Z to 2026-10-29T17:34:54Z)
19 ApplyManifestFailed: Applying manifest at "/var/lib/rancher/k3s/server/manifests/gateway-api-crd.yaml" failed: the server could not find the requested resource (2026-10-29T17:30:13Z to 2026-10-29T17:34:54Z)
```

Turning on `traefik` started the Helm controller, the next apply
succeeded, and the loop ended.

On 2026-11-21, a `lab` fleet with two leaders on 2026.11.19-002 and
the lab's features, which leave `helm` off, upgraded node-1 to a lab
build. node-1's console has 130 lines of `Failed to process config`
between 2026-11-21T23:52:11Z and 2026-11-22T00:27:39Z, each one naming
`gateway-api-crd.yaml`. A sample of the `Addon`s' `spec.checksum`
every few seconds from 00:56:12 to 01:37:44 UTC on 2026-11-22 found
two values each for
`cluster-operator`, `clusters-crd`, `logs`, `machine-operator`, and
`machines-crd`, one from each leader's release, and no checksum for
`gateway-api-crd`. node-1's reboot turn, granted at 23:53:08, was
still held at 00:27:43, with node-2 and node-4 waiting for theirs.

## The design

`DisabledComponents` in `cluster/features.go` also returns each
packaged manifest whose controller the cluster turns off. A table,
`controllerManifests`, names each such manifest with the feature that
starts its controller. Its one entry is `gateway-api-crd` with `helm`.
`init` renders the list into `boot.yaml` on leaders, as it does now.

With `helm` off, k3s still writes `gateway-api-crd.yaml` at each
start, and the deploy controller's first pass deletes the file and the
`gateway-api-crd` `Addon` without applying the file. Then a pass can
end with no error, and the deploy controller leaves its forced mode.
After that, a leader applies a manifest only when the file changes,
which for `liken`'s files is once at each k3s start.

A cluster with `helm` on does not change. Its disable list does not
name `gateway-api-crd`, and k3s installs the Gateway API CRDs as it
does now.

## Choices

- **The disable list, not a `.skip` file.** A file named
  `gateway-api-crd.yaml.skip` in the auto-deploy directory also stops
  the apply, because `listFilesIn` skips each file whose name has a
  `.skip` beside it. k3s writes the manifest at each start either way.
  But with a `.skip` file the failed `Addon` of an earlier start stays,
  and `init` would write into the part of the directory that k3s owns.
  The disable list is the interface that k3s documents for a packaged
  manifest that should not run, and k3s already uses it the same way
  for its own `ccm` manifest when the cloud controller and servicelb
  are both off.
- **Off with `helm`, not with `traefik`.** Disabling the chart unless
  `traefik` is on would also stop it on a cluster that declares `helm`
  alone, and that cluster gets the Gateway API CRDs today. This
  milestone changes nothing on a cluster with the Helm controller.
- **A table, not a special case.** A later k3s can add another
  packaged manifest that needs a controller that `liken` turns off.
  The table is where its name goes, and the smoke drill below fails
  until it is there.

## The check

`dev-cluster/smoke.sh` reads each `Addon` in `kube-system` after the
node is Ready, and fails when one has no `spec.checksum` within its
deadline. The deploy controller creates an `Addon` before it applies
the file, and writes the checksum only after an apply succeeds, so an
`Addon` without one is a manifest that has never applied. CI's `build`
job runs `make smoke-uefi` and `make smoke-bios` on each build of the
OS, and the lab's `Cluster` leaves `helm` off. So a k3s bump that adds
a manifest like `gateway-api-crd` fails CI. The failure names the
`Addon`s and prints the last `Failed to process config` line from the
console.

The check does not see a manifest that applied once and fails after
its file changes, because that `Addon` keeps its old checksum. A
smoke drill boots a new cluster, where no file changes.

A signal on the running cluster, such as a condition that
`machine-operator` sets when an `Addon` has no checksum, was set
aside. It needs a new watch and a new condition, and the fault it
reports comes from a k3s bump, which the smoke drill already checks
before a release.

## What this leaves open

- A leader on an older release still applies its own manifests over
  a newer leader's at each start of its k3s. A leader on a release
  before this milestone does so on every pass until it upgrades.
  `plans/open-problems/an-older-leader-applies-its-manifests-over-a-newer-leaders.md`
  records this.
- On a cluster that turned on `traefik`, the `kube-system/gateway-api-crd`
  `HelmChart` remains when `traefik` is removed, so the `NoHelmCharts`
  precondition of `helm` (`machine-operator/retraction.go`) does not
  hold, and `helm` stays on. This milestone does not change that.

## Tests

- `cluster/features_test.go`: the disable list names `gateway-api-crd`
  with no cluster document, with no features, and with
  `metrics-server` alone, and does not name it with `helm` or with
  `traefik`.
- `init/k3s_test.go`: the rendered `disable:` list of a leader with no
  features, and with no cluster document, starts with
  `gateway-api-crd`.

## Drills

1. `make smoke-uefi` and `make smoke-bios` pass, and print
   `k3s applied every manifest in its auto-deploy directory`. The
   console log of the disk boot has no `Failed to process config`
   line after the first passes, and
   `kubectl get addons -n kube-system` lists no `gateway-api-crd`.
2. The same drills, run on a tree without the change to
   `cluster/features.go`, fail with
   `k3s did not apply these manifests: gateway-api-crd`.
3. On a `lab` fleet with two leaders on 2026.11.19-002 and the lab's
   features, upgrade to a build with this milestone. When node-1 boots
   the new build, the `liken-cluster-operator` `Deployment`'s
   `metadata.generation` rises once for node-1's apply and once more
   when node-2's next pass writes its own copy back. It then stays
   constant while node-2 waits for its turn. The `gateway-api-crd`
   `Addon` comes back within one pass of node-2's and keeps no
   checksum. node-2's turn is granted and the rollout finishes. When
   node-2 boots the new build, the `Deployment` moves to the new
   images, and the `gateway-api-crd` `Addon` is deleted and does not
   come back.
4. On a `lab` fleet on this build with `traefik` declared, the
   `gateway-api-crd` `Addon` has a checksum, and the cluster serves
   the Gateway API CRDs.

The drills ran on 2026-11-21, with the results that the list above
expects:

1. `make smoke-uefi` and `make smoke-bios` passed. Each printed
   `k3s applied every manifest in its auto-deploy directory`.
2. `make smoke-uefi` on a tree without the change to
   `cluster/features.go` failed with
   `k3s did not apply these manifests: gateway-api-crd`.
3. A `lab` fleet with two leaders and one follower, with the Helm
   controller off, upgraded from the published 2026.11.19-002 to a
   build with this milestone in about 3 minutes. The
   `liken-cluster-operator` `Deployment`'s `metadata.generation` went
   1, 2, 3, 4, and no machine held its reboot turn longer than about
   80 seconds. The same upgrade without this milestone had not finished
   after 40 minutes, and the generation had passed 223.
4. The drill declared `helm` rather than `traefik`, which starts the
   same controller. The `gateway-api-crd` `Addon` applied, and the
   cluster served the ten Gateway API CRDs.
