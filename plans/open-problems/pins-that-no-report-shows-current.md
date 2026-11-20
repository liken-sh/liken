# Pins that no report shows as current or behind

Open problem. `make versions` and `ci pins` cover every pin in the
repository except four. Each one can fall behind upstream, or away
from another pin, and no report shows it. The rule that every pin
stays current is in the `pins` skill, under "Every pin stays current".

The document was checked against the tree on 2026-11-19:
`ls library-operator/appearances/models library-operator/titles/models`
lists no `latest.sh`, and no file of `ci/` reads
`liken.sh/.terraform.lock.hcl` or compares the syft of
`buildkit-syft-scanner` with the `syft` entry. No unit test can show a
report that does not exist.

## The first pins, as seen on 2026-11-01

- **The face models.** `library-operator/appearances/models/pin.toml`
  pins the two models of the `library-operator-appearances` image at
  one commit of OpenCV Zoo, and no `latest.sh` reports that commit or
  the models. What "current" means for these files is not decided.
  The newest tag of OpenCV Zoo, the head of its `main` branch, and the
  newest model file of each family all give different answers, and
  OpenCV Zoo publishes a newer YuNet model than the pinned one. A new
  model changes the detector digest of every detections record, so
  plan 75 in `library-operator/plans/` bears on the choice.
- **The syft inside the image scanner.** `toolchains/images.toml`
  pins `buildkit-syft-scanner`, which carries its own syft, and pins
  the `syft` CLI separately. The scanner writes the SBOM of every
  image, and the CLI writes the SBOM of the OS release, so the two
  can read the same file differently. The `go.mod` of
  `docker/buildkit-syft-scanner` at its pinned tag names its syft, and
  the `syft` entry of `images.toml` names the CLI's. No check compares
  them.
- **The Terraform providers.** `liken.sh/latest.sh` reports the
  provider versions of `liken.sh/terraform.tf`, and
  `liken.sh/.terraform.lock.hcl` holds their digests. No check in
  `ci/` fails when the lock and the versions disagree.

## A fourth pin, seen on 2026-11-17

- **The text models.** `library-operator/titles/models/pin.toml` pins
  the two PP-OCR models of the `library-operator-titles` image, and
  the list of characters that the reader reads, at one commit of each
  project that publishes them. No `latest.sh` reports those commits or
  the models. The question of what "current" means is the same as for
  the face models.
