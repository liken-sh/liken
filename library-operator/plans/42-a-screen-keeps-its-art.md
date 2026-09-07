# A screen keeps its art

Plan 42. A screen's art cache takes a `PersistentVolumeClaim` beside
the catalog claim plan 32 gave it, so a screen that restarts draws the
wall from art it already scaled. The `Catalog` sizes the
claim and classes it with the same field that classes the catalog
claim. This changes what plan 32 built in the screen pod.

## The problem

The browser scales every piece of art once, posters, backdrops,
episode stills, logos, and headshots alike, and keeps the scaled file
on disk, on an `emptyDir` capped at `640Mi` with a 512 MiB budget
inside it. The `emptyDir` dies with the pod. So every roll of the operator, a
template change, or a reboot starts the browser with an empty cache,
and the first pass over every wall reads the full-size art off the
library mounts and scales it again. Plan 32 solved the same problem
for the rows: the catalog claim cut a restart from 157 s to one
second. The art is the half it left on the `emptyDir`.

The pool is there. Every `liken` machine that declares `podStorage`
serves the `local-path` class from its own partition. The testbed
holds 32Gi on one node and 4Gi on the other, and the main cluster
holds 8Gi on every machine.

## The contract

- **Every screen gets an art claim.** A screen pod in a namespace
  with one `Catalog` mounts a `PersistentVolumeClaim` at the art
  cache path, named `<player>-media-browser-art`, `ReadWriteOnce`,
  owned by the `Player` as the catalog claim is. A namespace with no
  single `Catalog` keeps the `emptyDir`, as it keeps one for the
  agent.
- **The `Catalog` sizes it.** `Catalog.spec.screens.artCache.size`
  is a quantity, and the default is `2Gi` when the block is absent.
  It is a field of its own and not `spec.storage.size`, because the
  rows and the art have different sizes and different lives.
- **The class is the screens' class.** The claim takes
  `Catalog.spec.screens.storageClassName`, the field plan 32 added,
  because both claims of a screen belong on the node that holds the
  display. An absent value binds the cluster's default, as before.

  ```yaml
  spec:
    storage:
      size: 1Gi
      storageClassName: local-path
    screens:
      storageClassName: local-path
      artCache:
        size: 2Gi
  ```

- **The browser's budget follows the claim.** The browser takes a new
  flag, `--cache-budget BYTES`, and the operator passes the claim's
  size less 128 MiB of headroom for the temporary files an atomic
  write makes before its rename. A run with no flag keeps the 512 MiB
  default, so `local/browse` and the tests change nothing.
- **A claim is created once and left alone.** The rule the catalog
  claim follows: create it when there is none, and never update it,
  because a bound claim's spec is immutable. A size change on the
  `Catalog` therefore reaches new screens and not standing ones. A
  person who wants a standing screen resized deletes its claim, and
  the next pass creates it at the new size.
- **Recovery takes both claims.** A screen unschedulable past the
  grace on a `Bound` claim loses the pod, the catalog claim, and the
  art claim in one pass, with the same three guards on every
  delete: the derived name, the two labels, and the controller
  `ownerReference` on the `Player`.
- **The status names it.** Each entry of `Catalog.status.screens`
  gains `artClaim`, the claim the screen's art cache is on, or empty
  on an `emptyDir`.

## What this is not

- Not a shared cache. Two screens on one machine hold two claims and
  scale the same art twice. A machine-wide cache is a different
  design with a different owner.
- Not a cap the operator enforces. `local-path` enforces no size; the
  browser's own budget is the cap, and the claim's size is what the
  browser is told to keep under.

## The drill

Roll the build to `liken-1`, delete the screen pod, and measure the
first wall after the restart against the same wall today. Then delete
the art claim, watch the next pass create it again, and confirm
the pod comes back with an empty cache and fills it.
