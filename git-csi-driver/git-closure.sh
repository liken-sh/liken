#!/bin/sh
# Collects git, ssh, and every file the two of them open at runtime
# into one directory tree, so the image ships that tree and nothing
# else. Run it in a builder that has git and openssh-client
# installed, with the output directory as the argument.
#
# The functions come from vulkan/closure.sh, the closure library of
# every image on a Debian builder. The Dockerfile copies it from the
# build context named builder.
set -eu

# shellcheck source=../vulkan/closure.sh
. "$(dirname "$0")/closure.sh"

out=$1

# ldd reports the DT_NEEDED graph and nothing about a program that a
# running program execs by name. Every seed below is such a file, and
# each one is named with the driver command that reaches it.
#
# /usr/bin/git is the program runGit starts, by name, so it has to be
# on the PATH the container gets.
#
# /usr/lib/git-core/git is git's copy of itself on its exec path. git
# re-execs it as `git <subcommand>` for the work a command farms out:
# `git gc` runs maintenance, pack-objects, and rev-list that way, a
# fetch runs index-pack and rev-list, a push runs pack-objects, and a
# rebase runs reset and notes copy. Without it every one of those
# commands fails with "git: 'maintenance' is not a git command".
# Debian ships it as a second copy of the git binary. The closure
# replaces the copy with a link below.
#
# git-remote-https is the transport program for an https URL, which
# is what the store's fetch and the work tree's push run against a
# forge. It is a link to git-remote-http, the real binary, and the
# hops walk below copies both. Without it a fetch fails with "Unable
# to find remote helper for 'https'".
#
# git-upload-pack and git-receive-pack serve a fetch and a push whose
# URL is a local path. git runs each one through a shell. Both are
# links to git on the exec path, so they cost two links and no bytes.
#
# ssh is the transport a deploy key uses. credentials.go builds
# GIT_SSH_COMMAND around it.
#
# /bin/sh is dash, and git runs three things through it: the
# credential helper credentials.go writes, which is a `#!/bin/sh`
# script; GIT_SSH_COMMAND, which git always passes to a shell; and
# the local-path transport's `git-upload-pack '<path>'` line. Without
# it a token fetch, a key fetch, and a local-path fetch all fail. The
# image holds a shell for that reason and no other. Nothing in the
# pod spec runs one.
seeds="
/usr/bin/git
/usr/lib/git-core/git
/usr/lib/git-core/git-remote-https
/usr/lib/git-core/git-upload-pack
/usr/lib/git-core/git-receive-pack
/usr/bin/ssh
/bin/sh
"

# Two paths a program opens by name at runtime, so nothing in the
# library graph points at them.
#
# The CA bundle is what libcurl-gnutls, which git-remote-http links,
# verifies a forge's certificate against. Debian builds this path in
# as libcurl's default, so git needs no GIT_SSL_CAINFO to find it.
#
# The templates directory is what git init copies into a new
# repository. The driver reads none of what it holds, and commit.go
# writes info/exclude itself, but git init prints "warning: templates
# not found in /usr/share/git-core/templates" without it, and the
# driver runs git init for the store and again for every work tree.
# The directory is small, and it keeps the report of git init, the
# command a volume starts with, clean.
data="
/usr/share/git-core/templates
"

collect "$out" "$seeds" "$data"

# The exec-path git is byte for byte the /usr/bin one, so a link
# replaces the second copy. git resolves its exec path through
# the link the same way, which the release gate's gc run proves.
if cmp -s "$out/usr/bin/git" "$out/usr/lib/git-core/git"; then
	ln -sf ../../bin/git "$out/usr/lib/git-core/git"
fi

# ssh looks up the uid it runs as and refuses to start when no entry
# answers, with "No user exists for uid". The manifests in deploy/
# name no runAsUser, so both pods run as root, and 65534 is the uid a
# cluster owner sets first when it sets one. Two lines cover both. The
# home directory is / because nothing in the image writes under a
# home, and the shell is the one the image ships.
cat >"$out/etc/passwd" <<'PASSWD'
root:x:0:0:root:/:/bin/sh
nobody:x:65534:65534:nobody:/:/bin/sh
PASSWD

# The group file beside it, because a passwd entry names a gid.
cat >"$out/etc/group" <<'GROUP'
root:x:0:
nogroup:x:65534:
GROUP

notices "$out" git-csi-driver
