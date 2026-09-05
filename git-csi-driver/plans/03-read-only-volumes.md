# 03, Read-only volumes

Proposed. Low fidelity.

## The problem

A pod wants a checkout of a repository that it never writes, and it
wants the checkout to follow a ref without a sidecar. Many pods in many
namespaces may want the same repository.

## The design

- The volume is an inline ephemeral CSI volume with `readOnly: true`.
  The attributes are `url`, `ref`, `pull`, `depth`, and `offline`, as
  the design states. The driver refuses an attribute it does not know
  and a `readOnly: false` inline volume.
- The store holds one bare repository per URL, keyed by a digest of the
  URL. Every read-only volume of that URL on the node shares it. The
  published tree is a checkout of the ref in a directory per volume,
  bind-mounted read-only onto the target path.
- `pull` schedules a fetch per repository, not per volume. A fetch that
  moves the ref checks the new commit out in place under every volume
  that follows it.
- `offline: allowStale` publishes from the store when the fetch fails
  and the ref exists locally, and marks the volume's condition
  abnormal until a fetch succeeds. The default fails the publish.
- Credentials come from `nodePublishSecretRef` when the pod names one.
  The `Secret` carries an SSH private key or an HTTPS token, and the
  driver passes it to git through the environment for that call only.
- `NodeGetVolumeStats` reports the tree's size and a `VolumeCondition`.

## Considered and set aside

- **A work tree per volume with its own `.git`.** It costs a clone per
  pod and gives nothing a shared bare repository does not.
- **Following a ref with `git pull` in the checkout.** A checkout that
  pods read while it changes is exactly what the shared bare repository
  and an atomic re-checkout avoid.

## Proof

- Unit tests run against real repositories the tests create.
- In the lab, two pods mount one repository, a push to the forge
  reaches both inside `pull`, and a stopped forge leaves an
  `allowStale` pod running and a default pod refused.
