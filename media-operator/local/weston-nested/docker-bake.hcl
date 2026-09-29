# The nested compositor of local/video, as a bake target beside the
# repository's own. It inherits the weston target, so it builds from the
# same Debian snapshot with the same scripts, and it builds FROM the
# weston image of this checkout, never a published tag. local/video
# reads it together with the bake file at the top of the repository:
#
#   docker buildx bake -f docker-bake.hcl \
#     -f media-operator/local/weston-nested/docker-bake.hcl \
#     local-video-weston --load
target "local-video-weston" {
  inherits   = ["weston"]
  context    = "media-operator/local/weston-nested"
  dockerfile = "Dockerfile"
  contexts = {
    weston = "target:weston"
  }
  tags = ["local-video-weston:latest"]
}
