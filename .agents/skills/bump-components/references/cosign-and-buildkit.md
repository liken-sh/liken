# cosign and BuildKit

The `cosign` and `buildkit` entries of `toolchains/images.toml` are
images that no Dockerfile names. So a move of either changes no
recipe, and needs no revision.

- **`cosign`.** `liken/flux/latest.sh --bump` and
  `ci/latest.sh --bump trivy` run it with docker to verify the
  signatures of upstreams, so a flux bump needs docker. The publish
  job of every component signs each image that it pushes with it.
  `toolchains/latest.sh --bump cosign` verifies the new image with
  the pinned cosign before it writes the line. It stops unless
  sigstore's release build signed the image.
- **`buildkit`.** `ci/` writes it into the `driver-opts` of each
  `docker/setup-buildx-action` step, so CI's builders run that
  BuildKit daemon. Run `make workflows` after the bump to write it
  into the workflows.
