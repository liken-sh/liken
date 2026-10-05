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
    "indi",
    "indi-simulators",
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
  platforms  = ["linux/amd64"]
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
  platforms  = ["linux/amd64"]
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
  context    = "vulkan"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20260928"
  }
  tags       = ["ghcr.io/liken-sh/vulkan:20260928-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache", "type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache-20260928-2"]
  cache-to = concat(
    CACHE_WRITE == "vulkan" ? ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "vulkan" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/vulkan:buildcache-20260928-2,mode=max,ignore-error=true"] : [],
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
  tags       = ["ghcr.io/liken-sh/vaapi:20260928-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache", "type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache-20260928-2"]
  cache-to = concat(
    CACHE_WRITE == "vaapi" ? ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "vaapi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/vaapi:buildcache-20260928-2,mode=max,ignore-error=true"] : [],
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
  tags       = ["ghcr.io/liken-sh/ffmpeg:20260928-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache", "type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache-20260928-2"]
  cache-to = concat(
    CACHE_WRITE == "ffmpeg" ? ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "ffmpeg" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/ffmpeg:buildcache-20260928-2,mode=max,ignore-error=true"] : [],
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
  tags       = ["ghcr.io/liken-sh/weston:20260928-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache", "type=registry,ref=ghcr.io/liken-sh/weston:buildcache-20260928-2"]
  cache-to = concat(
    CACHE_WRITE == "weston" ? ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "weston" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/weston:buildcache-20260928-2,mode=max,ignore-error=true"] : [],
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
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache"]
  cache-to   = CACHE_WRITE == "display-operator" ? ["type=registry,ref=ghcr.io/liken-sh/display-operator:buildcache,mode=max,ignore-error=true"] : []
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
  platforms  = ["linux/amd64"]
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
  contexts = {
    "kubernetes" = "kubernetes"
  }
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

target "indi" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi" ? ["type=registry,ref=ghcr.io/liken-sh/indi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-simulators" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-simulators"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-simulators:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-simulators" ? ["type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-simulators" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-simulators:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-open" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-open"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-open:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-open" ? ["type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-open" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-open:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-zwo" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-zwo"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-zwo:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-zwo" ? ["type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-zwo" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-zwo:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-qhy" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-qhy"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-qhy:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-qhy" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-qhy" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qhy:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-playerone" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-playerone"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-playerone:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-playerone" ? ["type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-playerone" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-playerone:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-svbony" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-svbony"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-svbony:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-svbony" ? ["type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-svbony" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-svbony:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-atik" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-atik"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-atik:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-atik" ? ["type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-atik" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-atik:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-fli" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-fli"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-fli:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-fli" ? ["type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-fli" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-fli:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-sbig" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-sbig"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-sbig:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-sbig" ? ["type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-sbig" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-sbig:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-mi" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-mi"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-mi:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-mi" ? ["type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-mi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-mi:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-qsi" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-qsi"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-qsi:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-qsi" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-qsi" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-qsi:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-apogee" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-apogee"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-apogee:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-apogee" ? ["type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-apogee" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-apogee:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-astroasis" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-astroasis"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-astroasis:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-astroasis" ? ["type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-astroasis" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-astroasis:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-gphoto" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-gphoto"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-gphoto:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-gphoto" ? ["type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-gphoto" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-gphoto:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "indi-touptek" {
  context    = "indi"
  dockerfile = "Dockerfile"
  target     = "indi-touptek"
  platforms  = ["linux/amd64"]
  args = {
    VERSION = "20261005"
  }
  tags       = ["ghcr.io/liken-sh/indi-touptek:20261005-1"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache", "type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache-20261005-1"]
  cache-to = concat(
    CACHE_WRITE == "indi-touptek" ? ["type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "indi-touptek" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/indi-touptek:buildcache-20261005-1,mode=max,ignore-error=true"] : [],
  )
}

target "library-operator" {
  context    = "library-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
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
  contexts = {
    "ffmpeg" = "target:ffmpeg"
    "kubernetes" = "kubernetes"
  }
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
    "media-screen" = "media-screen"
    "vulkan" = "target:vulkan"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-media-browser:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-media-browser" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-media-browser:buildcache,mode=max,ignore-error=true"] : []
}

target "library-operator-appearances" {
  context    = "library-operator"
  dockerfile = "Dockerfile.appearances"
  platforms  = ["linux/amd64"]
  contexts = {
    "appearances" = "library-operator/appearances"
    "ffmpeg" = "target:ffmpeg"
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-appearances:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-appearances:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-appearances" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-appearances:buildcache,mode=max,ignore-error=true"] : []
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
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/library-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "library-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/library-operator-cli:buildcache,mode=max,ignore-error=true"] : []
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
  tags       = ["ghcr.io/liken-sh/mpv:20260928-2"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache", "type=registry,ref=ghcr.io/liken-sh/mpv:buildcache-20260928-2"]
  cache-to = concat(
    CACHE_WRITE == "mpv" ? ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache,mode=max,ignore-error=true"] : [],
    BRANCH_CACHE == "mpv" && CACHE_WRITE == "" ? ["type=registry,ref=ghcr.io/liken-sh/mpv:buildcache-20260928-2,mode=max,ignore-error=true"] : [],
  )
}

target "media-operator" {
  context    = "media-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
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
    "kubernetes" = "kubernetes"
    "mpv" = "target:mpv"
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
    "media-screen" = "media-screen"
    "vulkan" = "target:vulkan"
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
    "media-screen" = "media-screen"
    "vulkan" = "target:vulkan"
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
  contexts = {
    "ffmpeg" = "target:ffmpeg"
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-api:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-api" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-api:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-capabilities" {
  context    = "media-operator"
  dockerfile = "Dockerfile.capabilities"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
    "vaapi" = "target:vaapi"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-capabilities:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-capabilities:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-capabilities" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-capabilities:buildcache,mode=max,ignore-error=true"] : []
}

target "media-operator-cli" {
  context    = "media-operator"
  dockerfile = "Dockerfile.cli"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
  args = {
    VERSION = VERSION
  }
  tags       = ["ghcr.io/liken-sh/media-operator-cli:${VERSION}"]
  cache-from = ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache"]
  cache-to   = CACHE_WRITE == "media-operator-cli" ? ["type=registry,ref=ghcr.io/liken-sh/media-operator-cli:buildcache,mode=max,ignore-error=true"] : []
}

target "people-operator" {
  context    = "people-operator"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "kubernetes" = "kubernetes"
  }
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
