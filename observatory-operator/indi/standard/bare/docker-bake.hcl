# The image of the bare drivers. ci/ writes docker-bake.hcl at the top
# of the repository from the outputs of each package.toml, and each of
# those targets is published. This image is never published, so its
# target is here, and the make target names both files:
#
#   docker buildx bake -f docker-bake.hcl \
#     -f observatory-operator/indi/standard/bare/docker-bake.hcl indi-bare --load
#
# The trust context is the trust target of the same commit, as it is
# for the indi images.

# INDI_SNAPSHOT is the date of the snapshots in indi/package.toml.
variable "INDI_SNAPSHOT" {
  default = ""
}

target "indi-bare" {
  context    = "observatory-operator/indi/standard/bare"
  dockerfile = "Dockerfile"
  platforms  = ["linux/amd64"]
  contexts = {
    "trust" = "target:trust"
    "indi"  = "indi"
  }
  args = {
    VERSION = INDI_SNAPSHOT
  }
  tags = ["indi-bare:${INDI_SNAPSHOT}"]
}
