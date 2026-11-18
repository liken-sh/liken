#!/usr/bin/env bash
# Checks the git-csi-driver image before it ships.
#
# Usage: git-csi-driver.sh <image>
#
#   <image>  a reference in the local Docker daemon, tagged with the
#            version, for example
#            ghcr.io/liken-sh/git-csi-driver:2026.10.02-001. The script
#            requires the binary to report the version in the tag, so a
#            reference by digest or by :latest fails.
#
# The image is a closure on scratch. git-closure.sh copies the files of
# git and ssh into it, and no package manager checks them, so a file
# that it missed shows only when a program opens it. This script runs
# those programs. It reaches github.com over https, so it needs the
# network.
set -euo pipefail
export LC_ALL=C

image=$1

# notices/check.sh checks that the image has the license notices of
# its third-party software.
"$(dirname "$0")/../../notices/check.sh" "$image" \
  usr/share/doc/go/LICENSE \
  usr/share/doc/mozilla-ca/copyright
version=${image##*:}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Lists every path in the image as an absolute path.
container=$(docker create "$image")
docker export "$container" | tar -t | sed 's|^|/|; s|/$||' | sort -u > "$work/image-files.txt"
docker rm "$container" > /dev/null

# git runs `git-remote-https` for an https URL, `git-upload-pack` and
# `git-receive-pack` for a URL that is a local path, and the `git` in
# its exec path again for gc, fetch, and push. It runs the credential
# helper and `GIT_SSH_COMMAND` through `/bin/sh`. No command below runs
# the two pack programs, so this list is their only check. `/bin` is a
# link to `/usr/bin`, so the export lists `/bin` and `/usr/bin/sh`.
for path in \
  /usr/bin/git \
  /usr/bin/ssh \
  /bin \
  /usr/bin/sh \
  /usr/lib/git-core/git \
  /usr/lib/git-core/git-remote-https \
  /usr/lib/git-core/git-upload-pack \
  /usr/lib/git-core/git-receive-pack \
  /etc/ssl/certs/ca-certificates.crt \
  /etc/passwd
do
  grep -qx "$path" "$work/image-files.txt" \
    || { echo "the image has no $path"; exit 1; }
done

reported=$(docker run --rm "$image" --version)
grep -Fq "$version" <<< "$reported" \
  || { echo "the image reports '$reported', which does not name $version"; exit 1; }

docker run --rm --entrypoint git "$image" --version \
  || { echo "the image has no working git"; exit 1; }

docker run --rm --entrypoint ssh "$image" -V \
  || { echo "the image has no working ssh"; exit 1; }

# An ls-remote over https runs `git-remote-https`, which verifies the
# server's certificate with the CA bundle at git's default path.
docker run --rm --entrypoint git "$image" \
  ls-remote https://github.com/liken-sh/liken.git HEAD \
  || { echo "the image did not reach the forge over https"; exit 1; }

# The driver's sweep runs `git gc --auto`, and gc runs each of its steps
# as a separate program from the exec path. `gc.auto=1` sets the
# loose-object threshold of `--auto` as low as it goes.
docker run --rm --entrypoint /bin/sh "$image" -c '
  set -eu
  export GIT_CONFIG_GLOBAL=/dev/null GIT_CONFIG_SYSTEM=/dev/null LC_ALL=C
  export GIT_AUTHOR_NAME=gate GIT_AUTHOR_EMAIL=gate@liken.sh
  export GIT_COMMITTER_NAME=gate GIT_COMMITTER_EMAIL=gate@liken.sh
  git init --quiet /repo
  cd /repo
  echo collected > file
  git add file
  git commit --quiet -m "one commit"
  git -c gc.autoDetach=false -c gc.auto=1 gc --quiet --auto --prune=now
' || { echo "the image did not collect a repository"; exit 1; }
