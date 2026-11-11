# This file is generated from every package.toml and the Dockerfiles
# they name. Do not edit it: edit those files or the template in
# ci/templates/, then run `make workflows` at the top of the
# repository.
#
# Each image of the repository is one target here, named for the
# image. A Dockerfile that builds on another image of the repository
# names that image bare, as in FROM mpv, and the target's contexts map
# the name to the other image's target, so the consumer builds on the
# base of the same commit:
#
#   docker buildx bake media-operator-player --load
#
# builds mpv and every base under it first, from the layer cache on
# ghcr.io for each layer that did not change.
#
# A base of a pinned component builds from snapshot.debian.org and
# other upstream servers, and a consumer that built every base again
# would depend on each of those servers for each release. So a consumer
# takes a pinned base from ghcr.io when the base's image there is the
# build of the tree's recipe. `make bases` at the top of the repository
# checks each pinned base's published tag: the signature of its
# component's workflow, and the recipe label against the recipe of the
# tree. For each base that passes, it writes the image's reference and
# digest into docker-bake.override.hcl, which bake reads after this
# file and git ignores. A base with no such variable builds from the
# tree, so a commit that raises a base's revision builds the new base,
# and its consumers on it, until the base's tag is published. CI runs
# the same command in the plan job and gives each build its file.

# VERSION is the version a tracked image carries: the release tag, a
# development version, or "check" for a build that publishes nothing.
# A pinned image carries its own version from package.toml instead.
variable "VERSION" {
  default = "check"
}

# CACHE_WRITE names the one target whose layer cache this build writes
# to ghcr.io. CI sets it on main and in a run that publishes, and a
# build of a consumer never writes the cache of the bases under it.
variable "CACHE_WRITE" {
  default = ""
}

# BRANCH_CACHE names the one target whose layers a build off main
# writes to the branch cache on ghcr.io, and only a pinned base writes
# one. A base's job runs before its consumers' jobs, so a consumer on a
# branch that raises a base's revision builds the base from the layers
# that the base's job wrote, where :buildcache has none of them yet.
# The branch cache's tag is :buildcache-<version>-<revision>, the
# revision that the branch builds: two branches that build different
# revisions write different tags, and a published revision's layers
# are in :buildcache from then on. Every build reads both tags.
variable "BRANCH_CACHE" {
  default = ""
}

variable "PUBLISHED_ASTROMETRY" {
  default = ""
}

variable "PUBLISHED_FFMPEG" {
  default = ""
}

variable "PUBLISHED_MPV" {
  default = ""
}

variable "PUBLISHED_TRUST" {
  default = ""
}

variable "PUBLISHED_VAAPI" {
  default = ""
}

variable "PUBLISHED_VULKAN" {
  default = ""
}

variable "PUBLISHED_WESTON" {
  default = ""
}

# The toolchains target maps the bare name of each build image in
# toolchains/images.toml to its reference and digest. No image builds
# from it, and it is in no group. A bake file beside a Dockerfile whose
# image is never published adds its target with
# inherits = ["toolchains"], so that Dockerfile names its outside
# images bare too, and a bump of images.toml reaches it.
target "toolchains" {
  contexts = {
    "buildkit" = "docker-image://moby/buildkit:v0.34.2@sha256:c5f8862f52499773af85bc07695b2379ef08e8002352b0df2c4864c91d78dabe"
    "buildkit-syft-scanner" = "docker-image://docker/buildkit-syft-scanner:1.12.1@sha256:873a7458c3c8ac8e32c7b796c10e1600b07b963d2aa000a72a2d97aaaa2d6315"
    "buildroot" = "docker-image://registry.gitlab.com/buildroot.org/buildroot/base:20250218.2110@sha256:340aa0de68e8e13e4eca70a00aaf94ec68d3a3891c28f3e7495d844829a8c958"
    "corrosion" = "docker-image://ghcr.io/liken-sh/corrosion:2026.10.31-001-dev-007-5a95a51c@sha256:2a812f831aca60f4ba97cbf4e038e3dcb3878d1f93a5680efb3affb7ed4f5d79"
    "cosign" = "docker-image://ghcr.io/sigstore/cosign/cosign:v3.1.4@sha256:0f06154836d2de34f887a5c273506d52ca43edaa1d9e53e6510fa78a48f69c2d"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "distroless" = "docker-image://gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "grype" = "docker-image://anchore/grype:v0.121.3@sha256:13113eeceab017260370cf540e1d25442c59665fd07d1b09b66d63bb0c282887"
    "oras" = "docker-image://ghcr.io/oras-project/oras:v1.3.6@sha256:9cfb578be975b762fd898b9d004243d8003aeb807a3e15b2ed212cdd1326c9eb"
    "rust" = "docker-image://rust:1.99.1-trixie@sha256:09027f873d45b8ecd1dc86f1fa13b7a62b875c961f017a0e331267b350430137"
    "syft" = "docker-image://anchore/syft:v1.55.0@sha256:4cc92b64ca74ffca4c94d4c8fcc01f217731375300be14d0b6eeb2e82eaa110a"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
}

group "default" {
  targets = [
    "vulkan",
    "astrometry",
    "trust",
    "indi",
    "indi-simulators",
    "indi-phd2",
    "indi-open",
    "indi-zwo",
    "indi-qhy",
    "indi-playerone",
    "indi-svbony",
    "indi-atik",
    "indi-fli",
    "indi-sbig",
    "indi-mi",
    "indi-qsi",
    "indi-apogee",
    "indi-astroasis",
    "indi-gphoto",
    "indi-touptek",
    "weston",
    "observatory-operator",
    "observatory-operator-cli",
    "astrophotography-operator",
    "astrophotography-operator-worker",
    "audio-operator",
    "audio-operator-cli",
    "bluetooth-operator",
    "bluetooth-bondfetch",
    "bluetoothd",
    "bluetooth-operator-cli",
    "vaapi",
    "ffmpeg",
    "display-operator",
    "display-capture",
    "display-api",
    "display-operator-cli",
    "equipment-operator",
    "firmware-operator",
    "firmware-operator-scan",
    "git-csi-driver",
    "library-operator",
    "library-operator-ffmpeg",
    "library-operator-media-browser",
    "library-operator-appearances",
    "library-operator-corrosion",
    "library-operator-cli",
    "mpv",
    "media-operator",
    "media-operator-player",
    "media-operator-idle",
    "media-operator-display",
    "media-operator-api",
    "media-operator-capabilities",
    "media-operator-cli",
    "people-operator",
    "per-node-csi-driver",
  ]
}

target "vulkan" {
  description = "The Vulkan loader, the Intel and AMD Vulkan drivers, and the Wayland client libraries, on scratch."
  context    = "vulkan"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
  }
  args = {
    VERSION = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/vulkan:20261101-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache", "type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache-20261101-2"]
  cache-to = concat(
    CACHE_WRITE == "vulkan" ? ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "vulkan" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache-20261101-2,mode=max,ignore-error=true"] : [],
  )
}

target "astrometry" {
  description = "The astrometry.net solver and SourceExtractor, with every library they load, for the plate solving of astrophotography-operator."
  context    = "astrometry"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
  }
  args = {
    VERSION = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/astrometry:20261101-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/astrometry:buildcache", "type=registry,ref=ghcr.io/liken-sh/astrometry:buildcache-20261101-2"]
  cache-to = concat(
    CACHE_WRITE == "astrometry" ? ["type=registry,ref=ghcr.io/liken-sh/astrometry:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "astrometry" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/astrometry:buildcache-20261101-2,mode=max,ignore-error=true"] : [],
  )
}

target "trust" {
  description = "Mozilla's CA bundle, in the form curl publishes it, at the path where OpenSSL, GnuTLS, libcurl, and Go read it."
  context    = "trust"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "manifest" = "toolchains/manifest"
  }
  args = {
    VERSION = "20261020"
  }
  tags       = ["ghcr.io/liken-sh/trust:20261020-4"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/trust:buildcache", "type=registry,ref=ghcr.io/liken-sh/trust:buildcache-20261020-4"]
  cache-to = concat(
    CACHE_WRITE == "trust" ? ["type=registry,ref=ghcr.io/liken-sh/trust:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "trust" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/trust:buildcache-20261020-4,mode=max,ignore-error=true"] : [],
  )
}

target "indi" {
  description = "indiserver, socat, and every program in indi-bin: the core INDI drivers, simulators, and tools."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi" ? ["type=registry,ref=ghcr.io/liken-sh/indi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-simulators" {
  description = "The GSC star catalog and the gsc program, which the INDI CCD and guide simulators read to draw stars."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-simulators"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-simulators:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-simulators" ? ["type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-simulators" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-phd2" {
  description = "PHD2, the guider, as a client of an INDI server."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-phd2"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-phd2:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-phd2:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-phd2:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-phd2" ? ["type=registry,ref=ghcr.io/liken-sh/indi-phd2:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-phd2" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-phd2:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-open" {
  description = "The third-party INDI drivers that link no vendor SDK."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-open"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-open:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-open" ? ["type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-open" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-zwo" {
  description = "INDI drivers for ZWO cameras, EFW filter wheels, EAF focusers, the CAA rotator, and the USB-to-ST4 adapter."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-zwo"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-zwo:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-zwo" ? ["type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-zwo" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-qhy" {
  description = "INDI drivers for QHY cameras."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-qhy"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-qhy:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-qhy" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-qhy" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-playerone" {
  description = "INDI drivers for Player One cameras and the Player One filter wheel."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-playerone"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-playerone:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-playerone" ? ["type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-playerone" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-svbony" {
  description = "INDI drivers for SVBony cameras on SVBony's own SDK."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-svbony"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-svbony:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-svbony" ? ["type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-svbony" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-atik" {
  description = "INDI drivers for Atik cameras and filter wheels."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-atik"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-atik:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-atik" ? ["type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-atik" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-fli" {
  description = "INDI drivers for Finger Lakes Instrumentation cameras, focusers, and filter wheels, and the Kepler cameras."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-fli"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-fli:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-fli" ? ["type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-fli" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-sbig" {
  description = "INDI drivers for SBIG cameras."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-sbig"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-sbig:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-sbig" ? ["type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-sbig" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-mi" {
  description = "INDI drivers for Moravian Instruments cameras and filter wheels."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-mi"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-mi:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-mi" ? ["type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-mi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-qsi" {
  description = "INDI drivers for Quantum Scientific Imaging cameras."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-qsi"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-qsi:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-qsi" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-qsi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-apogee" {
  description = "INDI drivers for Apogee cameras and filter wheels."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-apogee"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-apogee:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-apogee" ? ["type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-apogee" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-astroasis" {
  description = "INDI drivers for the Astroasis Oasis focuser and filter wheel."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-astroasis"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-astroasis:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-astroasis" ? ["type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-astroasis" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-gphoto" {
  description = "INDI drivers for Canon, Nikon, Sony, Fuji, and Pentax cameras through gphoto2."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-gphoto"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-gphoto:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-gphoto" ? ["type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-gphoto" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-touptek" {
  description = "INDI drivers for the eleven camera brands that use the ToupTek SDK, including ToupTek, Altair, Omegon, and SVBony."
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-touptek"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
    "ubuntu" = "docker-image://ubuntu:26.04@sha256:6a06111c3b0a8600feadb270aec771dc239bf407b25625f93f44ab00e11f4d3c"
  }
  args = {
    VERSION = "20261111"
  }
  tags       = ["ghcr.io/liken-sh/indi-touptek:20261111-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache-20261111-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-touptek" ? ["type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-touptek" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache-20261111-1,mode=max,ignore-error=true"] : [],
  )
}

target "weston" {
  description = "The Weston compositor on the vulkan image, with liken's hotplug shim and its ivi-shell layout controller."
  context    = "weston"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "manifest" = "toolchains/manifest"
    "vulkan" = PUBLISHED_VULKAN != "" ? "docker-image://${PUBLISHED_VULKAN}" : "target:vulkan"
  }
  args = {
    VERSION = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/weston:20261101-3"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache", "type=registry,ref=ghcr.io/liken-sh/weston:buildcache-20261101-3"]
  cache-to = concat(
    CACHE_WRITE == "weston" ? ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "weston" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache-20261101-3,mode=max,ignore-error=true"] : [],
  )
}

target "observatory-operator" {
  description = "observatory-operator, which runs an observatory's mount, cameras, and other INDI equipment on the cluster."
  context    = "observatory-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/observatory-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/observatory-operator:buildcache"]
  cache-to   = CACHE_WRITE == "observatory-operator" ? ["type=registry,ref=ghcr.io/liken-sh/observatory-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "observatory-operator-cli" {
  description = "kubectl-liken-observatory, the kubectl plugin that the liken toolkit pulls from this image."
  context    = "observatory-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64"]
  contexts = {
    "distroless" = "docker-image://gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/observatory-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/observatory-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "observatory-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/observatory-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "astrophotography-operator" {
  description = "astrophotography-operator, which takes the pictures you ask for on a telescope that observatory-operator runs."
  context    = "astrophotography-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "observatory" = "observatory-operator"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/astrophotography-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/astrophotography-operator:buildcache"]
  cache-to   = CACHE_WRITE == "astrophotography-operator" ? ["type=registry,ref=ghcr.io/liken-sh/astrophotography-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "astrophotography-operator-worker" {
  description = "astrophotography-operator's program on the astrometry image, for the solver pods and the workers that extract stars from each frame."
  context    = "astrophotography-operator"
  dockerfile = "Dockerfile.worker"
  platforms  = ["linux/amd64"]
  contexts = {
    "astrometry" = PUBLISHED_ASTROMETRY != "" ? "docker-image://${PUBLISHED_ASTROMETRY}" : "target:astrometry"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "observatory" = "observatory-operator"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/astrophotography-operator-worker:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/astrophotography-operator-worker:buildcache"]
  cache-to   = CACHE_WRITE == "astrophotography-operator-worker" ? ["type=registry,ref=ghcr.io/liken-sh/astrophotography-operator-worker:buildcache,mode=max,ignore-error=true"] : []
}

target "audio-operator" {
  description = "audio-operator and the PipeWire and WirePlumber daemons that its pod runs to play and record sound on a machine."
  context    = "audio-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
    SNAPSHOT = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/audio-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/audio-operator:buildcache"]
  cache-to   = CACHE_WRITE == "audio-operator" ? ["type=registry,ref=ghcr.io/liken-sh/audio-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "audio-operator-cli" {
  description = "kubectl-liken-audio, the kubectl plugin that the liken toolkit pulls from this image."
  context    = "audio-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64"]
  contexts = {
    "distroless" = "docker-image://gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/audio-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/audio-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "audio-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/audio-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetooth-operator" {
  description = "bluetooth-operator, which pairs Bluetooth controllers and remotes and gives each one to one pod."
  context    = "bluetooth-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetooth-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator:buildcache"]
  cache-to   = CACHE_WRITE == "bluetooth-operator" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetooth-bondfetch" {
  description = "The init container that restores a Bluetooth adapter's bonds from its Secret before bluetoothd starts."
  context    = "bluetooth-operator"
  dockerfile = "bondfetch/Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetooth-bondfetch:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetooth-bondfetch:buildcache"]
  cache-to   = CACHE_WRITE == "bluetooth-bondfetch" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-bondfetch:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetoothd" {
  description = "BlueZ's bluetoothd, dbus-daemon, and btmon as static programs, with the entrypoints that bluetooth-operator's pod runs."
  context    = "bluetooth-operator"
  dockerfile = "bluetoothd/Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "buildroot" = "docker-image://registry.gitlab.com/buildroot.org/buildroot/base:20250218.2110@sha256:340aa0de68e8e13e4eca70a00aaf94ec68d3a3891c28f3e7495d844829a8c958"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "manifest" = "toolchains/manifest"
    "notices" = "notices"
    "userland" = "liken/userland"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetoothd:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetoothd:buildcache"]
  cache-to   = CACHE_WRITE == "bluetoothd" ? ["type=registry,ref=ghcr.io/liken-sh/bluetoothd:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetooth-operator-cli" {
  description = "kubectl-liken-bluetooth, the kubectl plugin that the liken toolkit pulls from this image."
  context    = "bluetooth-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64"]
  contexts = {
    "distroless" = "docker-image://gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetooth-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "bluetooth-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "vaapi" {
  description = "The VA-API loader, Intel's media driver, and Mesa's VA driver on the vulkan image, for programs that decode video on the GPU."
  context    = "vaapi"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "vulkan" = PUBLISHED_VULKAN != "" ? "docker-image://${PUBLISHED_VULKAN}" : "target:vulkan"
  }
  args = {
    VERSION = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/vaapi:20261101-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache", "type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache-20261101-2"]
  cache-to = concat(
    CACHE_WRITE == "vaapi" ? ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "vaapi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache-20261101-2,mode=max,ignore-error=true"] : [],
  )
}

target "ffmpeg" {
  description = "Debian's ffmpeg and ffprobe on the vaapi image, with every library they load."
  context    = "ffmpeg"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "vaapi" = PUBLISHED_VAAPI != "" ? "docker-image://${PUBLISHED_VAAPI}" : "target:vaapi"
  }
  args = {
    VERSION = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/ffmpeg:20261101-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache", "type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache-20261101-2"]
  cache-to = concat(
    CACHE_WRITE == "ffmpeg" ? ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "ffmpeg" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache-20261101-2,mode=max,ignore-error=true"] : [],
  )
}

target "display-operator" {
  description = "display-operator on the weston image, for the DaemonSet that runs the compositor on each machine with a monitor."
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-operator"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "weston" = PUBLISHED_WESTON != "" ? "docker-image://${PUBLISHED_WESTON}" : "target:weston"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache"]
  cache-to   = CACHE_WRITE == "display-operator" ? ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "display-capture" {
  description = "display-operator's program on the ffmpeg image, for the container that captures a Display's screenshots, clips, and streams."
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-capture"
  platforms  = ["linux/amd64"]
  contexts = {
    "ffmpeg" = PUBLISHED_FFMPEG != "" ? "docker-image://${PUBLISHED_FFMPEG}" : "target:ffmpeg"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-capture:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-capture:buildcache"]
  cache-to   = CACHE_WRITE == "display-capture" ? ["type=registry,ref=ghcr.io/liken-sh/display-capture:buildcache,mode=max,ignore-error=true"] : []
}

target "display-api" {
  description = "display-operator's program alone, for the display-api Deployment that serves the captures over HTTP."
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-api"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-api:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-api:buildcache"]
  cache-to   = CACHE_WRITE == "display-api" ? ["type=registry,ref=ghcr.io/liken-sh/display-api:buildcache,mode=max,ignore-error=true"] : []
}

target "display-operator-cli" {
  description = "kubectl-liken-display, the kubectl plugin that the liken toolkit pulls from this image."
  context    = "display-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64"]
  contexts = {
    "distroless" = "docker-image://gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "display-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/display-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "equipment-operator" {
  description = "equipment-operator, which controls a TV over HDMI-CEC and an A/V receiver over the network."
  context    = "equipment-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/equipment-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/equipment-operator:buildcache"]
  cache-to   = CACHE_WRITE == "equipment-operator" ? ["type=registry,ref=ghcr.io/liken-sh/equipment-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "firmware-operator" {
  description = "firmware-operator, which uploads firmware to the USB devices that hold none, finds firmware updates, and applies the ones you approve."
  context    = "firmware-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/firmware-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/firmware-operator:buildcache"]
  cache-to   = CACHE_WRITE == "firmware-operator" ? ["type=registry,ref=ghcr.io/liken-sh/firmware-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "firmware-operator-scan" {
  description = "fwupd and the scan program that firmware-operator runs on a machine to find, rehearse, and apply firmware updates."
  context    = "firmware-operator"
  dockerfile = "scan/Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
    SNAPSHOT = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/firmware-operator-scan:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/firmware-operator-scan:buildcache"]
  cache-to   = CACHE_WRITE == "firmware-operator-scan" ? ["type=registry,ref=ghcr.io/liken-sh/firmware-operator-scan:buildcache,mode=max,ignore-error=true"] : []
}

target "git-csi-driver" {
  description = "git-csi-driver, which mounts a git repository as a Kubernetes volume, with the git and ssh that it runs."
  context    = "git-csi-driver"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
    SNAPSHOT = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/git-csi-driver:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/git-csi-driver:buildcache"]
  cache-to   = CACHE_WRITE == "git-csi-driver" ? ["type=registry,ref=ghcr.io/liken-sh/git-csi-driver:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator" {
  description = "library-operator, which keeps a catalog of films and series and puts a media browser on every screen."
  context    = "library-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-ffmpeg" {
  description = "library-operator's pod program on the ffmpeg image, for the Jobs that probe a media file and make its trickplay images."
  context    = "library-operator"
  dockerfile = "Dockerfile.ffmpeg"
  platforms  = ["linux/amd64"]
  contexts = {
    "ffmpeg" = PUBLISHED_FFMPEG != "" ? "docker-image://${PUBLISHED_FFMPEG}" : "target:ffmpeg"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-ffmpeg:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-ffmpeg:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-ffmpeg" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-ffmpeg:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-media-browser" {
  description = "The media browser, which shows a namespace's catalog on a Player's screen and starts playback, on the vulkan image."
  context    = "library-operator/media-browser"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "media-screen" = "media-screen"
    "notices" = "notices"
    "rust" = "docker-image://rust:1.99.1-trixie@sha256:09027f873d45b8ecd1dc86f1fa13b7a62b875c961f017a0e331267b350430137"
    "toolchains" = "toolchains"
    "vulkan" = PUBLISHED_VULKAN != "" ? "docker-image://${PUBLISHED_VULKAN}" : "target:vulkan"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-media-browser:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-media-browser" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-appearances" {
  description = "The appearances tool, OpenVINO, and two face models on the ffmpeg image, for the Job that finds credited people in keyframes."
  context    = "library-operator"
  dockerfile = "Dockerfile.appearances"
  platforms  = ["linux/amd64"]
  contexts = {
    "appearances" = "library-operator/appearances"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "ffmpeg" = PUBLISHED_FFMPEG != "" ? "docker-image://${PUBLISHED_FFMPEG}" : "target:ffmpeg"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "manifest" = "toolchains/manifest"
    "notices" = "notices"
    "oras" = "docker-image://ghcr.io/oras-project/oras:v1.3.6@sha256:9cfb578be975b762fd898b9d004243d8003aeb807a3e15b2ed212cdd1326c9eb"
    "rust" = "docker-image://rust:1.99.1-trixie@sha256:09027f873d45b8ecd1dc86f1fa13b7a62b875c961f017a0e331267b350430137"
    "toolchains" = "toolchains"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-appearances:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-appearances:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-appearances" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-appearances:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-corrosion" {
  description = "Corrosion, from liken's fork, which replicates the catalog and the progress store to each scanner and media browser."
  context    = "library-operator/corrosion"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "corrosion" = "docker-image://ghcr.io/liken-sh/corrosion:2026.10.31-001-dev-007-5a95a51c@sha256:2a812f831aca60f4ba97cbf4e038e3dcb3878d1f93a5680efb3affb7ed4f5d79"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-corrosion:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-corrosion:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-corrosion" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-corrosion:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-cli" {
  description = "kubectl-liken-library, the kubectl plugin that the liken toolkit pulls from this image."
  context    = "library-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64"]
  contexts = {
    "distroless" = "docker-image://gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "mpv" {
  description = "mpv, built from its release, on the ffmpeg image, with every library and data file it opens by name."
  context    = "mpv"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "ffmpeg" = PUBLISHED_FFMPEG != "" ? "docker-image://${PUBLISHED_FFMPEG}" : "target:ffmpeg"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "manifest" = "toolchains/manifest"
  }
  args = {
    VERSION = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/mpv:20261101-3"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache", "type=registry,ref=ghcr.io/liken-sh/mpv:buildcache-20261101-3"]
  cache-to = concat(
    CACHE_WRITE == "mpv" ? ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "mpv" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache-20261101-3,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator" {
  description = "media-operator, and the pod build of the same program that each sidecar runs."
  context    = "media-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator:${VERSION}", "ghcr.io/liken-sh/media-operator-sidecar:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-player" {
  description = "mpv, the brand's fonts, and media-operator's player shim, for the pod that plays a film on a Player's screen."
  context    = "media-operator"
  dockerfile = "Dockerfile.player"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "mpv" = PUBLISHED_MPV != "" ? "docker-image://${PUBLISHED_MPV}" : "target:mpv"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
    SNAPSHOT = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/media-operator-player:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-player:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-player" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-player:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-idle" {
  description = "The idle screen that a Player shows while nothing plays, on the vulkan image."
  context    = "media-operator"
  dockerfile = "Dockerfile.screen"
  target     = "idle"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "media-screen" = "media-screen"
    "notices" = "notices"
    "rust" = "docker-image://rust:1.99.1-trixie@sha256:09027f873d45b8ecd1dc86f1fa13b7a62b875c961f017a0e331267b350430137"
    "toolchains" = "toolchains"
    "vulkan" = PUBLISHED_VULKAN != "" ? "docker-image://${PUBLISHED_VULKAN}" : "target:vulkan"
  }
  args = {
    VERSION = VERSION
    SNAPSHOT = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/media-operator-idle:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-idle:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-idle" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-idle:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-display" {
  description = "The display that media-operator draws over a playing film, with the scrubber, the clock, the track menus, and what plays next, on the vulkan image."
  context    = "media-operator"
  dockerfile = "Dockerfile.screen"
  target     = "display"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "builder" = "vulkan"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "media-screen" = "media-screen"
    "notices" = "notices"
    "rust" = "docker-image://rust:1.99.1-trixie@sha256:09027f873d45b8ecd1dc86f1fa13b7a62b875c961f017a0e331267b350430137"
    "toolchains" = "toolchains"
    "vulkan" = PUBLISHED_VULKAN != "" ? "docker-image://${PUBLISHED_VULKAN}" : "target:vulkan"
  }
  args = {
    VERSION = VERSION
    SNAPSHOT = "20261101"
  }
  tags       = ["ghcr.io/liken-sh/media-operator-display:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-display:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-display" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-display:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-api" {
  description = "media-operator's HTTP API on the ffmpeg image, which captures a Player's screen and sound."
  context    = "media-operator"
  dockerfile = "Dockerfile.api"
  platforms  = ["linux/amd64"]
  contexts = {
    "ffmpeg" = PUBLISHED_FFMPEG != "" ? "docker-image://${PUBLISHED_FFMPEG}" : "target:ffmpeg"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-api:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-api" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-capabilities" {
  description = "The agent that asks each render node's driver through libva what it decodes, encodes, and scales, on the vaapi image."
  context    = "media-operator"
  dockerfile = "Dockerfile.capabilities"
  platforms  = ["linux/amd64"]
  contexts = {
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "vaapi" = PUBLISHED_VAAPI != "" ? "docker-image://${PUBLISHED_VAAPI}" : "target:vaapi"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-capabilities:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-capabilities:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-capabilities" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-capabilities:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-cli" {
  description = "kubectl-liken-media, the kubectl plugin that the liken toolkit pulls from this image."
  context    = "media-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64"]
  contexts = {
    "distroless" = "docker-image://gcr.io/distroless/static:nonroot@sha256:25ac8f0c3b2334a9eb05452266bd4697864a8c8ef0ef71e5e6480d7f92868329"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "people-operator" {
  description = "people-operator, which keeps the Person objects of a cluster and decodes their pictures."
  context    = "people-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
    "trust" = PUBLISHED_TRUST != "" ? "docker-image://${PUBLISHED_TRUST}" : "target:trust"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/people-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/people-operator:buildcache"]
  cache-to   = CACHE_WRITE == "people-operator" ? ["type=registry,ref=ghcr.io/liken-sh/people-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "per-node-csi-driver" {
  description = "per-node-csi-driver, which gives a pod a directory on the node it runs on."
  context    = "per-node-csi-driver"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "notices" = "notices"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/per-node-csi-driver:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/per-node-csi-driver:buildcache"]
  cache-to   = CACHE_WRITE == "per-node-csi-driver" ? ["type=registry,ref=ghcr.io/liken-sh/per-node-csi-driver:buildcache,mode=max,ignore-error=true"] : []
}

# CI runs this target before the image's build, to copy the
# image's cache mount from .image-cache/. It is in no group.
target "bluetoothd-cache-seed" {
  context    = "bluetooth-operator"
  dockerfile = "bluetoothd/Dockerfile"
  target     = "cache-seed"
  platforms  = ["linux/amd64"]
  contexts = {
    "buildroot" = "docker-image://registry.gitlab.com/buildroot.org/buildroot/base:20250218.2110@sha256:340aa0de68e8e13e4eca70a00aaf94ec68d3a3891c28f3e7495d844829a8c958"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "manifest" = "toolchains/manifest"
    "notices" = "notices"
    "userland" = "liken/userland"
    "cache" = ".image-cache/bluetoothd"
  }
  output = ["type=cacheonly"]
}

# CI runs this target after the image's build, to copy the
# image's cache mount into .image-cache/. It is in no group.
target "bluetoothd-cache-export" {
  context    = "bluetooth-operator"
  dockerfile = "bluetoothd/Dockerfile"
  target     = "cache-export"
  platforms  = ["linux/amd64"]
  contexts = {
    "buildroot" = "docker-image://registry.gitlab.com/buildroot.org/buildroot/base:20250218.2110@sha256:340aa0de68e8e13e4eca70a00aaf94ec68d3a3891c28f3e7495d844829a8c958"
    "debian" = "docker-image://debian:sid-slim@sha256:5ac67a4d7c0de96e83873e4dd8f7ea3b507ba292fb2cce60a0c3f73322df5cae"
    "golang" = "docker-image://golang:1.27.4-trixie@sha256:b54b2639de8c1bfe538ec5ffc45f29b1932235ac5f6d63c059853646677112d9"
    "kubernetes" = "kubernetes"
    "manifest" = "toolchains/manifest"
    "notices" = "notices"
    "userland" = "liken/userland"
  }
  output = ["type=local,dest=.image-cache/bluetoothd"]
}
