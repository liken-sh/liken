# Three pins that no report shows as current or behind

Open problem. `make versions` and `ci pins` cover every pin in the
repository except three. Each one can fall behind upstream, or away
from another pin, and no report shows it. The rule that every pin
stays current is in `AGENTS.md`, under "Every pin stays current".

## The three pins, as seen on 2026-11-01

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
