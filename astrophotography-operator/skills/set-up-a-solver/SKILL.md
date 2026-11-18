---
name: set-up-a-solver
description: "Choose astrometry.net's index files for a camera's field, load them into a PersistentVolumeClaim, declare a Solver, and check that it is Ready with its index series. Use when a Target needs spec.centering, when adding index files, or when a Solver is not Ready."
---

This skill is the guide at https://liken.sh/astrophotography/docs/guides/set-up-a-solver/, emitted for agents. Before the first command, run `kubectl config current-context` and confirm that it names the cluster the person means.

# Set up a solver

This guide gives your targets a plate solver. At the end, a `Solver` is
`Ready` and lists the index series it loaded, and a target can use it
by setting `spec.centering.solver` to its name. [Solvers](https://liken.sh/astrophotography/docs/concepts/solvers/)
explains how a solve works and why the index files stay on one claim.

You need:

* `astrophotography-operator` installed, as
  [the install guide](https://liken.sh/astrophotography/docs/guides/install/) describes. The solver's
  pod runs the image `astrophotography-operator-worker`, with the same
  tag as the operator's image.
* A `StorageClass` that can provision a claim of a few hundred
  megabytes, such as `local-path`.
* The focal length of each optical tube you image with, and the pixel
  size of each camera.
* `kubectl` access to the namespace of your observatory. This guide
  uses `observatory`.

## Choose the index files

astrometry.net matches quads of four stars against its index files,
and each series of index files holds quads of one range of sizes.
Choose the series whose quads fit in your field: a series whose
smallest quads are wider than the field's short side cannot solve it.

The plate scale, in arcseconds per pixel, is 206.265 × the pixel size
in µm / the focal length in mm. The field's short side, in
arcminutes, is the scale × the camera's short side in pixels / 60.
The CCD simulator of `observatory-operator`'s example has pixels of
5.2 µm and 1024 rows, so behind a tube of 480 mm its scale is 2.235
arcseconds per pixel and its short side is 38.1 arcminutes.

| Series | Quads, in arcminutes | Fits a short side of |
|---|---|---|
| 4206 | 16 to 22 | 22 arcminutes or more |
| 4207 | 22 to 30 | 30 arcminutes or more |
| 4208 | 30 to 42 | 42 arcminutes or more |
| 4209 | 42 to 60 | 60 arcminutes or more |

A series of smaller quads also fits, and solves a frame with fewer
bright stars, but its files are larger. For the field of 38.1
arcminutes, 4207 and 4208 fit, and 4209 does not.

The 4200 series are built from 2MASS, and the 4100 series from
Tycho-2. 2MASS reaches fainter stars. Series 4205 to 4207 come as 12
tiles of the sky each, the smaller series as 48 tiles, and 4208 and
the larger series as one file each. Download them from
[data.astrometry.net](http://data.astrometry.net/4200/), or install
Debian's or Ubuntu's downloader packages on a computer, such as
`astrometry-data-2mass-07` for 4207 and `astrometry-data-2mass-08-19`
for 4208 and the larger series, and copy the files from
`/usr/share/astrometry`. The packages name the files by scale, such as
`index-2mass-07-00.fits` for the first tile of 4207. The solver reads
each file's series from its header, so only the pattern
`index-*.fits` matters in a name.

## Load the index files into a claim

Create a claim large enough for the files. The 13 files of 4207 and
4208 take 236 MiB:

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: v1
kind: PersistentVolumeClaim
metadata:
  name: astrometry-indexes
spec:
  accessModes: [ReadWriteOnce]
  resources:
    requests:
      storage: 512Mi
EOF
```

Start a pod that mounts the claim, and copy the files into it with
`tar` over `kubectl exec`:

```sh
kubectl run -n observatory astrometry-load --image=busybox:1.38.0 --restart=Never \
  --overrides='{"spec":{"containers":[{"name":"load","image":"busybox:1.38.0","command":["sleep","3600"],
    "volumeMounts":[{"name":"indexes","mountPath":"/indexes"}]}],
    "volumes":[{"name":"indexes","persistentVolumeClaim":{"claimName":"astrometry-indexes"}}]}}'
kubectl wait -n observatory --for=condition=Ready pod/astrometry-load
tar -C /usr/share/astrometry -cf - index-2mass-07-*.fits index-2mass-08.fits \
  | kubectl exec -i -n observatory astrometry-load -- tar -xf - -C /indexes
kubectl exec -n observatory astrometry-load -- ls -l /indexes
kubectl delete -n observatory pod astrometry-load
```

A `local-path` claim is bound to the node of the first pod that
mounts it, so the solver's pod starts on that node too. To keep the
files in a directory of a claim that holds other data, extract them
into that directory, and name it in `spec.indexFiles.path`.

## Declare the solver

```sh
kubectl apply -n observatory -f - <<'EOF'
apiVersion: astrophotography.liken.sh/v1alpha1
kind: Solver
metadata:
  name: lab
spec:
  indexFiles:
    claimName: astrometry-indexes
EOF
kubectl wait -n observatory --for=condition=Ready solver/lab --timeout=5m
```

The operator creates the `Deployment` and the `Service` `lab-solver`.
The pod reads the header of each index file, and the `Solver` lists
each series it found:

```sh
kubectl get solver -n observatory lab
```

```
NAME   READY   REASON   INDEXES     AGE
lab    True    Loaded   4207,4208   40s
```

A `Solver`'s name is at most 56 characters, because its `Service` is
named `<name>-solver`.

## Set how far a solve searches

The operator tells the solver where it expects the frame to point,
and the radius around that point to search. When a frame does not
solve, it tries each next radius of `spec.search.radii`, and then the
whole sky when `spec.search.blind` is `true`. The radii are in degrees,
and the defaults are `[5, 10]` and `true`. A frame that cannot be solved
uses up to 60 seconds of CPU time in each attempt, and the solves for
other telescopes wait until it ends. When your mount's slews end within
a degree of the target, a shorter list gives up sooner:

```yaml
spec:
  search:
    radii: [2]
    blind: false
```

## Add index files

Copy the new files into the claim as above, while the solver runs.
The solver's pod watches its directory, and loads each file when the
program that writes it closes it, or renames it into the directory.
So the solver never loads a half-copied file, and a `Job` can download
index files into the claim while the solver runs. The `Solver`'s `INDEXES` column and its `Ready` message change
when the file is loaded:

```sh
kubectl get solver -n observatory lab
```

The solver stops using a file when the file is deleted or renamed out
of the directory. It also stops using a file that is modified in
place, until the writer closes it.

The solver receives inotify events for the files that any pod on its
own node writes, and a `local-path` claim's pods all run on one node.
On a network file system, such as NFS, the solver receives no event
for a file that a pod on another node writes. In that case, restart the solver after the copy:

```sh
kubectl rollout restart -n observatory deployment/lab-solver
kubectl wait -n observatory --for=condition=Ready solver/lab --timeout=5m
```

## When the solver is not Ready

`kubectl describe solver -n observatory lab` shows the condition and
the `Event` of each transition:

| Reason | What to do |
|---|---|
| `ClaimMissing` | Create the claim that `spec.indexFiles.claimName` names, in the `Solver`'s namespace. |
| `NoIndexFiles` | The directory holds no `index-*.fits` file that the solver could read. Check `spec.indexFiles.path`, and copy the files. The `Solver` turns `Ready` when the first file is loaded. |
| `PodNotReady` | The message gives the scheduler's reason, or the error of the pod. `kubectl logs -n observatory deployment/lab-solver` shows what the solver read and each solve. |

The solver leaves out a file that matches `index-*.fits` but does not
read as an index file, or that ends before all of its data, and loads
the rest. Its log names each file it left out:

```sh
kubectl logs -n observatory deployment/lab-solver | grep 'left out'
```

Replace the file. The solver reads it again when the copy closes it.

While the `Solver` is not `Ready`, the operator images each target
that names it without centering, and each such reservation gets the
`Warning` `AstrophotographyNotCentered`.
