#!/usr/bin/env bash
# Publish one OS release to the channel at https://releases.liken.sh.
#
#   releases/publish.sh 2026.10.02-001
#
# The OS's publish job in CI runs this from liken/, after `make release`
# built the bundle into releases/dist/<version>/ and the smoke drill
# booted it. The tag is the act of release (releases/versioning.md).
# This script only carries out what the tag requires.
#
# The channel is object storage, and this script talks directly to it,
# with the scoped key that Terraform delivers as repository secrets
# (liken.sh/terraform.tf): RELEASES_ACCESS_KEY and RELEASES_SECRET_KEY.
# No machine can reach anything here until its Cluster document names
# the version and pins the digest that this run prints. Publishing
# makes bytes available. It never adopts them anywhere.
set -euo pipefail

VERSION=${1:?usage: releases/publish.sh <version>}
BUCKET=s3://releases.liken.sh
DIST=releases/dist/$VERSION

# The version must follow the CalVer grammar, and it must not be serial
# 000, which versioning.md reserves for lab bundles that are never
# published. The releases Makefile checks the grammar again.
if [[ ! "$VERSION" =~ ^[0-9]{4}\.[0-9]{2}\.[0-9]{2}-[0-9]{3}$ ]]; then
  echo "$VERSION is not a release version (yyyy.mm.dd-nnn)" >&2
  exit 1
fi
if [[ "$VERSION" == *-000 ]]; then
  echo "serial 000 is the lab stand-in and is never published" >&2
  exit 1
fi

# The release publishes the test coverage report beside the artifacts,
# and `make coverage-report` renders it from coverage.out, a profile of
# the tagged commit. A tag runs only the checks that its changes reach,
# so its own go job often does not run and writes no profile. The site
# serves the profile of its last deploy, and `ci profile` takes that
# copy when no file that the go job reads changed between that deploy
# and the tag. ci/coverage.go gives the argument. When the copy does not
# match, the job writes the profile with its own run of the tests, which
# takes minutes.
if ! (cd ../ci && go run . profile -root .. -component liken -site https://liken.sh); then
  make coverage-profile
fi
make coverage-report
cp coverage.html "$DIST/coverage.html"

# s3cmd is the uploader, because it speaks plain S3 to non-AWS endpoints
# without errors. Recent aws-cli versions attach AWS-only integrity
# checksums that Linode's S3 implementation rejects. The config names
# the endpoint, and the key comes from the environment.
sudo apt-get install -y --no-install-recommends s3cmd
cat > ~/.s3cfg <<EOF
[default]
access_key = $RELEASES_ACCESS_KEY
secret_key = $RELEASES_SECRET_KEY
host_base = us-east-1.linodeobjects.com
host_bucket = %(bucket)s.us-east-1.linodeobjects.com
use_https = True
EOF

# Releases are immutable: nothing rewrites a published version, and
# only the next serial supersedes it. A version that is already
# published fails here, before any byte moves.
if s3cmd info "$BUCKET/$VERSION/release.yaml" >/dev/null 2>&1; then
  echo "release $VERSION is already published; releases are immutable" >&2
  exit 1
fi

# The corresponding sources go up before the release does. The GPL- and
# LGPL-licensed components in the artifacts require the channel to
# offer their source from the same place it offers the binaries
# (licensing/sources.sh explains this), so no release may be
# discoverable before its sources are. The mirror is keyed by component
# version, not release version, and the target skips everything the
# channel already serves. So a release whose pins have not moved
# uploads nothing here, and when every file is already published, the
# target does not produce the sources tree at all.
make -C licensing sources
if [ -d licensing/dist/sources ]; then
  (
    cd licensing/dist/sources
    find . -type f | sort | while read -r file; do
      key="sources/${file#./}"
      if ! s3cmd info "$BUCKET/$key" >/dev/null 2>&1; then
        s3cmd put --acl-public --no-progress "$file" "$BUCKET/$key"
      fi
    done
  )
else
  echo "every source is already on the channel"
fi

# The artifacts go first and the document last: release.yaml is how
# anyone discovers the artifacts, so until it lands, the half-uploaded
# release does not exist yet. The loop reads the directory, so the
# coverage report goes up with the artifacts. It is not one of them:
# the document does not name it, and no machine downloads it onto a
# boot slot. Every object is public-read, because the channel is public
# by design, while the bucket itself refuses to list its contents, so
# only named paths answer. channel.yaml goes last of all. It is the
# channel's one mutable object: the pointer that clusters poll for the
# latest version. The immutability guard above stops a run of an old
# tag from moving that pointer backwards.
(
  cd "$DIST"
  for artifact in *; do
    [ "$artifact" = release.yaml ] && continue
    s3cmd put --acl-public --no-progress "$artifact" "$BUCKET/$VERSION/$artifact"
  done
  s3cmd put --acl-public --no-progress release.yaml "$BUCKET/$VERSION/release.yaml"
  s3cmd put --acl-public --no-progress ../channel.yaml "$BUCKET/channel.yaml"
)
s3cmd put --acl-public --no-progress --mime-type=image/x-icon \
  ../brand/static/favicon.ico "$BUCKET/favicon.ico"

# This proves the release the way a consumer meets it: it fetches the
# document over the public URL, compares it byte for byte with what was
# built, and prints the catalog entry that a Cluster commits to when it
# adopts this release. The digest is the root of the trust chain, so it
# is worth showing where a person will copy it from.
workdir=$(mktemp -d)
trap 'rm -rf "$workdir"' EXIT
curl --fail --silent --show-error --retry 5 --retry-delay 5 \
  "https://releases.liken.sh/$VERSION/release.yaml" -o "$workdir/fetched.yaml"
cmp "$workdir/fetched.yaml" "$DIST/release.yaml"
LATEST=$(curl --fail --silent --show-error https://releases.liken.sh/channel.yaml \
  | awk '/^latest:/ {print $2}')
if [[ "$LATEST" < "$VERSION" ]]; then
  echo "channel.yaml names $LATEST as latest but $VERSION just published" >&2
  exit 1
fi
DIGEST=$(sha256sum "$DIST/release.yaml" | cut -d' ' -f1)
{
  echo "## liken $VERSION"
  echo
  echo "Published to https://releases.liken.sh/$VERSION/"
  echo
  echo "Catalog entry for a Cluster's spec.releases.catalog:"
  echo
  echo '```yaml'
  echo "  - version: $VERSION"
  echo "    digest: sha256:$DIGEST"
  echo '```'
} >> "${GITHUB_STEP_SUMMARY:-/dev/stdout}"

# The notes are the subjects of the commits that changed the OS since
# its previous release. Commit messages here are written as prose, so
# the log is the changelog. The repository holds every component, so
# the log reads this directory only. The channel serves the list as
# <version>/notes.md, and the release's index page renders it, so this
# upload comes before the index. No digest names the notes and no
# machine reads them: they are announcement prose, in the same trust
# class as the pages.
PREVIOUS=$(git describe --tags --abbrev=0 \
  --match '[0-9][0-9][0-9][0-9].[0-9][0-9].[0-9][0-9]-[0-9][0-9][0-9]' \
  "$VERSION^" 2>/dev/null || true)
{
  if [ -n "$PREVIOUS" ]; then
    echo "Changes since $PREVIOUS:"
  else
    echo "Changes:"
  fi
  echo
  git log --format='- %s' ${PREVIOUS:+$PREVIOUS..}"$VERSION" -- .
} > "$workdir/changes.md"
s3cmd put --acl-public --no-progress \
  --mime-type="text/markdown; charset=utf-8" \
  "$workdir/changes.md" "$BUCKET/$VERSION/notes.md"

# The index comes after the release is published and proven, because
# it is how a person finds a release and not what makes one exist. A
# machine reads channel.yaml and release.yaml and never reads a page,
# so a run that fails here leaves a complete, adoptable release behind
# a stale index.
#
# Listing the bucket is the one part of this that needs the channel's
# key, and s3cmd already holds it. The renderer takes the key list and
# reads everything else from the public channel, so the pages describe
# what the channel serves, not what this run built. Every page is
# written again on every release, so a rerun repairs a page instead of
# only adding one.
s3cmd ls --recursive "$BUCKET/" \
  | awk '{print $4}' \
  | sed "s|^$BUCKET/||" > "$workdir/keys.txt"
"releases/build/$VERSION/cli/liken" index \
  -source https://releases.liken.sh "$workdir/pages" < "$workdir/keys.txt"
(
  cd "$workdir/pages"
  find . -type f | sort | while read -r file; do
    case "$file" in
      *.html) type="text/html; charset=utf-8" ;;
      *)      type="text/plain; charset=utf-8" ;;
    esac
    s3cmd put --acl-public --no-progress --mime-type="$type" \
      "$file" "$BUCKET/${file#./}"
  done
)
