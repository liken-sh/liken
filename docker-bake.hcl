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

# GHA_CACHE names the one target whose layers this build writes to the
# GitHub Actions cache, and any value also reads every target's layers
# from there. CI sets it in each images job on every run, branches too.
# A base's job runs before its consumers' jobs, so a consumer on a
# branch builds a new base revision from the layers that the base's
# job wrote, where ghcr.io has no cache of it yet. Only the Actions
# runtime can reach that cache, so a workstation leaves it empty.
variable "GHA_CACHE" {
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
    "weston",
    "display-operator",
    "display-capture",
    "display-api",
    "display-operator-cli",
    "equipment-operator",
    "git-csi-driver",
    "mpv",
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/audio-operator:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=audio-operator"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "audio-operator" ? ["type=registry,ref=ghcr.io/liken-sh/audio-operator:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "audio-operator" ? ["type=gha,scope=audio-operator,mode=max,ignore-error=true"] : [],
  )
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/audio-operator-cli:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=audio-operator-cli"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "audio-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/audio-operator-cli:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "audio-operator-cli" ? ["type=gha,scope=audio-operator-cli,mode=max,ignore-error=true"] : [],
  )
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=bluetooth-operator"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "bluetooth-operator" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "bluetooth-operator" ? ["type=gha,scope=bluetooth-operator,mode=max,ignore-error=true"] : [],
  )
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/bluetooth-bondfetch:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=bluetooth-bondfetch"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "bluetooth-bondfetch" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-bondfetch:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "bluetooth-bondfetch" ? ["type=gha,scope=bluetooth-bondfetch,mode=max,ignore-error=true"] : [],
  )
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/bluetoothd:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=bluetoothd"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "bluetoothd" ? ["type=registry,ref=ghcr.io/liken-sh/bluetoothd:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "bluetoothd" ? ["type=gha,scope=bluetoothd,mode=max,ignore-error=true"] : [],
  )
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator-cli:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=bluetooth-operator-cli"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "bluetooth-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/bluetooth-operator-cli:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "bluetooth-operator-cli" ? ["type=gha,scope=bluetooth-operator-cli,mode=max,ignore-error=true"] : [],
  )
}

target "vulkan" {
  context    = "vulkan"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20260928"
  }
  tags       = ["ghcr.io/liken-sh/vulkan:20260928-1"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=vulkan"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "vulkan" ? ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "vulkan" ? ["type=gha,scope=vulkan,mode=max,ignore-error=true"] : [],
  )
}

target "vaapi" {
  context    = "vaapi"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "vulkan" = "target:vulkan"
  }
  args = {
    VERSION = "20260928"
  }
  tags       = ["ghcr.io/liken-sh/vaapi:20260928-1"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=vaapi"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "vaapi" ? ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "vaapi" ? ["type=gha,scope=vaapi,mode=max,ignore-error=true"] : [],
  )
}

target "ffmpeg" {
  context    = "ffmpeg"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "vaapi" = "target:vaapi"
  }
  args = {
    VERSION = "20260928"
  }
  tags       = ["ghcr.io/liken-sh/ffmpeg:20260928-1"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=ffmpeg"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "ffmpeg" ? ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "ffmpeg" ? ["type=gha,scope=ffmpeg,mode=max,ignore-error=true"] : [],
  )
}

target "weston" {
  context    = "weston"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "vulkan" = "target:vulkan"
  }
  args = {
    VERSION = "20260928"
  }
  tags       = ["ghcr.io/liken-sh/weston:20260928-1"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=weston"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "weston" ? ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "weston" ? ["type=gha,scope=weston,mode=max,ignore-error=true"] : [],
  )
}

target "display-operator" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-operator"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
    "weston" = "target:weston"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-operator:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=display-operator"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "display-operator" ? ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "display-operator" ? ["type=gha,scope=display-operator,mode=max,ignore-error=true"] : [],
  )
}

target "display-capture" {
  context    = "display-operator"
  dockerfile = "Dockerfile"
  target     = "display-capture"
  platforms  = ["linux/amd64"]
  contexts = {
    "ffmpeg" = "target:ffmpeg"
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/display-capture:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/display-capture:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=display-capture"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "display-capture" ? ["type=registry,ref=ghcr.io/liken-sh/display-capture:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "display-capture" ? ["type=gha,scope=display-capture,mode=max,ignore-error=true"] : [],
  )
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/display-api:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=display-api"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "display-api" ? ["type=registry,ref=ghcr.io/liken-sh/display-api:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "display-api" ? ["type=gha,scope=display-api,mode=max,ignore-error=true"] : [],
  )
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
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/display-operator-cli:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=display-operator-cli"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "display-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/display-operator-cli:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "display-operator-cli" ? ["type=gha,scope=display-operator-cli,mode=max,ignore-error=true"] : [],
  )
}

target "equipment-operator" {
  context    = "equipment-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/equipment-operator:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/equipment-operator:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=equipment-operator"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "equipment-operator" ? ["type=registry,ref=ghcr.io/liken-sh/equipment-operator:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "equipment-operator" ? ["type=gha,scope=equipment-operator,mode=max,ignore-error=true"] : [],
  )
}

target "git-csi-driver" {
  context    = "git-csi-driver"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/git-csi-driver:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/git-csi-driver:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=git-csi-driver"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "git-csi-driver" ? ["type=registry,ref=ghcr.io/liken-sh/git-csi-driver:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "git-csi-driver" ? ["type=gha,scope=git-csi-driver,mode=max,ignore-error=true"] : [],
  )
}

target "mpv" {
  context    = "mpv"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "builder" = "vulkan"
    "ffmpeg" = "target:ffmpeg"
  }
  args = {
    VERSION = "20260928"
  }
  tags       = ["ghcr.io/liken-sh/mpv:20260928-1"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=mpv"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "mpv" ? ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "mpv" ? ["type=gha,scope=mpv,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator" {
  context    = "media-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator:${VERSION}", "ghcr.io/liken-sh/media-operator-sidecar:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/media-operator:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=media-operator"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "media-operator" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "media-operator" ? ["type=gha,scope=media-operator,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator-player" {
  context    = "media-operator"
  dockerfile = "Dockerfile.player"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "mpv" = "target:mpv"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-player:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/media-operator-player:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=media-operator-player"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "media-operator-player" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-player:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "media-operator-player" ? ["type=gha,scope=media-operator-player,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator-idle" {
  context    = "media-operator"
  dockerfile = "Dockerfile.screen"
  target     = "idle"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "vulkan" = "target:vulkan"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-idle:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/media-operator-idle:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=media-operator-idle"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "media-operator-idle" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-idle:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "media-operator-idle" ? ["type=gha,scope=media-operator-idle,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator-display" {
  context    = "media-operator"
  dockerfile = "Dockerfile.screen"
  target     = "display"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "vulkan" = "target:vulkan"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-display:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/media-operator-display:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=media-operator-display"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "media-operator-display" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-display:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "media-operator-display" ? ["type=gha,scope=media-operator-display,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator-api" {
  context    = "media-operator"
  dockerfile = "Dockerfile.api"
  platforms  = ["linux/amd64"]
  contexts = {
    "ffmpeg" = "target:ffmpeg"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-api:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=media-operator-api"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "media-operator-api" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "media-operator-api" ? ["type=gha,scope=media-operator-api,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator-cli" {
  context    = "media-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64", "linux/arm64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-cli:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=media-operator-cli"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "media-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "media-operator-cli" ? ["type=gha,scope=media-operator-cli,mode=max,ignore-error=true"] : [],
  )
}

target "library-operator" {
  context    = "library-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/library-operator:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=library-operator"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "library-operator" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "library-operator" ? ["type=gha,scope=library-operator,mode=max,ignore-error=true"] : [],
  )
}

target "library-operator-ffmpeg" {
  context    = "library-operator"
  dockerfile = "Dockerfile.ffmpeg"
  platforms  = ["linux/amd64"]
  contexts = {
    "ffmpeg" = "target:ffmpeg"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-ffmpeg:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/library-operator-ffmpeg:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=library-operator-ffmpeg"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "library-operator-ffmpeg" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-ffmpeg:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "library-operator-ffmpeg" ? ["type=gha,scope=library-operator-ffmpeg,mode=max,ignore-error=true"] : [],
  )
}

target "library-operator-media-browser" {
  context    = "library-operator/media-browser"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "brand" = "brand"
    "media-operator" = "media-operator"
    "vulkan" = "target:vulkan"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-media-browser:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=library-operator-media-browser"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "library-operator-media-browser" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "library-operator-media-browser" ? ["type=gha,scope=library-operator-media-browser,mode=max,ignore-error=true"] : [],
  )
}

target "library-operator-corrosion" {
  context    = "library-operator/corrosion"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-corrosion:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/library-operator-corrosion:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=library-operator-corrosion"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "library-operator-corrosion" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-corrosion:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "library-operator-corrosion" ? ["type=gha,scope=library-operator-corrosion,mode=max,ignore-error=true"] : [],
  )
}

target "library-operator-cli" {
  context    = "library-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64", "linux/arm64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-cli:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=library-operator-cli"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "library-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "library-operator-cli" ? ["type=gha,scope=library-operator-cli,mode=max,ignore-error=true"] : [],
  )
}

target "people-operator" {
  context    = "people-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/people-operator:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/people-operator:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=people-operator"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "people-operator" ? ["type=registry,ref=ghcr.io/liken-sh/people-operator:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "people-operator" ? ["type=gha,scope=people-operator,mode=max,ignore-error=true"] : [],
  )
}

target "per-node-csi-driver" {
  context    = "per-node-csi-driver"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/per-node-csi-driver:${VERSION}"]
  cache-from = concat(
    ["type=registry,ref=ghcr.io/liken-sh/per-node-csi-driver:buildcache"],
    GHA_CACHE != "" ? ["type=gha,scope=per-node-csi-driver"] : [],
  )
  cache-to = concat(
    CACHE_WRITE == "per-node-csi-driver" ? ["type=registry,ref=ghcr.io/liken-sh/per-node-csi-driver:buildcache,mode=max,ignore-error=true"] : [],
    GHA_CACHE == "per-node-csi-driver" ? ["type=gha,scope=per-node-csi-driver,mode=max,ignore-error=true"] : [],
  )
}
