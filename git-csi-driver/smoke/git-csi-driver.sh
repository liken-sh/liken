#!/usr/bin/env bash
# Proves the closure of the git-csi-driver image before it ships.
#
# Usage: git-csi-driver.sh <image>
#
#   <image>  the full reference of the git-csi-driver image, tagged
#            with its version, for example
#            ghcr.io/liken-sh/git-csi-driver:2026.10.02-001. The image
#            must be in the local Docker daemon. The check reads the
#            version from the tag and requires the binary to report
#            it, so a reference by digest or by :latest fails.
#
# The image is a closure on scratch, so nothing in it came from a
# package manager, and a file the closure script dropped shows up only
# when a command opens it. This check runs each such command by name
# before the image can ship. audio-operator's check proves its closure
# the same way.
#
# The check reaches github.com over https, so it needs the network.
set -euo pipefail
export LC_ALL=C

image=$1
version=${image##*:}

work=$(mktemp -d)
trap 'rm -rf "$work"' EXIT

# Every path in the image, as a process in it sees them.
container=$(docker create "$image")
docker export "$container" | tar -t | sed 's|^|/|; s|/$||' | sort -u > "$work/image-files.txt"
docker rm "$container" > /dev/null

# The programs git execs by name from its exec path. `git-remote-https`
# serves an https URL, `git-upload-pack` and `git-receive-pack` serve a
# URL that is a local path, the git beside them is what git re-execs
# for gc, fetch, and push, and `/bin/sh` is what git runs the
# credential helper and `GIT_SSH_COMMAND` through. No command below
# reaches the two pack programs, so the listing is the only check on
# them. git execs the shell as `/bin/sh`, and the image reaches that
# name through the `/bin` link into `/usr`, so the export holds the
# link and `/usr/bin/sh`.
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

# One fetch from the public forge over https. It runs
# `git-remote-https`, and its answer proves the CA bundle verifies a
# real certificate from the path git reads by default.
docker run --rm --entrypoint git "$image" \
  ls-remote https://github.com/liken-sh/liken.git HEAD \
  || { echo "the image did not reach the forge over https"; exit 1; }

# The sweep's collect, in a repository this run makes. `gc.auto=1`
# makes the one commit enough work to run, and gc farms every step out
# to the git on the exec path.
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
