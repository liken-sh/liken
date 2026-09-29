# This file is generated from every package.toml and the Dockerfiles
# they name. Do not edit it: edit those files or the template in
# ci/templates/, then run `make workflows` at the top of the
# repository.
#
# Each image of the repository is one target here, named for the
# image. A Dockerfile that builds on another image of the repository
# names that image bare, as in FROM mpv, and the target's contexts map
# the name to the other image's target. So a consumer always builds on
# the base of the same commit, and never on a published tag:
#
#   docker buildx bake media-operator-player --load
#
# builds mpv and every base under it first, from the layer cache on
# ghcr.io for each layer that did not change.

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

group "default" {
  targets = [
    "audio-operator",
    "audio-operator-cli",
    "bluetooth-operator",
    "bluetooth-bondfetch",
    "bluetoothd",
    "bluetooth-operator-cli",
    "vulkan",
    "vaapi",
    "ffmpeg",
    "mpv",
    "weston",
    "display-operator",
    "display-capture",
    "display-api",
    "display-operator-cli",
    "equipment-operator",
    "git-csi-driver",
    "media-operator",
    "media-operator-player",
    "media-operator-idle",
    "media-operator-display",
    "media-operator-api",
    "media-operator-cli",
    "library-operator",
    "library-operator-ffmpeg",
    "library-operator-media-browser",
    "library-operator-corrosion",
    "library-operator-cli",
    "people-operator",
    "per-node-csi-driver",
  ]
}

target "audio-operator" {
  context    = "audio-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/audio-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/audio-operator:buildcache"]
  cache-to   = CACHE_WRITE == "audio-operator" ? ["type=registry,ref=ghcr.io/liken-sh/audio-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "audio-operator-cli" {
  context    = "audio-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64", "linux/arm64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/audio-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/audio-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "audio-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/audio-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetooth-operator" {
  context    = "bluetooth-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetooth-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator:buildcache"]
  cache-to   = CACHE_WRITE == "bluetooth-operator" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetooth-bondfetch" {
  context    = "bluetooth-operator"
  dockerfile = "bondfetch/Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetooth-bondfetch:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetooth-bondfetch:buildcache"]
  cache-to   = CACHE_WRITE == "bluetooth-bondfetch" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-bondfetch:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetoothd" {
  context    = "bluetooth-operator"
  dockerfile = "bluetoothd/Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetoothd:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetoothd:buildcache"]
  cache-to   = CACHE_WRITE == "bluetoothd" ? ["type=registry,ref=ghcr.io/liken-sh/bluetoothd:buildcache,mode=max,ignore-error=true"] : []
}

target "bluetooth-operator-cli" {
  context    = "bluetooth-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64", "linux/arm64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/bluetooth-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "bluetooth-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "vulkan" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "vulkan"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/vulkan:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache"]
  cache-to   = CACHE_WRITE == "vulkan" ? ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache,mode=max,ignore-error=true"] : []
}

target "vaapi" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "vaapi"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/vaapi:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache"]
  cache-to   = CACHE_WRITE == "vaapi" ? ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache,mode=max,ignore-error=true"] : []
}

target "ffmpeg" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "ffmpeg"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/ffmpeg:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache"]
  cache-to   = CACHE_WRITE == "ffmpeg" ? ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache,mode=max,ignore-error=true"] : []
}

target "mpv" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "mpv"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/mpv:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache"]
  cache-to   = CACHE_WRITE == "mpv" ? ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache,mode=max,ignore-error=true"] : []
}

target "weston" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "weston"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/weston:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache"]
  cache-to   = CACHE_WRITE == "weston" ? ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache,mode=max,ignore-error=true"] : []
}

target "display-operator" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-operator"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache"]
  cache-to   = CACHE_WRITE == "display-operator" ? ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "display-capture" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-capture"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-capture:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-capture:buildcache"]
  cache-to   = CACHE_WRITE == "display-capture" ? ["type=registry,ref=ghcr.io/liken-sh/display-capture:buildcache,mode=max,ignore-error=true"] : []
}

target "display-api" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-api"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-api:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-api:buildcache"]
  cache-to   = CACHE_WRITE == "display-api" ? ["type=registry,ref=ghcr.io/liken-sh/display-api:buildcache,mode=max,ignore-error=true"] : []
}

target "display-operator-cli" {
  context    = "display-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64", "linux/arm64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "display-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/display-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "equipment-operator" {
  context    = "equipment-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/equipment-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/equipment-operator:buildcache"]
  cache-to   = CACHE_WRITE == "equipment-operator" ? ["type=registry,ref=ghcr.io/liken-sh/equipment-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "git-csi-driver" {
  context    = "git-csi-driver"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/git-csi-driver:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/git-csi-driver:buildcache"]
  cache-to   = CACHE_WRITE == "git-csi-driver" ? ["type=registry,ref=ghcr.io/liken-sh/git-csi-driver:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator" {
  context    = "media-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator:${VERSION}", "ghcr.io/liken-sh/media-operator-sidecar:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-player" {
  context    = "media-operator"
  dockerfile = "Dockerfile.player"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-player:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-player:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-player" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-player:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-idle" {
  context    = "media-operator"
  dockerfile = "Dockerfile.screen"
  target     = "idle"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-idle:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-idle:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-idle" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-idle:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-display" {
  context    = "media-operator"
  dockerfile = "Dockerfile.screen"
  target     = "display"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-display:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-display:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-display" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-display:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-api" {
  context    = "media-operator"
  dockerfile = "Dockerfile.api"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-api:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-api" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-cli" {
  context    = "media-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64", "linux/arm64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator" {
  context    = "library-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-ffmpeg" {
  context    = "library-operator"
  dockerfile = "Dockerfile.ffmpeg"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-ffmpeg:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-ffmpeg:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-ffmpeg" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-ffmpeg:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-media-browser" {
  context    = "library-operator/media-browser"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "media-operator" = "media-operator"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-media-browser:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-media-browser" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-corrosion" {
  context    = "library-operator/corrosion"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-corrosion:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-corrosion:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-corrosion" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-corrosion:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-cli" {
  context    = "library-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64", "linux/arm64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "people-operator" {
  context    = "people-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/people-operator:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/people-operator:buildcache"]
  cache-to   = CACHE_WRITE == "people-operator" ? ["type=registry,ref=ghcr.io/liken-sh/people-operator:buildcache,mode=max,ignore-error=true"] : []
}

target "per-node-csi-driver" {
  context    = "per-node-csi-driver"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/per-node-csi-driver:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/per-node-csi-driver:buildcache"]
  cache-to   = CACHE_WRITE == "per-node-csi-driver" ? ["type=registry,ref=ghcr.io/liken-sh/per-node-csi-driver:buildcache,mode=max,ignore-error=true"] : []
}
