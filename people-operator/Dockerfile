# people-operator runs no program, and this image runs none. It exists
# so a release has a tag in a registry: a cluster that takes releases
# through flux's image automation scans a registry for tags and never
# a git remote, so without an image under the release tag there is
# nothing for it to find. The image holds the deploy/ base, so the
# tag names the manifests it stands for and the image is not empty.
FROM scratch
COPY deploy/ /deploy/
